// Package config loads runtime configuration from environment variables.
package config

import (
	"os"
	"strconv"
)

type Config struct {
	HTTPAddr      string
	PublicBaseURL string
	PGDSN         string
	RedisAddr     string
	NATSURL       string
	TokenSecret   string
	StorageDriver string
	StorageDir    string
	Seed          bool
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// Load reads configuration with sensible local-dev defaults.
func Load() Config {
	return Config{
		HTTPAddr:      env("CAPTAIN_HTTP_ADDR", ":8080"),
		PublicBaseURL: env("CAPTAIN_PUBLIC_BASE_URL", "http://localhost:8080"),
		PGDSN:         env("CAPTAIN_PG_DSN", "postgres://captain:captain@localhost:5432/captain?sslmode=disable"),
		RedisAddr:     env("CAPTAIN_REDIS_ADDR", "localhost:6379"),
		NATSURL:       env("CAPTAIN_NATS_URL", "nats://localhost:4222"),
		TokenSecret:   env("CAPTAIN_TOKEN_SECRET", "dev-only-insecure-secret-change-me"),
		StorageDriver: env("CAPTAIN_STORAGE_DRIVER", "local"),
		StorageDir:    env("CAPTAIN_STORAGE_DIR", "/data"),
		Seed:          envBool("CAPTAIN_SEED", true),
	}
}
