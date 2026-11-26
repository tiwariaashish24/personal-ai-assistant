package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jarvis/internal/config"
)

func TestDefaultOllamaModelIsSizedForLowMemoryDesktop(t *testing.T) {
	assistant := newAIAssistant(&config.Config{})
	if assistant.model != "qwen2.5:3b" {
		t.Fatalf("default model = %q, want qwen2.5:3b", assistant.model)
	}
}

func TestAIStreamingSendsIncrementalText(t *testing.T) {
	assistant, _ := newTestEngine(t)
	var streamEnabled bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var completion chatCompletionRequest
		if err := json.NewDecoder(request.Body).Decode(&completion); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(writer, "invalid request", http.StatusBadRequest)
			return
		}
		streamEnabled = completion.Stream
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello \"}}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"there.\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(writer, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()
	assistant.ai = &aiAssistant{client: server.Client(), endpoint: server.URL + "/chat/completions", model: "test-model"}

	var deltas []string
	result, err := assistant.ExecuteStreaming("say hello", func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !streamEnabled {
		t.Fatal("AI provider request did not enable streaming")
	}
	if result.Output != "Hello there." {
		t.Fatalf("reply = %q", result.Output)
	}
	if fmt.Sprint(deltas) != "[Hello  there.]" {
		t.Fatalf("streamed deltas = %#v", deltas)
	}
}

func TestAIStreamingReassemblesToolCallsBeforeFinalReply(t *testing.T) {
	assistant, _ := newTestEngine(t)
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		requestCount++
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		if requestCount == 1 {
			_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\"}}]}}]}\n\n")
			_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"hello\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		} else {
			_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Found it.\"},\"finish_reason\":\"stop\"}]}\n\n")
		}
		_, _ = fmt.Fprint(writer, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()
	assistant.ai = &aiAssistant{client: server.Client(), endpoint: server.URL + "/chat/completions", model: "test-model"}

	var deltas []string
	reply, err := assistant.ai.respondStreaming("look something up", func(name string, arguments json.RawMessage) (string, error) {
		if name != "lookup" || string(arguments) != `{"q":"hello"}` {
			t.Errorf("tool call = %q %s", name, arguments)
		}
		return "lookup result", nil
	}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply != "Found it." || requestCount != 2 {
		t.Fatalf("reply = %q, provider requests = %d", reply, requestCount)
	}
	if fmt.Sprint(deltas) != "[Found it.]" {
		t.Fatalf("streamed deltas included tool-call content: %#v", deltas)
	}
}

func TestAIStreamingRejectsTruncatedProviderResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer server.Close()
	assistant := &aiAssistant{client: server.Client(), endpoint: server.URL + "/chat/completions", model: "test-model"}
	_, err := assistant.completeStreaming(nil, func(string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "ended before completion") {
		t.Fatalf("truncated stream error = %v", err)
	}
}

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
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Your React portfolio starter is being created in the background and opened in VS Code."}}]}`))
	}))
	defer server.Close()
	assistant.ai = &aiAssistant{client: server.Client(), endpoint: server.URL + "/chat/completions", model: "test-model"}

	result, err := assistant.Execute("Create a React portfolio project and open it in VS Code")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "Your React portfolio starter is being created in the background and opened in VS Code." {
		t.Fatalf("reply = %q", result.Output)
	}
	taskDeadline := time.Now().Add(time.Second)
	for time.Now().Before(taskDeadline) {
		tasks := assistant.tasks.List()
		if len(tasks) > 0 && tasks[0].Status == TaskCompleted {
			break
		}
		if len(tasks) > 0 && (tasks[0].Status == TaskFailed || tasks[0].Status == TaskCancelled || tasks[0].Status == TaskTimedOut) {
			t.Fatalf("React scaffold task ended in %s: %s", tasks[0].Status, tasks[0].Error)
		}
		time.Sleep(time.Millisecond)
	}
	if tasks := assistant.tasks.List(); len(tasks) == 0 || tasks[0].Status != TaskCompleted {
		t.Fatalf("React scaffold task did not complete: %#v", tasks)
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
