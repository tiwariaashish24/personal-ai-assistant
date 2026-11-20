package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const helpText = `Commands:
  open <chrome|vscode|notepad>  Open an installed application
  create <file> <text>          Create a new file
  read <file>                   Read a file
  append <file> <text>          Append text to a file
  delete <file>                 Delete a file
  system                        Show CPU, memory, and battery information
  help                          Show this help
  exit                          Quit the assistant

Quote paths or text containing spaces with double quotes. File paths are relative
to the workspace and cannot access files outside it.`

var ErrUnknownCommand = errors.New("unknown command")

type Result struct {
	Output string
	Exit   bool
}

type Engine struct {
	root   string
	launch func(string) error
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

	return &Engine{root: root, launch: startApplication}, nil
}

func (e *Engine) Execute(input string) (Result, error) {
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
			return Result{}, errors.New("usage: open <chrome|vscode|notepad>")
		}
		app := strings.ToLower(fields[1])
		if app != "chrome" && app != "vscode" && app != "notepad" {
			return Result{}, errors.New("app must be chrome, vscode, or notepad")
		}
		if err := e.launch(app); err != nil {
			return Result{}, fmt.Errorf("open %s: %w", app, err)
		}
		return Result{Output: "Opening " + app + "."}, nil
	case "create":
		if len(fields) < 3 {
			return Result{}, errors.New("usage: create <file> <text>")
		}
		return e.create(fields[1], strings.Join(fields[2:], " "))
	case "read":
		if len(fields) != 2 {
			return Result{}, errors.New("usage: read <file>")
		}
		return e.read(fields[1])
	case "append":
		if len(fields) < 3 {
			return Result{}, errors.New("usage: append <file> <text>")
		}
		return e.append(fields[1], strings.Join(fields[2:], " "))
	case "delete":
		if len(fields) != 2 {
			return Result{}, errors.New("usage: delete <file>")
		}
		return e.delete(fields[1])
	case "system":
		if len(fields) != 1 {
			return Result{}, errors.New("usage: system")
		}
		return Result{Output: systemSummary(platformStats())}, nil
	default:
		return Result{}, fmt.Errorf("%w: %s (type help for commands)", ErrUnknownCommand, fields[0])
	}
}

func (e *Engine) create(name, content string) (Result, error) {
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
	path, err := e.existingFilePath(name)
	if err != nil {
		return Result{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read file: %w", err)
	}
	return Result{Output: string(content)}, nil
}

func (e *Engine) append(name, content string) (Result, error) {
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

func (e *Engine) delete(name string) (Result, error) {
	path, err := e.existingFilePath(name)
	if err != nil {
		return Result{}, err
	}
	if err := os.Remove(path); err != nil {
		return Result{}, fmt.Errorf("delete file: %w", err)
	}
	return Result{Output: "Deleted " + name + "."}, nil
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
