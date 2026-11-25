package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"jarvis/internal/engine"
	"jarvis/internal/logx"
)

func main() {
	workspace := flag.String("workspace", ".", "directory where file commands are allowed")
	stdio := flag.Bool("stdio", false, "read and write JSON-lines commands on stdin and stdout")
	flag.Parse()

	assistant, err := engine.New(*workspace)
	if err != nil {
		logx.Error("initialize assistant: %v", err)
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	if *stdio {
		if err := runStdio(assistant); err != nil {
			logx.Error("stdio command loop failed: %v", err)
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		return
	}

	fmt.Println("JARVIS assistant. Type help for commands, or exit to quit.")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		result, err := assistant.Execute(scanner.Text())
		if err != nil {
			logx.Error("command failed: %v", err)
			fmt.Fprintln(os.Stderr, "Error:", err)
			continue
		}
		if result.Output != "" {
			fmt.Println(result.Output)
		}
		if result.Exit {
			return
		}
	}

	if err := scanner.Err(); err != nil {
		logx.Error("read command input: %v", err)
		fmt.Fprintln(os.Stderr, "Error reading input:", err)
		os.Exit(1)
	}
}

type stdioRequest struct {
	Command           string `json:"command"`
	ClearConversation bool   `json:"clearConversation,omitempty"`
}

type stdioResponse struct {
	Output string `json:"output,omitempty"`
	Exit   bool   `json:"exit,omitempty"`
	Error  string `json:"error,omitempty"`
}

func runStdio(assistant *engine.Engine) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	writer := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		var request stdioRequest
		response := stdioResponse{}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			response.Error = fmt.Sprintf("invalid request: %v", err)
		} else if request.ClearConversation {
			assistant.ClearConversation()
		} else {
			result, err := assistant.Execute(request.Command)
			if err != nil {
				logx.Error("command failed: %v", err)
				response.Error = err.Error()
			} else {
				response.Output = result.Output
				response.Exit = result.Exit
			}
		}
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			return fmt.Errorf("encode response: %w", err)
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	return nil
}
