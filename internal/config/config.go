package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr                  string
	DataDir               string
	ConfigDir             string
	DownloadDir           string
	DBPath                string
	Secret                string
	SourceRepo            string
	ScanInterval          time.Duration
	MaxChapterConcurrency int
	MaxPageConcurrency    int
	CookieSecure          bool
}

func Load() (Config, error) {
	cfg := Config{
		Addr:                  env("MANCO_ADDR", ":15600"),
		DataDir:               env("MANCO_DATA_DIR", "data"),
		ConfigDir:             env("MANCO_CONFIG_DIR", "config"),
		DownloadDir:           env("MANCO_DOWNLOAD_DIR", "downloads"),
		SourceRepo:            env("MANCO_SOURCE_REPO", ""),
		ScanInterval:          durationEnv("MANCO_SCAN_INTERVAL", 30*time.Minute),
		MaxChapterConcurrency: intEnv("MANCO_MAX_CHAPTER_CONCURRENCY", 2),
		MaxPageConcurrency:    intEnv("MANCO_MAX_PAGE_CONCURRENCY", 4),
		CookieSecure:          boolEnv("MANCO_COOKIE_SECURE", false),
	}
	if cfg.MaxChapterConcurrency < 1 {
		cfg.MaxChapterConcurrency = 1
	}
	if cfg.MaxPageConcurrency < 1 {
		cfg.MaxPageConcurrency = 1
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create data directory: %w", err)
	}
	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create config directory: %w", err)
	}
	if err := os.MkdirAll(cfg.DownloadDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create download directory: %w", err)
	}
	cfg.DBPath = filepath.Join(cfg.DataDir, "manco.db")
	envSecret := strings.TrimSpace(os.Getenv("MANCO_SECRET"))
	if envSecret != "" {
		cfg.Secret = envSecret
	} else {
		secretPath := filepath.Join(cfg.DataDir, ".secret")
		if _, err := os.Stat(secretPath); errors.Is(err, os.ErrNotExist) {
			secretPath = filepath.Join(cfg.ConfigDir, "secret.key")
		}
		secret, err := loadOrCreateSecret(secretPath)
		if err != nil {
			return Config{}, err
		}
		cfg.Secret = secret
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func boolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func loadOrCreateSecret(path string) (string, error) {
	if raw, err := os.ReadFile(path); err == nil {
		value := strings.TrimSpace(string(raw))
		if value != "" {
			return value, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate application secret: %w", err)
	}
	value := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("persist application secret: %w", err)
	}
	return value, nil
}
