package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"jarvis/internal/config"
	"jarvis/internal/permissions"
)

const helpText = `Commands:
  open <chrome|vscode|notepad>  Open an installed application
  create <file> <text>          Create a new file
  read <file>                   Read a file
  append <file> <text>          Append text to a file
  delete <file>                 Delete a file
  permissions                   Show saved permission policies
  permission read add <folder>  Approve a folder for file reads
  permission read remove <folder> Revoke a folder's read permission
  system                        Show CPU, memory, and battery information
  help                          Show this help
  exit                          Quit the assistant

Quote paths or text containing spaces with double quotes. File paths are relative
to the workspace and cannot access files outside it.
Natural-language requests and conversation use the configured AI provider.

Hindi examples: क्रोम खोलो, सिस्टम जानकारी, फाइल बनाओ <नाम> <टेक्स्ट>,
फाइल पढ़ो <नाम>.`

var ErrUnknownCommand = errors.New("unknown command")

type Result struct {
	Output       string
	Exit         bool
	Confirmation string
}

type Engine struct {
	root        string
	launch      func(string, ...string) error
	ai          *aiAssistant
	permissions *permissions.Store
}

func (e *Engine) ClearConversation() {
	if e.ai != nil {
		e.ai.history = nil
	}
}

func New(workspace string) (*Engine, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect workspace: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("workspace must be a directory")
	}

	cfg := config.Load()
	permissionStore, err := permissions.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("initialize permissions: %w", err)
	}
	if err := permissionStore.Initialize(root); err != nil {
		_ = permissionStore.Close()
		return nil, fmt.Errorf("initialize permissions: %w", err)
	}
	return &Engine{
		root:        root,
		launch:      startApplication,
		ai:          newAIAssistant(cfg),
		permissions: permissionStore,
	}, nil
}

func (e *Engine) Execute(input string) (Result, error) {
	return e.execute(input, false)
}

func (e *Engine) ExecuteConfirmed(input string) (Result, error) {
	return e.execute(input, true)
}

func (e *Engine) Close() error {
	if e.permissions == nil {
		return nil
	}
	return e.permissions.Close()
}

func (e *Engine) execute(input string, confirmed bool) (Result, error) {
	originalInput := input
	input = normalizeHindiCommand(input)
	fields, err := parseFields(input)
	if err != nil {
		return Result{}, err
	}
	if len(fields) == 0 {
		return Result{}, nil
	}

	switch strings.ToLower(fields[0]) {
	case "help":
		if len(fields) != 1 {
			return Result{}, errors.New("usage: help")
		}
		return Result{Output: helpText}, nil
	case "exit", "quit":
		if len(fields) != 1 {
			return Result{}, errors.New("usage: exit")
		}
		return Result{Output: "Goodbye.", Exit: true}, nil
	case "open":
		if len(fields) != 2 {
			return e.respondToAI(originalInput)
		}
		app := strings.ToLower(fields[1])
		if app != "chrome" && app != "vscode" && app != "notepad" {
			return Result{}, errors.New("app must be chrome, vscode, or notepad")
		}
		allowed, err := e.permissionAllowed("open_app", "*")
		if err != nil {
			return Result{}, err
		}
		if !allowed {
			return Result{}, errors.New("opening applications is not permitted")
		}
		if err := e.launch(app); err != nil {
			return Result{}, fmt.Errorf("open %s: %w", app, err)
		}
		return Result{Output: "Opening " + app + "."}, nil
	case "create":
		if len(fields) < 3 || (len(fields) > 3 && originalInput == input) ||
			(len(fields) > 1 && (strings.EqualFold(fields[1], "a") || strings.EqualFold(fields[1], "an") || strings.EqualFold(fields[1], "the"))) {
			return e.respondToAI(originalInput)
		}
		return e.create(fields[1], strings.Join(fields[2:], " "))
	case "read":
		if len(fields) != 2 {
			return e.respondToAI(originalInput)
		}
		return e.read(fields[1])
	case "append":
		if len(fields) < 3 || (len(fields) > 3 && originalInput == input) {
			return e.respondToAI(originalInput)
		}
		return e.append(fields[1], strings.Join(fields[2:], " "))
	case "delete":
		if len(fields) != 2 {
			return e.respondToAI(originalInput)
		}
		return e.delete(fields[1], confirmed)
	case "permissions":
		if len(fields) != 1 {
			return Result{}, errors.New("usage: permissions")
		}
		return e.listPermissions()
	case "permission":
		return e.managePermission(fields)
	case "system":
		if len(fields) != 1 {
			return e.respondToAI(originalInput)
		}
		return Result{Output: systemSummary(platformStats())}, nil
	default:
		if strings.EqualFold(fields[0], "run") {
			return Result{}, fmt.Errorf("%w: %s (type help for commands)", ErrUnknownCommand, fields[0])
		}
		if e.ai == nil {
			return Result{}, fmt.Errorf("%w: %s (type help for commands)", ErrUnknownCommand, fields[0])
		}
		return e.respondToAI(originalInput)
	}
}

