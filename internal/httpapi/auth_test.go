package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nitinvedi/url-shortener/internal/auth"
	"github.com/nitinvedi/url-shortener/internal/user"
)

type authTestStore struct {
	users map[string]*user.User
}

func newAuthTestService(t *testing.T) *auth.Service {
	t.Helper()
	store := &authTestStore{users: make(map[string]*user.User)}
	service, err := auth.NewService(store, authTestHasher{}, authTestTokens{})
	if err != nil {
		t.Fatalf("NewService(): %v", err)
	}
	return service
}

func (s *authTestStore) Create(_ context.Context, account *user.User) error {
	if _, exists := s.users[account.Email]; exists {
		return user.ErrEmailTaken
	}
	account.ID = "user-" + account.Email
	account.CreatedAt = time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	account.UpdatedAt = account.CreatedAt
	copyOfUser := *account
	s.users[account.Email] = &copyOfUser
	return nil
}

func (s *authTestStore) GetByEmail(_ context.Context, email string) (*user.User, error) {
	account, exists := s.users[email]
	if !exists {
		return nil, user.ErrNotFound
	}
	copyOfUser := *account
	return &copyOfUser, nil
}

type authTestHasher struct{}

func (authTestHasher) Hash(password string) (string, error) {
	return "test-hash:" + password, nil
}

func (authTestHasher) Verify(hash, password string) error {
	if hash != "test-hash:"+password {
		return auth.ErrPasswordMismatch
	}
	return nil
}

type authTestTokens struct{}

func (authTestTokens) Issue(subject string) (string, time.Time, error) {
	return "signed-test-token-for-" + subject, time.Now().Add(auth.AccessTokenTTL), nil
}

func TestRegisterAndLoginHTTP(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandlerWithAuth(logger, newAuthTestService(t))

	register := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(
		`{"email":" DEV@example.com ","password":"correct horse battery staple"}`,
	))
	register.Header.Set("Content-Type", "application/json")
	registerRecorder := httptest.NewRecorder()
	handler.ServeHTTP(registerRecorder, register)

	if registerRecorder.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body = %s", registerRecorder.Code, registerRecorder.Body.String())
	}
	var registerResponse authResponse
	if err := json.Unmarshal(registerRecorder.Body.Bytes(), &registerResponse); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if registerResponse.User.Email != "dev@example.com" {
		t.Errorf("register email = %q", registerResponse.User.Email)
	}
	if registerResponse.AccessToken == "" || registerResponse.TokenType != "Bearer" || registerResponse.ExpiresIn != 900 {
		t.Errorf("unexpected token response: %+v", registerResponse)
	}

	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
		`{"email":"dev@example.com","password":"correct horse battery staple"}`,
	))
	login.Header.Set("Content-Type", "application/json")
	loginRecorder := httptest.NewRecorder()
	handler.ServeHTTP(loginRecorder, login)

	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRecorder.Code, loginRecorder.Body.String())
	}
	var loginResponse authResponse
	if err := json.Unmarshal(loginRecorder.Body.Bytes(), &loginResponse); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResponse.User.ID != registerResponse.User.ID || loginResponse.AccessToken == "" {
		t.Errorf("unexpected login response: %+v", loginResponse)
	}
}

func TestRegisterMapsDuplicateEmailAndValidationErrors(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandlerWithAuth(logger, newAuthTestService(t))
	makeRequest := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	first := makeRequest(`{"email":"dev@example.com","password":"correct horse battery staple"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first registration status = %d", first.Code)
	}
	duplicate := makeRequest(`{"email":"DEV@example.com","password":"another good password"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409; body = %s", duplicate.Code, duplicate.Body.String())
	}
	invalid := makeRequest(`{"email":"nope","password":"short"}`)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d, want 400", invalid.Code)
	}
}

func TestAuthEndpointsRejectInvalidJSONAndContentType(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandlerWithAuth(logger, newAuthTestService(t))
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{"missing content type", "", `{"email":"dev@example.com","password":"correct horse battery staple"}`},
		{"unknown field", "application/json", `{"email":"dev@example.com","password":"correct horse battery staple","role":"admin"}`},
		{"multiple objects", "application/json", `{"email":"dev@example.com","password":"correct horse battery staple"}{}`},
		{"malformed JSON", "application/json", `{"email":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(tt.body))
			request.Header.Set("Content-Type", tt.contentType)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestLoginDoesNotRevealWhetherEmailExists(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandlerWithAuth(logger, newAuthTestService(t))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
		`{"email":"missing@example.com","password":"correct horse battery staple"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "email or password is incorrect") {
		t.Errorf("unexpected generic login failure: %s", recorder.Body.String())
	}
	}

func TestHealthRoutesRemainAvailableWithAuth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandlerWithAuth(logger, newAuthTestService(t))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", recorder.Code)
	}
}
