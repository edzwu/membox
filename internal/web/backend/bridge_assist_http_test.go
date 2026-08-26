package backend

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBridgeAssistRequiresToken(t *testing.T) {
	server := &Server{token: "secret"}
	request := httptest.NewRequest(http.MethodPost, "/api/bridge/assist", strings.NewReader(`{
		"mode":"edit",
		"instruction":"format this",
		"selection":"job text"
	}`))
	response := httptest.NewRecorder()

	server.handleBridgeAssist(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestBridgeAssistValidatesPayloadBeforeRunningModel(t *testing.T) {
	server := &Server{token: "secret"}
	request := httptest.NewRequest(http.MethodPost, "/api/bridge/assist", strings.NewReader(`{
		"mode":"edit",
		"instruction":"format this",
		"selection":""
	}`))
	request.Header.Set(bridgeTokenHeader, "secret")
	response := httptest.NewRecorder()

	server.handleBridgeAssist(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if !strings.Contains(response.Body.String(), "selection is required") {
		t.Fatalf("body = %q, want validation error", response.Body.String())
	}
}

func TestBridgeAssistStatusRequiresToken(t *testing.T) {
	server := &Server{token: "secret"}
	request := httptest.NewRequest(http.MethodGet, "/api/bridge/assist/status", nil)
	response := httptest.NewRecorder()

	server.handleBridgeAssistStatus(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestBridgeAssistStatusReportsRunner(t *testing.T) {
	server := &Server{token: "secret"}
	request := httptest.NewRequest(http.MethodGet, "/api/bridge/assist/status", nil)
	request.Header.Set(bridgeTokenHeader, "secret")
	response := httptest.NewRecorder()

	server.handleBridgeAssistStatus(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	for _, field := range []string{`"available"`, `"provider":"deepseek"`, `"model":"deepseek-v4-flash"`} {
		if !strings.Contains(response.Body.String(), field) {
			t.Fatalf("body = %q, want field %s", response.Body.String(), field)
		}
	}
}

func TestBridgeAssistRejectsNonPost(t *testing.T) {
	server := &Server{token: "secret"}
	request := httptest.NewRequest(http.MethodGet, "/api/bridge/assist", nil)
	response := httptest.NewRecorder()

	server.handleBridgeAssist(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}