func (e *Engine) permissionAllowed(action, resource string) (bool, error) {
	decision, err := e.permissions.Decision(action, resource)
	if err != nil {
		return false, err
	}
	switch decision {
	case permissions.Allow:
		return true, nil
	case permissions.Confirm:
		return false, fmt.Errorf("%s requires confirmation", action)
	default:
		return false, nil
	}
}

func (e *Engine) listPermissions() (Result, error) {
	entries, err := e.permissions.List()
	if err != nil {
		return Result{}, err
	}
	var output strings.Builder
	output.WriteString("Saved permissions:\n")
	for _, entry := range entries {
		fmt.Fprintf(&output, "  %-12s %-7s %s\n", entry.Action, entry.Decision, entry.Resource)
	}
	output.WriteString("  Gmail, email sending, and payments are unavailable until their integrations are configured.")
	return Result{Output: output.String()}, nil
}

func (e *Engine) managePermission(fields []string) (Result, error) {
	if len(fields) != 4 || !strings.EqualFold(fields[1], "read") {
		return Result{}, errors.New("usage: permission read <add|remove> <folder>")
	}
	folder, err := filepath.Abs(fields[3])
	if err != nil {
		return Result{}, fmt.Errorf("resolve read folder: %w", err)
	}
	folder = filepath.Clean(folder)
	if resolved, resolveErr := filepath.EvalSymlinks(folder); resolveErr == nil {
		folder = resolved
	} else if strings.EqualFold(fields[2], "add") {
		return Result{}, fmt.Errorf("resolve read folder: %w", resolveErr)
	}

	switch strings.ToLower(fields[2]) {
	case "add":
		info, err := os.Stat(folder)
		if err != nil {
			return Result{}, fmt.Errorf("inspect read folder: %w", err)
		}
		if !info.IsDir() {
			return Result{}, errors.New("read permission requires a directory")
		}
		if err := e.permissions.Set("read_file", folder, permissions.Allow); err != nil {
			return Result{}, err
		}
		return Result{Output: "Approved reads from " + folder + "."}, nil
	case "remove":
		if err := e.permissions.Set("read_file", folder, permissions.Deny); err != nil {
			return Result{}, err
		}
		return Result{Output: "Revoked read permission for " + folder + "."}, nil
	default:
		return Result{}, errors.New("permission action must be add or remove")
	}
}

func (e *Engine) respondToAI(input string) (Result, error) {
	if e.ai == nil {
		return Result{}, fmt.Errorf("%w: type help for commands", ErrUnknownCommand)
	}
	reply, err := e.ai.respond(input, e.runAITool)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: reply}, nil
}

