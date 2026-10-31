package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nitinvedi/url-shortener/internal/auth"
	"github.com/nitinvedi/url-shortener/internal/link"
)

type testTokenValidator struct{}

func (testTokenValidator) Validate(raw string) (auth.TokenClaims, error) {
	if raw != "valid-token" {
		return auth.TokenClaims{}, auth.ErrInvalidToken
	}
	return auth.TokenClaims{Subject: "owner-from-signed-token"}, nil
}

type urlCreatorFunc func(context.Context, link.CreateInput) (*link.ShortURL, error)

func (f urlCreatorFunc) Create(ctx context.Context, input link.CreateInput) (*link.ShortURL, error) {
	return f(ctx, input)
}

func newURLHandler(creator URLCreator) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHandlerWithServices(logger, nil, testTokenValidator{}, creator, "https://sho.rt/")
}

func TestCreateURLUsesAuthenticatedOwnerAndConfiguredPublicOrigin(t *testing.T) {
	var received link.CreateInput
	createdAt := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	handler := newURLHandler(urlCreatorFunc(func(_ context.Context, input link.CreateInput) (*link.ShortURL, error) {
		received = input
		return &link.ShortURL{
			ID:          "url-id",
			UserID:      input.UserID,
			ShortCode:   "Ab12Cd34Ef",
			OriginalURL: input.OriginalURL,
			CreatedAt:   createdAt,
			ExpiresAt:   input.ExpiresAt,
			IsActive:    true,
		}, nil
	}))

	request := httptest.NewRequest(http.MethodPost, "/api/v1/urls", strings.NewReader(
		`{"url":"https://example.com/articles/hello","custom_alias":"hello-world","expires_at":"2026-12-31T23:59:59Z"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer valid-token")
	request.Host = "attacker-controlled.example"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if received.UserID != "owner-from-signed-token" {
		t.Fatalf("service user ID = %q, want verified JWT subject", received.UserID)
	}
	if received.CustomAlias != "hello-world" || received.ExpiresAt == nil || !received.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("service received unexpected create input: %+v", received)
	}

	var response createURLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ShortURL != "https://sho.rt/Ab12Cd34Ef" {
		t.Fatalf("short_url = %q; incoming Host must not be trusted", response.ShortURL)
	}
	if response.ID != "url-id" || response.OriginalURL != received.OriginalURL || response.CreatedAt != createdAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.ExpiresAt == nil || *response.ExpiresAt != expiresAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected expiry response: %+v", response.ExpiresAt)
	}
}

func TestCreateURLRequiresValidBearerToken(t *testing.T) {
	calls := 0
	handler := newURLHandler(urlCreatorFunc(func(context.Context, link.CreateInput) (*link.ShortURL, error) {
		calls++
		return nil, nil
	}))

	tests := []struct {
		name  string
		token string
	}{
		{name: "missing token"},
		{name: "invalid token", token: "Bearer wrong-token"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/urls", strings.NewReader(`{"url":"https://example.com"}`))
			request.Header.Set("Content-Type", "application/json")
			if test.token != "" {
				request.Header.Set("Authorization", test.token)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", recorder.Code)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("unauthenticated request reached URL service %d times", calls)
	}
}

func TestCreateURLRejectsUnknownOwnerFieldAndMalformedRequests(t *testing.T) {
	handler := newURLHandler(urlCreatorFunc(func(context.Context, link.CreateInput) (*link.ShortURL, error) {
		return nil, errors.New("should not be called")
	}))
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{"unknown owner field", "application/json", `{"url":"https://example.com","user_id":"victim"}`},
		{"missing content type", "", `{"url":"https://example.com"}`},
		{"invalid expiry format", "application/json", `{"url":"https://example.com","expires_at":"tomorrow"}`},
		{"multiple JSON values", "application/json", `{"url":"https://example.com"}{}`},
		{"malformed JSON", "application/json", `{"url":`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/urls", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			request.Header.Set("Authorization", "Bearer valid-token")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestCreateURLMapsValidationAndAliasConflicts(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"invalid destination", &link.ValidationError{Code: "invalid_url", Message: "invalid URL"}, http.StatusBadRequest, "invalid_url"},
		{"invalid alias", &link.ValidationError{Code: "invalid_alias", Message: "invalid alias"}, http.StatusBadRequest, "invalid_alias"},
		{"duplicate alias", link.ErrCodeTaken, http.StatusConflict, "short_code_taken"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newURLHandler(urlCreatorFunc(func(context.Context, link.CreateInput) (*link.ShortURL, error) {
				return nil, test.err
			}))
			request := httptest.NewRequest(http.MethodPost, "/api/v1/urls", strings.NewReader(`{"url":"https://example.com"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer valid-token")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("error code missing from response: %s", recorder.Body.String())
			}
		})
	}
}

func TestCreateURLRejectsEmptyServiceResult(t *testing.T) {
	handler := newURLHandler(urlCreatorFunc(func(context.Context, link.CreateInput) (*link.ShortURL, error) {
		return nil, nil
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/urls", strings.NewReader(`{"url":"https://example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer valid-token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
}
