package httpserver

import (
	"testing"
)

func env(kv ...string) func(string) string {
	return func(key string) string {
		for i := 0; i+1 < len(kv); i += 2 {
			if kv[i] == key {
				return kv[i+1]
			}
		}
		return ""
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8081" {
		t.Fatalf("default addr: got %q", cfg.HTTPAddr)
	}
	if cfg.AppEnv != "development" {
		t.Fatalf("default env: got %q", cfg.AppEnv)
	}
	if cfg.MaxUploadBytes != defaultMaxUploadBytes {
		t.Fatalf("default max upload: got %d", cfg.MaxUploadBytes)
	}
	if cfg.R2Configured() {
		t.Fatal("R2 should be unconfigured by default")
	}
}

func TestLoadRejectsBadEnv(t *testing.T) {
	if _, err := Load(env("APP_ENV", "staging")); err == nil {
		t.Fatal("want APP_ENV error")
	}
	if _, err := Load(env("HTTP_ADDR", "not-an-addr")); err == nil {
		t.Fatal("want HTTP_ADDR error")
	}
	if _, err := Load(env("MAX_UPLOAD_BYTES", "-1")); err == nil {
		t.Fatal("want MAX_UPLOAD_BYTES error")
	}
}

func TestLoadRejectsPartialR2(t *testing.T) {
	_, err := Load(env("R2_ACCOUNT_ID", "acct", "R2_BUCKET", "b"))
	if err == nil {
		t.Fatal("want all-or-nothing R2 error")
	}
	cfg, err := Load(env(
		"R2_ACCOUNT_ID", "acct", "R2_BUCKET", "b",
		"R2_ACCESS_KEY_ID", "id", "R2_SECRET_ACCESS_KEY", "secret",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.R2Configured() {
		t.Fatal("R2 should be configured")
	}
	if cfg.R2Endpoint() != "https://acct.r2.cloudflarestorage.com" {
		t.Fatalf("endpoint: got %q", cfg.R2Endpoint())
	}
}
