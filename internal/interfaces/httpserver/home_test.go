package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHomeServesEmbeddedIndex(t *testing.T) {
	s := New(testConfig(), nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	// Built assets present → SPA HTML; fresh clone without `make remote-web`
	// → clear 503 instructions. Both are acceptable, anything else is a bug.
	if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("home: got %d", rec.Code)
	}
}

func TestListDocumentsRequiresAuth(t *testing.T) {
	// M4: the list endpoint is real now; without a session it must be 401
	// (never a document leak, plan T10/T18).
	s := New(testConfig(), nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/documents?q=paper", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("documents without session: got %d, want 401", rec.Code)
	}
}
