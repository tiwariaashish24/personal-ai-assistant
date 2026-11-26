package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

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
	defer func() {
		if err := assistant.Close(); err != nil {
			logx.Error("close assistant: %v", err)
		}
	}()

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

		command := scanner.Text()
		result, err := assistant.Execute(command)
		if err == nil && result.Confirmation != "" {
			fmt.Printf("%s [y/N]: ", result.Confirmation)
			if !scanner.Scan() {
				break
			}
			if strings.EqualFold(strings.TrimSpace(scanner.Text()), "y") {
				result, err = assistant.ExecuteConfirmed(command)
			} else {
				assistant.CancelConfirmation(command)
				result.Output = "Cancelled. No changes were made."
			}
		}
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
	Command            string `json:"command"`
	ClearConversation  bool   `json:"clearConversation,omitempty"`
	Confirmed          bool   `json:"confirmed,omitempty"`
	CancelConfirmation bool   `json:"cancelConfirmation,omitempty"`
	Stream             bool   `json:"stream,omitempty"`
}

type stdioResponse struct {
	Stream       bool   `json:"stream,omitempty"`
	Delta        string `json:"delta,omitempty"`
	Output       string `json:"output,omitempty"`
	Exit         bool   `json:"exit,omitempty"`
	Error        string `json:"error,omitempty"`
	Confirmation string `json:"confirmation,omitempty"`
	Data         any    `json:"data,omitempty"`
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
		} else if request.CancelConfirmation {
			assistant.CancelConfirmation(request.Command)
			response.Output = "Cancelled. No changes were made."
		} else {
			var result engine.Result
			var err error
			if request.Confirmed {
				result, err = assistant.ExecuteConfirmed(request.Command)
			} else if request.Stream {
				result, err = assistant.ExecuteStreaming(request.Command, func(delta string) error {
					if err := json.NewEncoder(writer).Encode(stdioResponse{Stream: true, Delta: delta}); err != nil {
						return fmt.Errorf("encode streaming response: %w", err)
					}
					if err := writer.Flush(); err != nil {
						return fmt.Errorf("write streaming response: %w", err)
					}
					return nil
				})
			} else {
				result, err = assistant.Execute(request.Command)
			}
			if err != nil {
				logx.Error("command failed: %v", err)
				response.Error = err.Error()
			} else {
				response.Output = result.Output
				response.Exit = result.Exit
				response.Confirmation = result.Confirmation
				response.Data = result.Data
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
