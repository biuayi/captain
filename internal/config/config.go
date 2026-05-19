// Package config loads runtime configuration from environment variables.
package config

import (
	"os"
	"strconv"
)

type Config struct {
	HTTPAddr          string
	PublicBaseURL     string
	PGDSN             string
	RedisAddr         string
	NATSURL           string
	TokenSecret       string
	IdentityPepper    string
	AdminPath         string // 管理后台路径混淆（T-083），默认 "admin"
	SeedAdminPw       string
	SeedOrgPw         string
	TurnstileMode     string // off | enforce（REQ-CHANGE-003）
	TurnstileSite     string
	TurnstileSecret   string
	StorageDriver     string
	StorageDir        string
	OSSEndpoint       string
	OSSBucket         string
	OSSKeyID          string
	OSSKeySecret      string
	ConfigKey         string // CAPTAIN_CONFIG_KEY: 32B AES-256-GCM key for platform_config secrets (prod required)
	Env               string // CAPTAIN_ENV: dev | staging | prod (prod refuses weak default secrets, S4)
	LoginFailClosed   bool   // CAPTAIN_LOGIN_FAILCLOSED: deny login when Redis down (S3, default false)
	OpenParticipation bool   // CAPTAIN_OPEN_PARTICIPATION: legacy anon/external path, default off (DESIGN §SS-2)
	Seed              bool
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
		HTTPAddr:          env("CAPTAIN_HTTP_ADDR", ":8080"),
		PublicBaseURL:     env("CAPTAIN_PUBLIC_BASE_URL", "http://localhost:8080"),
		PGDSN:             env("CAPTAIN_PG_DSN", "postgres://captain:captain@localhost:5432/captain?sslmode=disable"),
		RedisAddr:         env("CAPTAIN_REDIS_ADDR", "localhost:6379"),
		NATSURL:           env("CAPTAIN_NATS_URL", "nats://localhost:4222"),
		TokenSecret:       env("CAPTAIN_TOKEN_SECRET", "dev-only-insecure-secret-change-me"),
		IdentityPepper:    env("CAPTAIN_IDENTITY_PEPPER", "dev-only-insecure-pepper-change-me"),
		AdminPath:         env("CAPTAIN_ADMIN_PATH", "admin"),
		SeedAdminPw:       env("CAPTAIN_SEED_ADMIN_PW", "admin123"),
		SeedOrgPw:         env("CAPTAIN_SEED_ORG_PW", "xundao123"),
		TurnstileMode:     env("CAPTAIN_TURNSTILE_MODE", "off"),
		TurnstileSite:     env("CAPTAIN_TURNSTILE_SITEKEY", ""),
		TurnstileSecret:   env("CAPTAIN_TURNSTILE_SECRET", ""),
		StorageDriver:     env("CAPTAIN_STORAGE_DRIVER", "local"),
		StorageDir:        env("CAPTAIN_STORAGE_DIR", "/data"),
		OSSEndpoint:       env("CAPTAIN_OSS_ENDPOINT", ""),
		OSSBucket:         env("CAPTAIN_OSS_BUCKET", ""),
		OSSKeyID:          env("CAPTAIN_OSS_KEY_ID", ""),
		OSSKeySecret:      env("CAPTAIN_OSS_KEY_SECRET", ""),
		ConfigKey:         env("CAPTAIN_CONFIG_KEY", ""),
		Env:               env("CAPTAIN_ENV", "dev"),
		LoginFailClosed:   envBool("CAPTAIN_LOGIN_FAILCLOSED", false),
		OpenParticipation: envBool("CAPTAIN_OPEN_PARTICIPATION", false),
		Seed:              envBool("CAPTAIN_SEED", true),
	}
}
