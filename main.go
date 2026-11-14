package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func main() {
	InitLogger()
	fmt.Println("Assistant ready. Type 'help' for commands, 'exit' to quit.")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("assistant> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if err := HandleCommand(input); err != nil {
			fmt.Printf("error: %v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		Logf("scanner error: %v", err)
	}
	fmt.Println("Bye.")
}
