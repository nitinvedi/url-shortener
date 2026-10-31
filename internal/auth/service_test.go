package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nitinvedi/url-shortener/internal/user"
)

type memoryUserStore struct {
	byEmail map[string]*user.User
}

func newMemoryUserStore() *memoryUserStore {
	return &memoryUserStore{byEmail: make(map[string]*user.User)}
}

func (s *memoryUserStore) Create(_ context.Context, account *user.User) error {
	if _, exists := s.byEmail[account.Email]; exists {
		return user.ErrEmailTaken
	}
	account.ID = "user-" + account.Email
	account.CreatedAt = time.Now().UTC()
	account.UpdatedAt = account.CreatedAt
	copyOfUser := *account
	s.byEmail[account.Email] = &copyOfUser
	return nil
}

func (s *memoryUserStore) GetByEmail(_ context.Context, email string) (*user.User, error) {
	account, ok := s.byEmail[email]
	if !ok {
		return nil, user.ErrNotFound
	}
	copyOfUser := *account
	return &copyOfUser, nil
}

type testPasswordHasher struct{}

func (testPasswordHasher) Hash(password string) (string, error) {
	return "test-hash:" + password, nil
}

func (testPasswordHasher) Verify(encoded, password string) error {
	if encoded != "test-hash:"+password {
		return ErrPasswordMismatch
	}
	return nil
}

type testTokenIssuer struct{}

func (testTokenIssuer) Issue(subject string) (string, time.Time, error) {
	return "test-token:" + subject, time.Now().Add(AccessTokenTTL), nil
}

func newTestService(t *testing.T) (*Service, *memoryUserStore) {
	t.Helper()
	store := newMemoryUserStore()
	service, err := NewService(store, testPasswordHasher{}, testTokenIssuer{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, store
}

func TestRegisterNormalizesEmailAndStoresOnlyHash(t *testing.T) {
	service, store := newTestService(t)
	session, err := service.Register(context.Background(), "  DEV@Example.COM ", "correct horse battery staple")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if session.User.Email != "dev@example.com" {
		t.Errorf("email = %q, want normalized email", session.User.Email)
	}
	if store.byEmail["dev@example.com"].PasswordHash != "test-hash:correct horse battery staple" {
		t.Errorf("unexpected stored hash: %q", store.byEmail["dev@example.com"].PasswordHash)
	}
	if session.AccessToken == "" || session.ExpiresAt.IsZero() {
		t.Fatal("Register() did not return an access token and expiry")
	}
}

func TestRegisterRejectsInvalidInputAndDuplicateEmail(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.Register(context.Background(), "not-an-email", "correct horse battery staple"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid email error = %v, want ErrInvalidInput", err)
	}
	if _, err := service.Register(context.Background(), "dev@example.com", "short"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("short password error = %v, want ErrInvalidInput", err)
	}
	if _, err := service.Register(context.Background(), "dev@example.com", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Register(context.Background(), "DEV@example.com", "another correct password"); !errors.Is(err, user.ErrEmailTaken) {
		t.Fatalf("duplicate email error = %v, want ErrEmailTaken", err)
	}
}

func TestLoginUsesGenericInvalidCredentials(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.Register(context.Background(), "dev@example.com", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	session, err := service.Login(context.Background(), " DEV@example.com ", "correct horse battery staple")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if session.User.Email != "dev@example.com" {
		t.Errorf("email = %q", session.User.Email)
	}
	if _, err := service.Login(context.Background(), "dev@example.com", "wrong password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v, want generic invalid credentials", err)
	}
	if _, err := service.Login(context.Background(), "missing@example.com", "wrong password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown email error = %v, want generic invalid credentials", err)
	}
}
