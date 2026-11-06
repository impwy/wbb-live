// Package config loads WBB API server configuration from environment variables.
// Secrets (JWT key, database credentials) are only accepted from the environment
// and are never hard-coded. In dev an ephemeral signing key may be generated,
// which means access/refresh state does not survive restarts.
package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env         string // "dev" or "prod"
	Addr        string
	DatabaseURL string
	RedisAddr   string
	JWTSecret   []byte

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	CORSOrigins []string
	StaticDir   string

	RateLimitRequests int64
	RateLimitWindow   time.Duration

	ephemeralSecret bool
}

// Load reads configuration from WBB_* environment variables.
func Load() (Config, error) {
	cfg := Config{
		Env:               envOr("WBB_ENV", "dev"),
		Addr:              envOr("WBB_ADDR", ":8080"),
		DatabaseURL:       os.Getenv("WBB_DATABASE_URL"),
		RedisAddr:         envOr("WBB_REDIS_ADDR", "127.0.0.1:6379"),
		AccessTokenTTL:    10 * time.Minute,
		RefreshTokenTTL:   7 * 24 * time.Hour,
		RateLimitRequests: 30,
		RateLimitWindow:   time.Minute,
		StaticDir:         os.Getenv("WBB_STATIC_DIR"),
	}
	if cfg.DatabaseURL == "" {
		return cfg, errors.New("WBB_DATABASE_URL is required")
	}
	if cfg.Env != "dev" && cfg.Env != "prod" {
		return cfg, fmt.Errorf("WBB_ENV must be \"dev\" or \"prod\", got %q", cfg.Env)
	}

	var err error
	if cfg.AccessTokenTTL, err = durationOr("WBB_ACCESS_TOKEN_TTL", cfg.AccessTokenTTL); err != nil {
		return cfg, err
	}
	if cfg.RefreshTokenTTL, err = durationOr("WBB_REFRESH_TOKEN_TTL", cfg.RefreshTokenTTL); err != nil {
		return cfg, err
	}
	if cfg.RefreshTokenTTL <= cfg.AccessTokenTTL {
		return cfg, errors.New("WBB_REFRESH_TOKEN_TTL must be greater than WBB_ACCESS_TOKEN_TTL")
	}

	if raw := os.Getenv("WBB_CORS_ORIGINS"); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if o = strings.TrimSpace(o); o != "" {
				cfg.CORSOrigins = append(cfg.CORSOrigins, o)
			}
		}
	}
	if raw := os.Getenv("WBB_RATE_LIMIT_REQUESTS"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			return cfg, errors.New("WBB_RATE_LIMIT_REQUESTS must be a positive integer")
		}
		cfg.RateLimitRequests = n
	}
	if cfg.RateLimitWindow, err = durationOr("WBB_RATE_LIMIT_WINDOW", cfg.RateLimitWindow); err != nil {
		return cfg, err
	}

	secret := os.Getenv("WBB_JWT_SECRET")
	switch {
	case secret != "":
		if len(secret) < 32 {
			return cfg, errors.New("WBB_JWT_SECRET must be at least 32 bytes")
		}
		cfg.JWTSecret = []byte(secret)
	case cfg.Env == "dev":
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return cfg, fmt.Errorf("generating ephemeral JWT secret: %w", err)
		}
		cfg.JWTSecret = buf
		cfg.ephemeralSecret = true
	default:
		return cfg, errors.New("WBB_JWT_SECRET is required when WBB_ENV=prod")
	}
	return cfg, nil
}

// EphemeralSecret reports whether the JWT secret was randomly generated for
// this process only (dev convenience; tokens do not survive restarts).
func (c Config) EphemeralSecret() bool { return c.ephemeralSecret }

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func durationOr(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback, fmt.Errorf("%s: %w", key, err)
	}
	if d <= 0 {
		return fallback, fmt.Errorf("%s must be positive", key)
	}
	return d, nil
}
