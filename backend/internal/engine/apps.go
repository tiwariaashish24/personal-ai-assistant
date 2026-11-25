package engine

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func startApplication(name string, args ...string) error {
	var candidates []string
	switch name {
	case "chrome":
		if runtime.GOOS == "windows" {
			candidates = append(candidates, "chrome.exe",
				filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"))
		} else {
			candidates = []string{"google-chrome", "chromium", "chrome"}
		}
	case "vscode":
		if runtime.GOOS == "windows" {
			candidates = append(candidates, "Code.exe",
				filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Microsoft VS Code", "Code.exe"))
		} else {
			candidates = []string{"code"}
		}
	case "notepad":
		if runtime.GOOS == "windows" {
			candidates = []string{"notepad.exe"}
		} else {
			return errors.New("notepad is only available on Windows")
		}
	default:
		return errors.New("application is not allowed")
	}

	for _, candidate := range candidates {
		path, err := resolveExecutable(candidate)
		if err != nil {
			continue
		}
		cmd := exec.Command(path, args...)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start application: %w", err)
		}
		return cmd.Process.Release()
	}
	return fmt.Errorf("%s was not found; install it or add it to PATH", name)
}

func openURL(value string) error {
	var executable string
	var args []string
	switch runtime.GOOS {
	case "windows":
		executable = "rundll32.exe"
		args = []string{"url.dll,FileProtocolHandler", value}
	case "darwin":
		executable = "open"
		args = []string{value}
	default:
		executable = "xdg-open"
		args = []string{value}
	}
	path, err := exec.LookPath(executable)
	if err != nil {
		return fmt.Errorf("find browser opener %s: %w", executable, err)
	}
	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start browser opener: %w", err)
	}
	return cmd.Process.Release()
}

func resolveExecutable(candidate string) (string, error) {
	if filepath.IsAbs(candidate) {
		info, err := os.Stat(candidate)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", errors.New("executable path is a directory")
		}
		return candidate, nil
	}
	return exec.LookPath(candidate)
}
