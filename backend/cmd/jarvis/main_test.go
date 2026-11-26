package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"jarvis/internal/engine"
)

func TestStdioStreamsAITextBeforeFinalResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello.\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	workspace := t.TempDir()
	t.Setenv("JARVIS_AI_PROVIDER", "ollama")
	t.Setenv("JARVIS_AI_BASE_URL", server.URL)
	t.Setenv("JARVIS_AI_MODEL", "test-model")
	t.Setenv("JARVIS_DB_PATH", filepath.Join(workspace, "jarvis.db"))
	assistant, err := engine.New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer assistant.Close()

	inputReader, inputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outputReader, outputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previousStdin, previousStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inputReader, outputWriter
	_, writeErr := fmt.Fprintln(inputWriter, `{"command":"say hello","stream":true}`)
	closeInputErr := inputWriter.Close()
	runErr := runStdio(assistant)
	os.Stdin, os.Stdout = previousStdin, previousStdout
	_ = inputReader.Close()
	_ = outputWriter.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeInputErr != nil {
		t.Fatal(closeInputErr)
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	output, err := io.ReadAll(outputReader)
	_ = outputReader.Close()
	if err != nil {
		t.Fatal(err)
	}

	var responses []stdioResponse
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		var response stdioResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatalf("decode stdio response: %v", err)
		}
		responses = append(responses, response)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(responses) != 2 || !responses[0].Stream || responses[0].Delta != "Hello." ||
		responses[1].Stream || responses[1].Output != "Hello." {
		t.Fatalf("stdio streaming responses = %#v", responses)
	}
}
