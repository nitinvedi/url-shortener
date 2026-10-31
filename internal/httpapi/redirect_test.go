package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nitinvedi/url-shortener/internal/link"
)

type redirectorFunc func(context.Context, string) (*link.RedirectTarget, error)

func (f redirectorFunc) Resolve(ctx context.Context, code string) (*link.RedirectTarget, error) {
	return f(ctx, code)
}

func newRedirectHandler(service URLRedirector) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandlerWithRedirects(logger, nil, nil, nil, service, "https://sho.rt")
}

func TestPublicRedirectReturns302WithoutAuthentication(t *testing.T) {
	handler := newRedirectHandler(redirectorFunc(func(_ context.Context, code string) (*link.RedirectTarget, error) {
		if code != "Ab12Cd34Ef" {
			t.Fatalf("code = %q, want Ab12Cd34Ef", code)
		}
		return &link.RedirectTarget{URLID: "url-id", OriginalURL: "https://example.com/articles/hello?source=docs"}, nil
	}))

	request := httptest.NewRequest(http.MethodGet, "/Ab12Cd34Ef", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Location"); got != "https://example.com/articles/hello?source=docs" {
		t.Fatalf("Location = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestPublicRedirectMapsUnknownExpiredAndUnavailable(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"unknown", link.ErrNotFound, http.StatusNotFound},
		{"invalid code", link.ErrInvalidCode, http.StatusNotFound},
		{"expired", link.ErrExpired, http.StatusGone},
		{"database unavailable", errors.New("database unavailable"), http.StatusServiceUnavailable},
		{"invalid stored destination", link.ErrInvalidDestination, http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newRedirectHandler(redirectorFunc(func(context.Context, string) (*link.RedirectTarget, error) {
				return nil, test.err
			}))
			request := httptest.NewRequest(http.MethodGet, "/Ab12Cd34Ef", nil)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestPublicRedirectRejectsEmptyServiceResult(t *testing.T) {
	handler := newRedirectHandler(redirectorFunc(func(context.Context, string) (*link.RedirectTarget, error) {
		return nil, nil
	}))
	request := httptest.NewRequest(http.MethodGet, "/Ab12Cd34Ef", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
}
