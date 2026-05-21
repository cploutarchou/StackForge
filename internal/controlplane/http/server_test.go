package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	cpconfig "stackforge/internal/controlplane/config"
)

func TestAPIRequiresAuth(t *testing.T) {
	s := New(cpconfig.Config{AdminAPIKeys: []string{"secret"}, ReadyProtected: true}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/domains", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestHealthPublic(t *testing.T) {
	s := New(cpconfig.Config{AdminAPIKeys: []string{"secret"}}, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
}
