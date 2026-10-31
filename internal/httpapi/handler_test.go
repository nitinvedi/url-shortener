package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHealthzReturnsOK(t *testing.T) {
	handler := NewHandler(testLogger())
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != `{"status":"ok"}` {
		t.Errorf("body = %s", got)
	}
	if recorder.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID response header")
	}
}

func TestReadyzWithHealthyDependencies(t *testing.T) {
	handler := NewHandler(testLogger(), DependencyCheck{
		Name: "postgres",
		Check: func(context.Context) error {
			return nil
		},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"status":"ready"`) || !strings.Contains(body, `"postgres":"ok"`) {
		t.Errorf("unexpected response body: %s", body)
	}
}

func TestReadyzReturns503WhenDependencyFails(t *testing.T) {
	handler := NewHandler(testLogger(), DependencyCheck{
		Name: "postgres",
		Check: func(context.Context) error {
			return errors.New("database unavailable")
		},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `"status":"not_ready"`) || !strings.Contains(body, `"postgres":"degraded"`) {
		t.Errorf("unexpected response body: %s", body)
	}
}

func TestReadyzWithoutDependenciesIsReady(t *testing.T) {
	handler := NewHandler(testLogger())
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	handler := NewHandler(testLogger())
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}
