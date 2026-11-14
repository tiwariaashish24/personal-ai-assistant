package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// AppAliases maps friendly names to Windows commands.
var AppAliases = map[string]string{
	"chrome":   "chrome",
	"code":     "code",
	"notepad":  "notepad",
	"edge":     "msedge",
	"explorer": "explorer",
	"calc":     "calc",
	"paint":    "mspaint",
	"wordpad":  "wordpad",
	"cmd":      "cmd",
	"terminal": "wt",
	"vscode":   "code",
}

// HandleCommand parses and dispatches a single input line.
func HandleCommand(input string) error {
	parts := strings.Fields(input)
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	switch cmd {
	case "help":
		PrintHelp()
	case "exit", "quit":
		fmt.Println("Goodbye!")
		os.Exit(0)
	case "open":
		return openApp(args)
	case "create":
		return createFile(args)
	case "write":
		return writeFile(args)
	case "read":
		return readFile(args)
	case "delete", "del":
		return deleteFile(args)
	case "list", "ls":
		return listFiles(args)
	case "system", "info", "sysinfo":
		return systemInfo()
	case "battery":
		return batteryInfo()
	case "cpu":
		return cpuInfo()
	case "ram", "memory":
		return ramInfo()
	default:
		return fmt.Errorf("unknown command %q - type 'help'", cmd)
	}
	return nil
}

func PrintHelp() {
	fmt.Println(`Commands:
  open <app>      launch an app (chrome, code, notepad, edge, calc, paint, explorer...)
  create <file>   create an empty file
  write <file> <text>   write text to a file (replaces contents)
  read <file>     print file contents
  delete <file>   delete a file
  list [dir]      list files in a directory (default: current)
  system          show OS / CPU / RAM summary
  battery         show battery status
  cpu             CPU usage
  ram             RAM usage
  help            show this help
  exit            quit`)
}

// openApp launches an application by friendly name.
func openApp(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: open <app>")
	}
	name := strings.ToLower(args[0])
	target, ok := AppAliases[name]
	if !ok {
		// Allow opening arbitrary commands too.
		target = name
	}
	Logf("opening app: %s", target)
	return exec.Command("cmd", "/c", "start", "", target).Start()
}
