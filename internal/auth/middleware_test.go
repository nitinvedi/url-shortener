package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMiddlewareSetsIdentityFromVerifiedToken(t *testing.T) {
	manager := testTokenManager(t)
	token, _, err := manager.Issue("user-123")
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := UserIDFromContext(r.Context())
		if !ok || userID != "user-123" {
			t.Errorf("authenticated user = %q, %v", userID, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handler := Middleware(manager, next)
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
}

func TestMiddlewareRejectsMissingMalformedAndExpiredTokens(t *testing.T) {
	manager := testTokenManager(t)
	expired := signedTestToken(t, manager.key, tokenHeader{Algorithm: "HS256", Type: "JWT"}, TokenClaims{
		Issuer: manager.issuer, Audience: manager.audience, Subject: "user",
		IssuedAt: time.Now().Add(-time.Hour).Unix(), ExpiresAt: time.Now().Add(-time.Minute).Unix(),
	})
	tests := []struct {
		name   string
		header string
	}{
		{"missing", ""},
		{"wrong scheme", "Basic token"},
		{"malformed", "Bearer not-a-jwt"},
		{"expired", "Bearer " + expired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			handler := Middleware(manager, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				nextCalled = true
			}))
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tt.header != "" {
				request.Header.Set("Authorization", tt.header)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized || nextCalled {
				t.Fatalf("status = %d, nextCalled = %v; want 401 and false", recorder.Code, nextCalled)
			}
		})
	}
}
