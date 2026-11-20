package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"

	"jarvis/internal/engine"
	"jarvis/internal/logx"
)

func main() {
	workspace := flag.String("workspace", ".", "directory where file commands are allowed")
	flag.Parse()

	assistant, err := engine.New(*workspace)
	if err != nil {
		logx.Error("initialize assistant: %v", err)
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
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
