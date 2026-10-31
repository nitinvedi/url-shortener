package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nitinvedi/url-shortener/internal/analytics"
	"github.com/nitinvedi/url-shortener/internal/link"
)

type redirectResolverFunc func(context.Context, string) (*link.ShortURL, error)

func (f redirectResolverFunc) Resolve(ctx context.Context, code string) (*link.ShortURL, error) {
	return f(ctx, code)
}

type recordingClickRecorder struct {
	urlIDs []string
}

func (r *recordingClickRecorder) RecordClick(_ context.Context, urlID string) {
	r.urlIDs = append(r.urlIDs, urlID)
}

func newRedirectHandler(resolver RedirectResolver, recorder analytics.ClickRecorder) http.Handler {
	return NewHandlerWithRedirects(
		testLogger(),
		nil,
		nil,
		nil,
		resolver,
		recorder,
		"",
	)
}

func TestPublicRedirectReturns302AndRecordsClick(t *testing.T) {
	recorder := &recordingClickRecorder{}
	handler := newRedirectHandler(redirectResolverFunc(func(_ context.Context, code string) (*link.ShortURL, error) {
		if code != "Ab12Cd34Ef" {
			t.Fatalf("resolver code = %q, want Ab12Cd34Ef", code)
		}
		return &link.ShortURL{
			ID:          "url-123",
			ShortCode:   code,
			OriginalURL: "https://example.com/articles/hello?source=short#top",
			IsActive:    true,
		}, nil
	}), recorder)

	request := httptest.NewRequest(http.MethodGet, "/Ab12Cd34Ef", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", response.Code, response.Body.String())
	}
	wantLocation := "https://example.com/articles/hello?source=short#top"
	if got := response.Header().Get("Location"); got != wantLocation {
		t.Fatalf("Location = %q, want %q", got, wantLocation)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if len(recorder.urlIDs) != 1 || recorder.urlIDs[0] != "url-123" {
		t.Fatalf("recorded click IDs = %v, want [url-123]", recorder.urlIDs)
	}
}

func TestPublicRedirectMapsUnknownInvalidAndExpiredCodes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"unknown", link.ErrNotFound, http.StatusNotFound},
		{"invalid code", link.ErrInvalidCode, http.StatusNotFound},
		{"expired", link.ErrExpired, http.StatusGone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newRedirectHandler(redirectResolverFunc(func(context.Context, string) (*link.ShortURL, error) {
				return nil, test.err
			}), nil)
			request := httptest.NewRequest(http.MethodGet, "/Ab12Cd34Ef", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

func TestPublicRedirectReturns503WhenPostgresCannotResolveMiss(t *testing.T) {
	handler := newRedirectHandler(redirectResolverFunc(func(context.Context, string) (*link.ShortURL, error) {
		return nil, errors.New("PostgreSQL unavailable")
	}), nil)
	request := httptest.NewRequest(http.MethodGet, "/Ab12Cd34Ef", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	if response.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q, want 1", response.Header().Get("Retry-After"))
	}
}

func TestPublicRedirectDoesNotRecordClickOnFailure(t *testing.T) {
	recorder := &recordingClickRecorder{}
	handler := newRedirectHandler(redirectResolverFunc(func(context.Context, string) (*link.ShortURL, error) {
		return nil, link.ErrNotFound
	}), recorder)
	request := httptest.NewRequest(http.MethodGet, "/Ab12Cd34Ef", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound || len(recorder.urlIDs) != 0 {
		t.Fatalf("status=%d recorded=%v; failed redirects must not create click events", response.Code, recorder.urlIDs)
	}
}

func TestHealthRouteTakesPrecedenceOverPublicRedirectPattern(t *testing.T) {
	handler := newRedirectHandler(redirectResolverFunc(func(context.Context, string) (*link.ShortURL, error) {
		return nil, link.ErrNotFound
	}), nil)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", response.Code)
	}
}
