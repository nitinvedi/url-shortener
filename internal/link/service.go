package link

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const (
	GeneratedCodeLength = 10
	maxCreateAttempts   = 5
	codeAlphabet        = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

var (
	ErrCodeGeneration = errors.New("could not allocate a unique short code")
	reservedAliases   = map[string]struct{}{
		"api": {}, "healthz": {}, "readyz": {}, "favicon.ico": {}, "robots.txt": {},
	}
)

// CreateInput contains the validated identity and client-supplied URL fields.
// UserID must come from the authenticated request principal, never the body.
type CreateInput struct {
	UserID      string
	OriginalURL string
	CustomAlias string
	ExpiresAt   *time.Time
}

// ValidationError is safe to map to a client-facing 400 response.
type ValidationError struct {
	Code    string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// CodeGenerator produces one case-sensitive, uniformly distributed Base62 code.
type CodeGenerator interface {
	Generate() (string, error)
}

type secureCodeGenerator struct {
	random io.Reader
}

func newSecureCodeGenerator(random io.Reader) CodeGenerator {
	return &secureCodeGenerator{random: random}
}

func (g *secureCodeGenerator) Generate() (string, error) {
	if g.random == nil {
		return "", fmt.Errorf("random source is nil")
	}

	code := make([]byte, 0, GeneratedCodeLength)
	var buffer [32]byte
	for len(code) < GeneratedCodeLength {
		if _, err := io.ReadFull(g.random, buffer[:]); err != nil {
			return "", fmt.Errorf("read secure random bytes: %w", err)
		}
		for _, value := range buffer {
			// 248 is the largest multiple of 62 below 256. Rejecting 248–255
			// avoids modulo bias when mapping random bytes into Base62.
			if value >= 248 {
				continue
			}
			code = append(code, codeAlphabet[int(value)%len(codeAlphabet)])
			if len(code) == GeneratedCodeLength {
				break
			}
		}
	}
	return string(code), nil
}

// Service implements the URL creation use case.
type Service struct {
	repository Repository
	generator  CodeGenerator
	now        func() time.Time
}

func NewService(repository Repository) (*Service, error) {
	return NewServiceWithGenerator(repository, newSecureCodeGenerator(rand.Reader), time.Now)
}

// NewServiceWithGenerator exposes deterministic seams for tests while keeping
// the production constructor pinned to crypto/rand and the system clock.
func NewServiceWithGenerator(repository Repository, generator CodeGenerator, now func() time.Time) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("link repository is required")
	}
	if generator == nil {
		return nil, fmt.Errorf("short-code generator is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, generator: generator, now: now}, nil
}

// Create validates the request and persists an owner-scoped short URL. The
// database unique constraint remains the final collision guard.
func (s *Service) Create(ctx context.Context, input CreateInput) (*ShortURL, error) {
	if strings.TrimSpace(input.UserID) == "" {
		return nil, fmt.Errorf("authenticated user ID is required")
	}
	if err := validateDestinationURL(input.OriginalURL); err != nil {
		return nil, err
	}
	if input.CustomAlias != "" {
		if err := validateAlias(input.CustomAlias); err != nil {
			return nil, err
		}
	}

	now := s.now().UTC()
	var expiresAt *time.Time
	if input.ExpiresAt != nil {
		if !input.ExpiresAt.After(now) {
			return nil, &ValidationError{
				Code:    "invalid_expiry",
				Message: "expires_at must be a valid RFC 3339 timestamp in the future",
			}
		}
		value := input.ExpiresAt.UTC()
		expiresAt = &value
	}

	attempts := maxCreateAttempts
	if input.CustomAlias != "" {
		attempts = 1
	}

	for attempt := 0; attempt < attempts; attempt++ {
		code := input.CustomAlias
		if code == "" {
			var err error
			code, err = s.generator.Generate()
			if err != nil {
				return nil, fmt.Errorf("generate short code: %w", err)
			}
			if !isGeneratedCode(code) {
				return nil, fmt.Errorf("short-code generator returned an invalid code")
			}
		}

		shortURL := &ShortURL{
			UserID:      input.UserID,
			ShortCode:   code,
			OriginalURL: input.OriginalURL,
			ExpiresAt:   expiresAt,
			IsActive:    true,
		}
		err := s.repository.Create(ctx, shortURL)
		if err == nil {
			return shortURL, nil
		}
		if !errors.Is(err, ErrCodeTaken) {
			return nil, fmt.Errorf("persist short URL: %w", err)
		}
		if input.CustomAlias != "" {
			return nil, ErrCodeTaken
		}
	}

	return nil, ErrCodeGeneration
}

func validateDestinationURL(raw string) error {
	if len(raw) == 0 || len(raw) > 2048 || strings.TrimSpace(raw) != raw {
		return &ValidationError{
			Code:    "invalid_url",
			Message: "url must be an absolute HTTP or HTTPS URL no longer than 2048 bytes",
		}
	}
	for _, character := range raw {
		if unicode.IsControl(character) {
			return &ValidationError{
				Code:    "invalid_url",
				Message: "url must not contain control characters",
			}
		}
	}

	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Opaque != "" {
		return &ValidationError{
			Code:    "invalid_url",
			Message: "url must be an absolute HTTP or HTTPS URL",
		}
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") ||
		parsed.Host == "" ||
		parsed.Hostname() == "" ||
		parsed.User != nil {
		return &ValidationError{
			Code:    "invalid_url",
			Message: "url must be an absolute HTTP or HTTPS URL with a hostname and no credentials",
		}
	}
	return nil
}

func validateAlias(alias string) error {
	if len(alias) < 4 || len(alias) > 32 {
		return &ValidationError{
			Code:    "invalid_alias",
			Message: "custom_alias must contain between 4 and 32 letters, digits, hyphens, or underscores",
		}
	}
	if _, reserved := reservedAliases[alias]; reserved {
		return &ValidationError{
			Code:    "invalid_alias",
			Message: "custom_alias is reserved",
		}
	}
	for i := 0; i < len(alias); i++ {
		value := alias[i]
		valid := value >= 'a' && value <= 'z' ||
			value >= 'A' && value <= 'Z' ||
			value >= '0' && value <= '9' ||
			value == '-' || value == '_'
		if !valid {
			return &ValidationError{
				Code:    "invalid_alias",
				Message: "custom_alias must contain only letters, digits, hyphens, or underscores",
			}
		}
	}
	return nil
}

func isGeneratedCode(code string) bool {
	if len(code) != GeneratedCodeLength {
		return false
	}
	for i := 0; i < len(code); i++ {
		if !strings.ContainsRune(codeAlphabet, rune(code[i])) {
			return false
		}
	}
	return true
}
