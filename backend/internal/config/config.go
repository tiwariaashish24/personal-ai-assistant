package config

import (
	"bufio"
	"os"
	"strings"
)

type Config struct {
	// Addr is the HTTP listen address for the JARVIS API.
	Addr string
	// DBPath is the SQLite database file location.
	DBPath string
	// NodeServiceURL is the base URL of the Node.js speech service.
	NodeServiceURL string
	// EnableVoice enables the voice pipeline status endpoints.
	EnableVoice bool
}

func Load() *Config {
	loadDotEnv(".env")
	loadDotEnv("../.env")

	cfg := &Config{
		Addr:           envOr("JARVIS_API_ADDR", "127.0.0.1:8080"),
		DBPath:         envOr("JARVIS_DB_PATH", "jarvis.db"),
		NodeServiceURL: envOr("JARVIS_NODE_URL", "http://127.0.0.1:8787"),
		EnableVoice:    envOr("JARVIS_VOICE", "true") != "false",
	}
	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadDotEnv reads a simple KEY=VALUE .env file if present.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}