func (e *Engine) runAITool(name string, arguments json.RawMessage) (string, error) {
	switch name {
	case "open_app":
		var args struct {
			App       *string `json:"app"`
			Directory *string `json:"directory"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.App == nil {
			return "", errors.New("app is required")
		}
		if *args.App != "chrome" && *args.App != "vscode" && *args.App != "notepad" {
			return "", errors.New("app must be chrome, vscode, or notepad")
		}
		var launchArgs []string
		if args.Directory != nil {
			if *args.App != "vscode" {
				return "", errors.New("a directory can only be opened with vscode")
			}
			directory, err := e.workspaceDirectory(*args.Directory)
			if err != nil {
				return "", err
			}
			launchArgs = append(launchArgs, directory)
		}
		if err := e.launch(*args.App, launchArgs...); err != nil {
			return "", fmt.Errorf("open %s: %w", *args.App, err)
		}
		return "Opening " + *args.App + ".", nil
	case "create_file":
		var args struct {
			File *string `json:"file"`
			Text *string `json:"text"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.File == nil || *args.File == "" {
			return "", errors.New("file is required")
		}
		if args.Text == nil {
			return "", errors.New("text is required")
		}
		result, err := e.create(*args.File, *args.Text)
		return result.Output, err
	case "read_file":
		var args struct {
			File *string `json:"file"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.File == nil || *args.File == "" {
			return "", errors.New("file is required")
		}
		result, err := e.read(*args.File)
		return result.Output, err
	case "append_file":
		var args struct {
			File *string `json:"file"`
			Text *string `json:"text"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.File == nil || *args.File == "" {
			return "", errors.New("file is required")
		}
		if args.Text == nil {
			return "", errors.New("text is required")
		}
		result, err := e.append(*args.File, *args.Text)
		return result.Output, err
	case "create_react_project":
		var args struct {
			ProjectName *string `json:"project_name"`
		}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		if args.ProjectName == nil {
			return "", errors.New("project_name is required")
		}
		return e.createReactProject(*args.ProjectName)
	case "system_info":
		var args struct{}
		if err := decodeToolArguments(arguments, &args); err != nil {
			return "", err
		}
		return systemSummary(platformStats()), nil
	default:
		return "", fmt.Errorf("unsupported AI tool %q", name)
	}
}

func (e *Engine) workspaceDirectory(name string) (string, error) {
	if name == "." {
		return e.root, nil
	}
	relative, err := e.relativePath(name)
	if err != nil {
		return "", err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(e.root, relative))
	if err != nil {
		return "", fmt.Errorf("resolve workspace directory: %w", err)
	}
	if !withinRoot(e.root, path) {
		return "", errors.New("directory must stay inside the workspace")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect workspace directory: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("path must refer to a directory")
	}
	return path, nil
}

func (e *Engine) createReactProject(name string) (string, error) {
	name, err := normalizeReactProjectName(name)
	if err != nil {
		return "", err
	}
	allowed, err := e.permissionAllowed("create_file", e.root)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", errors.New("creating files is not permitted in this workspace")
	}
	relative, err := e.relativePath(name)
	if err != nil {
		return "", err
	}
	projectPath := filepath.Join(e.root, relative)
	if err := os.Mkdir(projectPath, 0755); err != nil {
		return "", fmt.Errorf("create project directory: %w", err)
	}
	sourcePath := filepath.Join(projectPath, "src")
	if err := os.Mkdir(sourcePath, 0755); err != nil {
		return "", fmt.Errorf("create project source directory: %w", err)
	}

	packageJSON, err := json.MarshalIndent(struct {
		Name         string            `json:"name"`
		Version      string            `json:"version"`
		Private      bool              `json:"private"`
		Type         string            `json:"type"`
		Scripts      map[string]string `json:"scripts"`
		Dependencies map[string]string `json:"dependencies"`
		DevDeps      map[string]string `json:"devDependencies"`
	}{
		Name:         name,
		Version:      "1.0.0",
		Private:      true,
		Type:         "module",
		Scripts:      map[string]string{"dev": "vite", "build": "vite build", "preview": "vite preview"},
		Dependencies: map[string]string{"react": "latest", "react-dom": "latest"},
		DevDeps:      map[string]string{"vite": "latest"},
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode project package manifest: %w", err)
	}

	files := []struct {
		path    string
		content string
	}{
		{path: "package.json", content: string(packageJSON) + "\n"},
		{path: "index.html", content: `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>My Portfolio</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.jsx"></script>
  </body>
</html>
`},
		{path: filepath.Join("src", "main.jsx"), content: `import React from "react";
import { createRoot } from "react-dom/client";
import App from "./App.jsx";
import "./index.css";

createRoot(document.getElementById("root")).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
`},
		{path: filepath.Join("src", "App.jsx"), content: `const projects = [
  { title: "Featured project", description: "A project you're proud to share." },
  { title: "Another project", description: "A little more about what you can build." },
];

export default function App() {
  return (
    <main>
      <nav><a className="brand" href="#">Portfolio</a><a href="#projects">Projects</a><a href="#contact">Contact</a></nav>
      <section className="hero">
        <p className="eyebrow">DESIGNER · DEVELOPER · CREATOR</p>
        <h1>Hi, I'm <span>Your Name.</span></h1>
        <p>I build thoughtful digital experiences that make a difference.</p>
        <a className="button" href="#projects">Explore my work <span aria-hidden="true">→</span></a>
      </section>
      <section id="projects">
        <p className="eyebrow">SELECTED WORK</p><h2>Things I've made</h2>
        <div className="projects">{projects.map((project) => (
          <article className="card" key={project.title}><h3>{project.title}</h3><p>{project.description}</p></article>
        ))}</div>
      </section>
      <footer id="contact"><h2>Let's make something great.</h2><a href="mailto:hello@example.com">hello@example.com</a></footer>
    </main>
  );
}
`},
		{path: filepath.Join("src", "index.css"), content: `:root { font-family: Inter, ui-sans-serif, system-ui, sans-serif; color: #17211c; background: #f7f8f4; font-synthesis: none; }
* { box-sizing: border-box; }
body { margin: 0; }
main { max-width: 1080px; margin: auto; padding: 0 32px; }
nav { height: 88px; display: flex; align-items: center; gap: 30px; }
nav a, footer a { color: inherit; text-decoration: none; }
.brand { margin-right: auto; font-size: 20px; font-weight: 750; }
.hero { min-height: 540px; display: flex; flex-direction: column; justify-content: center; align-items: flex-start; }
.eyebrow { color: #6b7b6e; font-size: 12px; font-weight: 700; letter-spacing: .14em; }
h1 { max-width: 760px; margin: 12px 0; font-size: clamp(52px, 9vw, 94px); line-height: 1.02; letter-spacing: -.06em; }
h1 span { color: #64866d; }
.hero > p:not(.eyebrow) { max-width: 460px; color: #647069; font-size: 19px; line-height: 1.6; }
.button { margin-top: 20px; padding: 14px 19px; border-radius: 6px; color: white; background: #263d30; text-decoration: none; }
.button span { margin-left: 12px; }
section#projects { padding: 72px 0; }
h2 { font-size: 36px; letter-spacing: -.04em; }
.projects { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 18px; }
.card { min-height: 180px; padding: 24px; border: 1px solid #e4e9e2; border-radius: 10px; background: white; }
.card p { color: #647069; line-height: 1.6; }
footer { margin-top: 56px; padding: 50px 0; border-top: 1px solid #e4e9e2; }
@media (max-width: 600px) { main { padding: 0 20px; } nav { gap: 16px; } .hero { min-height: 440px; } }
`},
	}
	for _, file := range files {
		if _, err := e.create(filepath.Join(name, file.path), file.content); err != nil {
			return "", fmt.Errorf("project folder created but scaffold failed: %w", err)
		}
	}
	return "Created React project " + name + ". Run npm install and npm run dev in the project directory.", nil
}

func normalizeReactProjectName(name string) (string, error) {
	name = strings.ToLower(strings.Join(strings.Fields(name), "-"))
	if name == "" || strings.ContainsAny(name, `/\<>:"|?*`) {
		return "", errors.New("project name must be a single folder name")
	}
	for index, char := range name {
		valid := char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '.' || char == '_' || char == '-'
		if !valid || (index == 0 && !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9')) {
			return "", errors.New("project name must use letters, numbers, dots, underscores, or hyphens")
		}
	}
	if len(name) > 214 {
		return "", errors.New("project name must be 214 characters or fewer")
	}
	switch strings.SplitN(name, ".", 2)[0] {
	case "con", "prn", "aux", "nul", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
		"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9":
		return "", errors.New("project name is reserved on Windows")
	}
	return name, nil
}

func normalizeHindiCommand(input string) string {
	command := strings.ToLower(strings.TrimSpace(input))
	phrases := map[string]string{
		"मदद":    "help",
		"सहायता": "help",
		"सिस्टम जानकारी": "system",
		"सिस्टम दिखाओ":   "system",
		"बाहर निकलो":     "exit",
		"बंद हो जाओ":     "exit",
	}
	if translated, ok := phrases[command]; ok {
		return translated
	}

	appNames := map[string]string{
		"क्रोम":               "chrome",
		"गूगल क्रोम":          "chrome",
		"वीएस कोड":            "vscode",
		"विजुअल स्टूडियो कोड": "vscode",
		"नोटपैड":              "notepad",
	}
	if strings.HasSuffix(command, " खोलो") {
		appName := strings.TrimSpace(strings.TrimSuffix(command, " खोलो"))
		if app, ok := appNames[appName]; ok {
			return "open " + app
		}
	}

	for _, alias := range []struct {
		prefix string
		target string
	}{
		{prefix: "फाइल पढ़ो ", target: "read"},
		{prefix: "फ़ाइल पढ़ो ", target: "read"},
		{prefix: "फाइल हटाओ ", target: "delete"},
		{prefix: "फ़ाइल हटाओ ", target: "delete"},
	} {
		if strings.HasPrefix(command, alias.prefix) {
			return alias.target + " " + strings.TrimSpace(strings.TrimPrefix(command, alias.prefix))
		}
	}

	for _, alias := range []struct {
		prefix string
		target string
	}{
		{prefix: "फाइल बनाओ ", target: "create"},
		{prefix: "फ़ाइल बनाओ ", target: "create"},
		{prefix: "फाइल में जोड़ो ", target: "append"},
		{prefix: "फ़ाइल में जोड़ो ", target: "append"},
	} {
		if strings.HasPrefix(command, alias.prefix) {
			nameAndText := strings.TrimSpace(strings.TrimPrefix(command, alias.prefix))
			name, text, ok := strings.Cut(nameAndText, " ")
			if ok && name != "" && strings.TrimSpace(text) != "" {
				return alias.target + " " + name + " " + strings.TrimSpace(text)
			}
		}
	}
	return input
}

func (e *Engine) create(name, content string) (Result, error) {
	allowed, err := e.permissionAllowed("create_file", e.root)
	if err != nil {
		return Result{}, err
	}
	if !allowed {
		return Result{}, errors.New("creating files is not permitted in this workspace")
	}
	path, err := e.newFilePath(name)
	if err != nil {
		return Result{}, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return Result{}, fmt.Errorf("create file: %w", err)
	}
	_, writeErr := file.WriteString(content)
	closeErr := file.Close()
	if writeErr != nil {
		if removeErr := os.Remove(path); removeErr != nil {
			return Result{}, errors.Join(
				fmt.Errorf("write file: %w", writeErr),
				fmt.Errorf("remove incomplete file: %w", removeErr),
			)
		}
		return Result{}, fmt.Errorf("write file: %w", writeErr)
	}
	if closeErr != nil {
		if removeErr := os.Remove(path); removeErr != nil {
			return Result{}, errors.Join(
				fmt.Errorf("close file: %w", closeErr),
				fmt.Errorf("remove incomplete file: %w", removeErr),
			)
		}
		return Result{}, fmt.Errorf("close file: %w", closeErr)
	}
	return Result{Output: "Created " + name + "."}, nil
}

func (e *Engine) read(name string) (Result, error) {
	path, err := e.readableFilePath(name)
	if err != nil {
		return Result{}, err
	}
	decision, err := e.permissions.ReadFolderDecision(path)
	if err != nil {
		return Result{}, err
	}
	if decision != permissions.Allow {
		return Result{}, fmt.Errorf("reading %s is not permitted; approve its folder with permission read add", name)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read file: %w", err)
	}
	return Result{Output: string(content)}, nil
}

func (e *Engine) append(name, content string) (Result, error) {
	allowed, err := e.permissionAllowed("append_file", e.root)
	if err != nil {
		return Result{}, err
	}
	if !allowed {
		return Result{}, errors.New("appending files is not permitted in this workspace")
	}
	path, err := e.existingFilePath(name)
	if err != nil {
		return Result{}, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return Result{}, fmt.Errorf("open file: %w", err)
	}
	_, writeErr := file.WriteString(content)
	closeErr := file.Close()
	if writeErr != nil {
		return Result{}, fmt.Errorf("append to file: %w", writeErr)
	}
	if closeErr != nil {
		return Result{}, fmt.Errorf("close file: %w", closeErr)
	}
	return Result{Output: "Updated " + name + "."}, nil
}

func (e *Engine) delete(name string, confirmed bool) (Result, error) {
	path, err := e.existingFilePath(name)
	if err != nil {
		return Result{}, err
	}
	decision, err := e.permissions.Decision("delete_file", e.root)
	if err != nil {
		return Result{}, err
	}
	if decision == permissions.Deny {
		return Result{}, errors.New("deleting files is not permitted in this workspace")
	}
	if decision == permissions.Confirm && !confirmed {
		return Result{Confirmation: fmt.Sprintf("Delete %q? This cannot be undone.", name)}, nil
	}
	if err := os.Remove(path); err != nil {
		return Result{}, fmt.Errorf("delete file: %w", err)
	}
	return Result{Output: "Deleted " + name + "."}, nil
}

func (e *Engine) readableFilePath(name string) (string, error) {
	var path string
	if filepath.IsAbs(name) {
		linkInfo, err := os.Lstat(name)
		if err != nil {
			return "", fmt.Errorf("inspect file: %w", err)
		}
		if linkInfo.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symbolic links are not supported")
		}
		path, err = filepath.EvalSymlinks(name)
		if err != nil {
			return "", fmt.Errorf("resolve file: %w", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("inspect file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("path must refer to a regular file")
		}
		return path, nil
	}
	return e.existingFilePath(name)
}

func (e *Engine) newFilePath(name string) (string, error) {
	relative, err := e.relativePath(name)
	if err != nil {
		return "", err
	}
	target := filepath.Join(e.root, relative)
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return "", fmt.Errorf("resolve file directory: %w", err)
	}
	if !withinRoot(e.root, parent) {
		return "", errors.New("file path must stay inside the workspace")
	}
	return filepath.Join(parent, filepath.Base(target)), nil
}

func (e *Engine) existingFilePath(name string) (string, error) {
	relative, err := e.relativePath(name)
	if err != nil {
		return "", err
	}
	path := filepath.Join(e.root, relative)
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect file: %w", err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("symbolic links are not supported")
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve file: %w", err)
	}
	if !withinRoot(e.root, target) {
		return "", errors.New("file path must stay inside the workspace")
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", fmt.Errorf("inspect file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("path must refer to a regular file")
	}
	return target, nil
}

func (e *Engine) relativePath(name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", errors.New("file path must be relative to the workspace")
	}
	path := filepath.Clean(name)
	if path == "." || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", errors.New("file path must stay inside the workspace")
	}
	if !withinRoot(e.root, filepath.Join(e.root, path)) {
		return "", errors.New("file path must stay inside the workspace")
	}
	return path, nil
}

func withinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func parseFields(input string) ([]string, error) {
	var fields []string
	var current strings.Builder
	inQuotes := false
	started := false

	for _, char := range input {
		switch {
		case char == '"':
			inQuotes = !inQuotes
			started = true
		case !inQuotes && (char == ' ' || char == '\t'):
			if started {
				fields = append(fields, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(char)
			started = true
		}
	}
	if inQuotes {
		return nil, errors.New("unclosed double quote")
	}
	if started {
		fields = append(fields, current.String())
	}
	return fields, nil
}
