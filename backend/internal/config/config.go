package config

import (
	"bufio"
	"os"
	"path/filepath"
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
	// AIProvider selects ollama or openai for natural-language requests.
	AIProvider string
	// AIBaseURL overrides the selected provider's OpenAI-compatible API URL.
	AIBaseURL string
	// AIModel overrides the provider's default model.
	AIModel string
	// OpenAIKey is the API key used only by the OpenAI provider.
	OpenAIKey string
	// GoogleClientID and GoogleClientSecret configure the local Google OAuth app.
	GoogleClientID     string
	GoogleClientSecret string
}

func Load() *Config {
	loadDotEnv(".env")
	loadDotEnv("../.env")

	cfg := &Config{
		Addr:               envOr("JARVIS_API_ADDR", "127.0.0.1:8080"),
		DBPath:             envOr("JARVIS_DB_PATH", defaultDBPath()),
		NodeServiceURL:     envOr("JARVIS_NODE_URL", "http://127.0.0.1:8787"),
		EnableVoice:        envOr("JARVIS_VOICE", "true") != "false",
		AIProvider:         envOr("JARVIS_AI_PROVIDER", "ollama"),
		AIBaseURL:          os.Getenv("JARVIS_AI_BASE_URL"),
		AIModel:            os.Getenv("JARVIS_AI_MODEL"),
		OpenAIKey:          os.Getenv("OPENAI_API_KEY"),
		GoogleClientID:     os.Getenv("JARVIS_GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("JARVIS_GOOGLE_CLIENT_SECRET"),
	}
	return cfg
}

func defaultDBPath() string {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "jarvis.db"
	}
	return filepath.Join(directory, "JARVIS", "jarvis.db")
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
