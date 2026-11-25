package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNaturalLanguageUsesToolsAndRemembersConversation(t *testing.T) {
	assistant, root := newTestEngine(t)
	var requests []chatCompletionRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var completion chatCompletionRequest
		if err := json.NewDecoder(request.Body).Decode(&completion); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		requests = append(requests, completion)
		writer.Header().Set("Content-Type", "application/json")
		switch len(requests) {
		case 1:
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"create-1","type":"function","function":{"name":"create_file","arguments":"{\"file\":\"note.txt\",\"text\":\"hello\"}"}}]}}]}`))
		case 2:
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Saved hello in note.txt."}}]}`))
		case 3:
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"You asked me to save hello."}}]}`))
		default:
			t.Errorf("unexpected completion request %d", len(requests))
			http.Error(writer, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	assistant.ai = &aiAssistant{
		client:   server.Client(),
		endpoint: server.URL + "/chat/completions",
		model:    "test-model",
	}

	result, err := assistant.Execute("Please save hello in note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "Saved hello in note.txt." {
		t.Fatalf("reply = %q", result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Fatalf("file content = %q", content)
	}
	if len(requests) != 2 {
		t.Fatalf("got %d provider requests, want 2", len(requests))
	}
	if len(requests[1].Messages) < 3 || requests[1].Messages[len(requests[1].Messages)-1].ToolCallID != "create-1" {
		t.Fatalf("tool result was not sent back to the provider: %#v", requests[1].Messages)
	}

	result, err = assistant.Execute("What did I ask you to save?")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "You asked me to save hello." {
		t.Fatalf("contextual reply = %q", result.Output)
	}
	if len(requests) != 3 || !strings.Contains(requests[2].Messages[1].Content, "Please save hello") {
		t.Fatalf("previous conversation was not included: %#v", requests[2].Messages)
	}

	assistant.ClearConversation()
	if len(assistant.ai.history) != 0 {
		t.Fatal("ClearConversation did not remove AI history")
	}
}

func TestAIPlansReactProjectAndOpensItInVSCode(t *testing.T) {
	assistant, root := newTestEngine(t)
	var opened []string
	assistant.launch = func(name string, args ...string) error {
		if name != "vscode" {
			t.Errorf("opened app = %q, want vscode", name)
		}
		opened = append(opened, args...)
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		writer.Header().Set("Content-Type", "application/json")
		if len(opened) == 0 {
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"project-1","type":"function","function":{"name":"create_react_project","arguments":"{\"project_name\":\"portfolio\"}"}},{"id":"open-1","type":"function","function":{"name":"open_app","arguments":"{\"app\":\"vscode\",\"directory\":\"portfolio\"}"}}]}}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Your React portfolio starter is ready and open in VS Code."}}]}`))
	}))
	defer server.Close()
	assistant.ai = &aiAssistant{client: server.Client(), endpoint: server.URL + "/chat/completions", model: "test-model"}

	result, err := assistant.Execute("Create a React portfolio project and open it in VS Code")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "Your React portfolio starter is ready and open in VS Code." {
		t.Fatalf("reply = %q", result.Output)
	}
	for _, path := range []string{
		filepath.Join("portfolio", "package.json"),
		filepath.Join("portfolio", "index.html"),
		filepath.Join("portfolio", "src", "App.jsx"),
		filepath.Join("portfolio", "src", "main.jsx"),
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("expected project file %q: %v", path, err)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(root, "portfolio", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var packageConfig struct {
		Name    string            `json:"name"`
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(manifest, &packageConfig); err != nil {
		t.Fatalf("invalid package.json: %v", err)
	}
	if packageConfig.Name != "portfolio" || packageConfig.Scripts["dev"] != "vite" || packageConfig.Scripts["build"] != "vite build" {
		t.Fatalf("unexpected React project manifest: %#v", packageConfig)
	}
	if len(opened) != 1 || opened[0] != filepath.Join(root, "portfolio") {
		t.Fatalf("VS Code arguments = %#v, want project directory", opened)
	}
}

func TestAIToolCannotEscapeWorkspace(t *testing.T) {
	assistant, root := newTestEngine(t)
	outside := filepath.Join(filepath.Dir(root), "outside-ai.txt")
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		requestCount++
		if requestCount == 1 {
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"create-1","type":"function","function":{"name":"create_file","arguments":"{\"file\":\"../outside-ai.txt\",\"text\":\"not allowed\"}"}}]}}]}`))
			return
		}
		var completion chatCompletionRequest
		if err := json.NewDecoder(request.Body).Decode(&completion); err != nil {
			t.Errorf("decode request: %v", err)
		} else if !strings.Contains(completion.Messages[len(completion.Messages)-1].Content, "file path must stay inside the workspace") {
			t.Errorf("tool error was not returned to the model: %#v", completion.Messages)
		}
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"I could not create that file outside the workspace."}}]}`))
	}))
	defer server.Close()
	assistant.ai = &aiAssistant{
		client:   server.Client(),
		endpoint: server.URL + "/chat/completions",
		model:    "test-model",
	}

	result, err := assistant.Execute("Create a file outside the workspace")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "I could not create that file outside the workspace." {
		t.Fatalf("reply = %q", result.Output)
	}
	if requestCount != 2 {
		t.Fatalf("provider requests = %d, want 2", requestCount)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside file should not have been created, stat error: %v", err)
	}
}

func TestDecodeToolArgumentsRejectsUnknownFields(t *testing.T) {
	var args struct {
		File string `json:"file"`
	}
	err := decodeToolArguments(json.RawMessage(`{"file":"notes.txt","command":"whoami"}`), &args)
	if err == nil {
		t.Fatal("expected unknown tool argument to be rejected")
	}
}
