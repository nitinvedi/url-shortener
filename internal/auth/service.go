package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/nitinvedi/url-shortener/internal/user"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidInput       = errors.New("invalid input")
)

type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func (e *ValidationError) Is(target error) bool {
	return target == ErrInvalidInput
}

type UserStore interface {
	Create(ctx context.Context, account *user.User) error
	GetByEmail(ctx context.Context, email string) (*user.User, error)
}

type TokenIssuer interface {
	Issue(subject string) (string, time.Time, error)
}

type Session struct {
	User        *user.User
	AccessToken string
	ExpiresAt   time.Time
}

type Service struct {
	users     UserStore
	passwords PasswordHasher
	tokens    TokenIssuer
}

func NewService(users UserStore, passwords PasswordHasher, tokens TokenIssuer) (*Service, error) {
	if users == nil || passwords == nil || tokens == nil {
		return nil, fmt.Errorf("user store, password hasher, and token issuer are required")
	}
	return &Service{users: users, passwords: passwords, tokens: tokens}, nil
}

func (s *Service) Register(ctx context.Context, email, password string) (*Session, error) {
	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return nil, err
	}
	if len(password) < 8 || len(password) > 128 {
		return nil, &ValidationError{Message: "password must be between 8 and 128 bytes"}
	}

	passwordHash, err := s.passwords.Hash(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	account := &user.User{
		Email:        normalizedEmail,
		PasswordHash: passwordHash,
	}
	if err := s.users.Create(ctx, account); err != nil {
		return nil, err
	}

	token, expiresAt, err := s.tokens.Issue(account.ID)
	if err != nil {
		return nil, fmt.Errorf("issue access token: %w", err)
	}
	return &Session{User: account, AccessToken: token, ExpiresAt: expiresAt}, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (*Session, error) {
	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if len(password) == 0 || len(password) > 128 {
		return nil, ErrInvalidCredentials
	}

	account, err := s.users.GetByEmail(ctx, normalizedEmail)
	if errors.Is(err, user.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("lookup user for login: %w", err)
	}

	if err := s.passwords.Verify(account.PasswordHash, password); err != nil {
		// Treat malformed stored hashes and password mismatches identically to
		// keep login responses from disclosing account state.
		return nil, ErrInvalidCredentials
	}

	token, expiresAt, err := s.tokens.Issue(account.ID)
	if err != nil {
		return nil, fmt.Errorf("issue access token: %w", err)
	}
	return &Session{User: account, AccessToken: token, ExpiresAt: expiresAt}, nil
}

func normalizeEmail(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" || len(normalized) > 254 {
		return "", &ValidationError{Message: "email must be a valid email address"}
	}
	parsed, err := mail.ParseAddress(normalized)
	if err != nil || parsed.Address != normalized || strings.ContainsAny(normalized, " \t\r\n") {
		return "", &ValidationError{Message: "email must be a valid email address"}
	}
	return normalized, nil
}
