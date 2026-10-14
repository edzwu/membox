package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testConfig() Config {
	return Config{
		AppEnv:            "development",
		HTTPAddr:          "127.0.0.1:8081",
		MaxUploadBytes:    defaultMaxUploadBytes,
		ReadinessCacheTTL: 50 * time.Millisecond,
	}
}

func TestHealthz(t *testing.T) {
	s := New(testConfig(), nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: got %d, want 200", rec.Code)
	}
}

type stubProbe struct {
	name  string
	err   error
	calls *atomic.Int32
}

func (p stubProbe) Name() string { return p.name }
func (p stubProbe) Check(context.Context) error {
	p.calls.Add(1)
	return p.err
}

func TestReadyzUnconfiguredDepsDoNotFail(t *testing.T) {
	// M0: nothing is configured yet, and unconfigured dependencies are
	// informational. Once M2 makes PostgreSQL mandatory, startup config
	// validation — not /readyz — owns that failure.
	s := New(testConfig(), nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz without deps: got %d, want 200; body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "unconfigured") {
		t.Fatalf("expected unconfigured dependencies in report, got %s", rec.Body)
	}
}

func TestReadyzHealthyProbe(t *testing.T) {
	calls := &atomic.Int32{}
	cfg := testConfig()
	cfg.DatabaseURL = "postgres://u:p@db.internal:5432/membox"
	s := New(cfg, []Probe{stubProbe{name: "postgres", calls: calls}})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz healthy: got %d, want 200; body %s", rec.Code, rec.Body)
	}
}

func TestReadyzFailingProbeIs503(t *testing.T) {
	calls := &atomic.Int32{}
	cfg := testConfig()
	cfg.R2AccountID, cfg.R2Bucket = "acct", "bucket"
	cfg.R2AccessKeyID, cfg.R2SecretAccessKey = "id", "secret"
	s := New(cfg, []Probe{stubProbe{name: "r2", err: errors.New("dial refused"), calls: calls}})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz failing: got %d, want 503", rec.Code)
	}
}

func TestReadyzCachesProbeResults(t *testing.T) {
	calls := &atomic.Int32{}
	cfg := testConfig()
	cfg.DatabaseURL = "postgres://u:p@db.internal:5432/membox"
	s := New(cfg, []Probe{stubProbe{name: "postgres", calls: calls}})
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("probe called %d times within TTL, want 1", got)
	}
	time.Sleep(cfg.ReadinessCacheTTL + 20*time.Millisecond)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if got := calls.Load(); got != 2 {
		t.Fatalf("probe called %d times after TTL, want 2", got)
	}
}

func TestTCPProbeURLParsing(t *testing.T) {
	probe, err := TCPProbe("postgres", "postgres://u:p@example.com/membox")
	if err != nil {
		t.Fatal(err)
	}
	if got := probe.(tcpProbe).address; got != "example.com:5432" {
		t.Fatalf("postgres default port: got %q", got)
	}
	probe, err = TCPProbe("r2", "https://acct.r2.cloudflarestorage.com")
	if err != nil {
		t.Fatal(err)
	}
	if got := probe.(tcpProbe).address; got != "acct.r2.cloudflarestorage.com:443" {
		t.Fatalf("https default port: got %q", got)
	}
}
