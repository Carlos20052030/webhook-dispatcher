package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	RedisAddr   string
	JWTSecret   []byte
	IngestToken []byte
	LogLevel    slog.Level
}

// Load reads env vars and fails fast on anything missing or malformed.
// Callers assume a returned Config is always usable.
func Load() (*Config, error) {
	level, err := parseLogLevel(getEnv("LOG_LEVEL", "info"))
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		HTTPAddr:    getEnv("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisAddr:   os.Getenv("REDIS_ADDR"),
		JWTSecret:   []byte(os.Getenv("JWT_SECRET")),
		IngestToken: []byte(os.Getenv("INGEST_TOKEN")),
		LogLevel:    level,
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate reports every missing var at once, not just the first.
func (c *Config) validate() error {
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if c.RedisAddr == "" {
		missing = append(missing, "REDIS_ADDR")
	}
	if len(c.JWTSecret) == 0 {
		missing = append(missing, "JWT_SECRET")
	}
	if len(c.IngestToken) == 0 {
		missing = append(missing, "INGEST_TOKEN")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseLogLevel(s string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return 0, fmt.Errorf("invalid LOG_LEVEL %q: %w", s, err)
	}
	return level, nil
}
