package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func createFile(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: create <filename>")
	}
	path := args[0]
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("file already exists: %s", path)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	f.Close()
	Logf("created file: %s", path)
	fmt.Printf("Created %s\n", path)
	return nil
}

func writeFile(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: write <filename> <text>")
	}
	path := args[0]
	content := strings.Join(args[1:], " ") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return err
	}
	Logf("wrote file: %s", path)
	fmt.Printf("Wrote %d bytes to %s\n", len(content), path)
	return nil
}

func readFile(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: read <filename>")
	}
	path := args[0]
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	Logf("read file: %s", path)
	fmt.Printf("--- %s ---\n%s", path, data)
	return nil
}

func deleteFile(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: delete <filename>")
	}
	path := args[0]
	if err := os.Remove(path); err != nil {
		return err
	}
	Logf("deleted file: %s", path)
	fmt.Printf("Deleted %s\n", path)
	return nil
}

func listFiles(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		kind := "file"
		if e.IsDir() {
			kind = "dir "
		}
		info, err := e.Info()
		size := ""
		if err == nil {
			size = fmt.Sprintf("%10d", info.Size())
		}
		fmt.Printf("%s  %s  %s\n", kind, size, filepath.Join(dir, e.Name()))
	}
	return nil
}
