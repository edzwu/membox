// Package httpserver implements the membox remote node HTTP API.
//
// Per ADR-003/ADR-006 (docs/remote-stage1.md): this package belongs to the
// remote membox-server process. It reuses the local companion's thin-handler
// pattern (stdlib mux, handlers only parse/authorize/delegate) but shares no
// code with internal/web/backend, and nothing under cmd/mm, cmd/mmd or
// internal/web may depend on it.
package httpserver

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config carries every membox-server setting. Values come from the process
// environment only (ADR-004): no config files are read outside the optional
// .env loaded by main before Load.
type Config struct {
	// AppEnv is "development" or "production". Production refuses loopback
	// defaults that are fine for local development.
	AppEnv string

	// HTTPAddr is the listen address. Development default 127.0.0.1:8081.
	HTTPAddr string

	// DatabaseURL is the PostgreSQL DSN. Empty in M0/M1 (readiness probe
	// reports it as unconfigured); required once M2 lands.
	DatabaseURL string

	// R2 object storage (S3-compatible). All-or-nothing: either all four are
	// set or the dependency counts as unconfigured.
	R2AccountID       string
	R2Bucket          string
	R2AccessKeyID     string
	R2SecretAccessKey string

	// PublicBaseURL is the externally reachable base URL (behind tunnel or
	// reverse proxy), used when building absolute links.
	PublicBaseURL string

	// SMTP delivers OTP sign-in codes. Empty host in development falls back
	// to a log mailer; production refuses to start without it.
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	// OTPHMACSecret keys the one-time-code HMAC (T11). Required whenever the
	// database is configured.
	OTPHMACSecret string

	// MaxUploadBytes bounds document upload bodies (T15). Default 200 MiB.
	MaxUploadBytes int64

	// ReadinessCacheTTL bounds how often /readyz re-probes dependencies so
	// frequent polling stays cheap (plan T02: "避免每次探测过重").
	ReadinessCacheTTL time.Duration
}

const defaultMaxUploadBytes = 200 << 20

// Load reads Config from the environment, applying development defaults.
// Missing values are not errors at this layer: readiness reports unconfigured
// dependencies, and later milestones tighten requirements as their features
// land (e.g. DATABASE_URL becomes mandatory with M2).
func Load(getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	cfg := Config{
		AppEnv:            strings.TrimSpace(getenv("APP_ENV")),
		HTTPAddr:          strings.TrimSpace(getenv("HTTP_ADDR")),
		DatabaseURL:       strings.TrimSpace(getenv("DATABASE_URL")),
		R2AccountID:       strings.TrimSpace(getenv("R2_ACCOUNT_ID")),
		R2Bucket:          strings.TrimSpace(getenv("R2_BUCKET")),
		R2AccessKeyID:     strings.TrimSpace(getenv("R2_ACCESS_KEY_ID")),
		R2SecretAccessKey: strings.TrimSpace(getenv("R2_SECRET_ACCESS_KEY")),
		PublicBaseURL:     strings.TrimSpace(getenv("PUBLIC_BASE_URL")),
		SMTPHost:          strings.TrimSpace(getenv("SMTP_HOST")),
		SMTPPort:          strings.TrimSpace(getenv("SMTP_PORT")),
		SMTPUsername:      strings.TrimSpace(getenv("SMTP_USERNAME")),
		SMTPPassword:      getenv("SMTP_PASSWORD"),
		SMTPFrom:          strings.TrimSpace(getenv("SMTP_FROM")),
		OTPHMACSecret:     getenv("OTP_HMAC_SECRET"),
		ReadinessCacheTTL: 5 * time.Second,
	}
	if cfg.AppEnv == "" {
		cfg.AppEnv = "development"
	}
	switch cfg.AppEnv {
	case "development", "production":
	default:
		return Config{}, fmt.Errorf("APP_ENV must be development or production, got %q", cfg.AppEnv)
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = "127.0.0.1:8081"
	}
	if _, _, err := net.SplitHostPort(cfg.HTTPAddr); err != nil {
		return Config{}, fmt.Errorf("HTTP_ADDR %q: %w", cfg.HTTPAddr, err)
	}
	if raw := strings.TrimSpace(getenv("MAX_UPLOAD_BYTES")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return Config{}, fmt.Errorf("MAX_UPLOAD_BYTES must be a positive integer, got %q", raw)
		}
		cfg.MaxUploadBytes = value
	} else {
		cfg.MaxUploadBytes = defaultMaxUploadBytes
	}
	if cfg.SMTPPort == "" {
		cfg.SMTPPort = "25"
	}
	if cfg.AppEnv == "production" {
		if cfg.DatabaseURL == "" {
			return Config{}, fmt.Errorf("DATABASE_URL is required in production")
		}
		if !cfg.R2Configured() {
			return Config{}, fmt.Errorf("R2 credentials are required in production")
		}
		if cfg.SMTPHost == "" {
			return Config{}, fmt.Errorf("SMTP_HOST is required in production")
		}
		if cfg.OTPHMACSecret == "" {
			return Config{}, fmt.Errorf("OTP_HMAC_SECRET is required in production")
		}
	}
	if partial := cfg.r2PartiallyConfigured(); partial {
		return Config{}, fmt.Errorf("R2 config is all-or-nothing: set R2_ACCOUNT_ID, R2_BUCKET, R2_ACCESS_KEY_ID and R2_SECRET_ACCESS_KEY together")
	}
	return cfg, nil
}

// R2Configured reports whether the full R2 credential set is present.
func (c Config) R2Configured() bool {
	return c.R2AccountID != "" && c.R2Bucket != "" &&
		c.R2AccessKeyID != "" && c.R2SecretAccessKey != ""
}

func (c Config) r2PartiallyConfigured() bool {
	set := 0
	for _, v := range []string{c.R2AccountID, c.R2Bucket, c.R2AccessKeyID, c.R2SecretAccessKey} {
		if v != "" {
			set++
		}
	}
	return set > 0 && set < 4
}

// SecureCookies reports whether session cookies get the Secure attribute.
func (c Config) SecureCookies() bool {
	return c.AppEnv == "production" || strings.HasPrefix(c.PublicBaseURL, "https://")
}

// R2Endpoint is the S3-compatible endpoint host for readiness probing.
// The real client (T05) builds the full URL from the account ID.
func (c Config) R2Endpoint() string {
	return fmt.Sprintf("https://%s.r2.cloudflarestorage.com", c.R2AccountID)
}
