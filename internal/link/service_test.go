package link

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type serviceRepository struct {
	create func(*ShortURL) error
	calls  int
}

func (r *serviceRepository) Create(_ context.Context, shortURL *ShortURL) error {
	r.calls++
	if r.create != nil {
		return r.create(shortURL)
	}
	shortURL.ID = "url-id"
	shortURL.CreatedAt = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	shortURL.IsActive = true
	return nil
}
func (*serviceRepository) GetByCode(context.Context, string) (*ShortURL, error) {
	return nil, ErrNotFound
}
func (*serviceRepository) ListByUser(context.Context, string, int, *Cursor) ([]ShortURL, error) {
	return nil, nil
}
func (*serviceRepository) DeactivateByUserAndCode(context.Context, string, string) error {
	return nil
}

type sequenceGenerator struct {
	codes []string
	calls int
	err   error
}

func (g *sequenceGenerator) Generate() (string, error) {
	g.calls++
	if g.err != nil {
		return "", g.err
	}
	if len(g.codes) == 0 {
		return "", errors.New("no test codes configured")
	}
	index := g.calls - 1
	if index >= len(g.codes) {
		index = len(g.codes) - 1
	}
	return g.codes[index], nil
}

func newServiceForTest(t *testing.T, repository Repository, generator CodeGenerator) *Service {
	t.Helper()
	service, err := NewServiceWithGenerator(repository, generator, func() time.Time {
		return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("NewServiceWithGenerator() error = %v", err)
	}
	return service
}

func TestCreatePersistsGeneratedCodeAndOptionalExpiry(t *testing.T) {
	repository := &serviceRepository{}
	generator := &sequenceGenerator{codes: []string{"Ab12Cd34Ef"}}
	service := newServiceForTest(t, repository, generator)
	expiresAt := time.Date(2026, 12, 31, 23, 59, 59, 0, time.FixedZone("UTC+5:30", 5*60*60+30*60))

	created, err := service.Create(context.Background(), CreateInput{
		UserID:      "owner-1",
		OriginalURL: "https://example.com/articles/hello?source=docs",
		ExpiresAt:   &expiresAt,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ShortCode != "Ab12Cd34Ef" || created.UserID != "owner-1" {
		t.Fatalf("unexpected persisted URL: %+v", created)
	}
	if created.ExpiresAt == nil || created.ExpiresAt.Location() != time.UTC {
		t.Fatalf("expiry was not normalized to UTC: %+v", created.ExpiresAt)
	}
	if repository.calls != 1 || generator.calls != 1 {
		t.Fatalf("repository calls = %d, generator calls = %d", repository.calls, generator.calls)
	}
}

func TestCreateRetriesGeneratedCodeCollisionsAtMostFiveTimes(t *testing.T) {
	repository := &serviceRepository{create: func(shortURL *ShortURL) error {
		if shortURL.ShortCode != "Gh12Jk34Lm" {
			return ErrCodeTaken
		}
		shortURL.ID = "created"
		return nil
	}}
	generator := &sequenceGenerator{codes: []string{
		"Ab12Cd34Ef", "Ab12Cd34Ef", "Ab12Cd34Ef", "Ab12Cd34Ef", "Gh12Jk34Lm",
	}}
	service := newServiceForTest(t, repository, generator)

	created, err := service.Create(context.Background(), CreateInput{
		UserID: "owner-1", OriginalURL: "https://example.com",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ShortCode != "Gh12Jk34Lm" || repository.calls != 5 {
		t.Fatalf("unexpected result after collision retries: %+v, calls=%d", created, repository.calls)
	}
}

func TestCreateStopsAfterFiveGeneratedCodeCollisions(t *testing.T) {
	repository := &serviceRepository{create: func(*ShortURL) error { return ErrCodeTaken }}
	generator := &sequenceGenerator{codes: []string{"Ab12Cd34Ef"}}
	service := newServiceForTest(t, repository, generator)

	_, err := service.Create(context.Background(), CreateInput{
		UserID: "owner-1", OriginalURL: "https://example.com",
	})
	if !errors.Is(err, ErrCodeGeneration) {
		t.Fatalf("Create() error = %v, want ErrCodeGeneration", err)
	}
	if repository.calls != maxCreateAttempts || generator.calls != maxCreateAttempts {
		t.Fatalf("collision handling was not bounded: repo=%d generator=%d", repository.calls, generator.calls)
	}
}

func TestCreateCustomAliasCollisionIsNotRetried(t *testing.T) {
	repository := &serviceRepository{create: func(*ShortURL) error { return ErrCodeTaken }}
	generator := &sequenceGenerator{codes: []string{"Ab12Cd34Ef"}}
	service := newServiceForTest(t, repository, generator)

	_, err := service.Create(context.Background(), CreateInput{
		UserID: "owner-1", OriginalURL: "https://example.com", CustomAlias: "hello-world",
	})
	if !errors.Is(err, ErrCodeTaken) {
		t.Fatalf("Create() error = %v, want ErrCodeTaken", err)
	}
	if repository.calls != 1 || generator.calls != 0 {
		t.Fatalf("custom alias collision should not retry: repo=%d generator=%d", repository.calls, generator.calls)
	}
}

func TestCreateRejectsMissingOwner(t *testing.T) {
	service := newServiceForTest(t, &serviceRepository{}, &sequenceGenerator{codes: []string{"Ab12Cd34Ef"}})
	_, err := service.Create(context.Background(), CreateInput{OriginalURL: "https://example.com"})
	if err == nil || err.Error() != "authenticated user ID is required" {
		t.Fatalf("Create() error = %v, want authenticated user ID requirement", err)
	}
}

func TestCreateValidation(t *testing.T) {
	past := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		input     CreateInput
		wantError string
	}{
		{"relative URL", CreateInput{UserID: "owner", OriginalURL: "/path"}, "invalid_url"},
		{"unsupported scheme", CreateInput{UserID: "owner", OriginalURL: "ftp://example.com/file"}, "invalid_url"},
		{"missing host", CreateInput{UserID: "owner", OriginalURL: "https:///path"}, "invalid_url"},
		{"credentials", CreateInput{UserID: "owner", OriginalURL: "https://user:pass@example.com"}, "invalid_url"},
		{"malformed percent escape", CreateInput{UserID: "owner", OriginalURL: "https://example.com/%zz"}, "invalid_url"},
		{"control character", CreateInput{UserID: "owner", OriginalURL: "https://example.com/\npath"}, "invalid_url"},
		{"too long", CreateInput{UserID: "owner", OriginalURL: "https://example.com/" + strings.Repeat("a", 2048)}, "invalid_url"},
		{"alias too short", CreateInput{UserID: "owner", OriginalURL: "https://example.com", CustomAlias: "abc"}, "invalid_alias"},
		{"alias invalid chars", CreateInput{UserID: "owner", OriginalURL: "https://example.com", CustomAlias: "hello.world"}, "invalid_alias"},
		{"alias reserved", CreateInput{UserID: "owner", OriginalURL: "https://example.com", CustomAlias: "healthz"}, "invalid_alias"},
		{"alias too long", CreateInput{UserID: "owner", OriginalURL: "https://example.com", CustomAlias: strings.Repeat("a", 33)}, "invalid_alias"},
		{"expiry in past", CreateInput{UserID: "owner", OriginalURL: "https://example.com", ExpiresAt: &past}, "invalid_expiry"},
		{"expiry at now", CreateInput{UserID: "owner", OriginalURL: "https://example.com", ExpiresAt: func() *time.Time { v := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC); return &v }()}, "invalid_expiry"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &serviceRepository{}
			service := newServiceForTest(t, repository, &sequenceGenerator{codes: []string{"Ab12Cd34Ef"}})
			_, err := service.Create(context.Background(), test.input)
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) || validationErr.Code != test.wantError {
				t.Fatalf("Create() error = %v, want validation code %q", err, test.wantError)
			}
			if repository.calls != 0 {
				t.Fatalf("invalid request reached repository %d times", repository.calls)
			}
		})
	}
}

func TestGeneratedCodeUsesUnbiasedBase62Mapping(t *testing.T) {
	// 248 and 255 are rejected; 61 maps to the final Base62 symbol.
	random := bytes.NewReader([]byte{
		248, 255, 61, 0, 1, 2, 3, 4, 5, 6, 7, 8,
		9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20,
		21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
	})
	generator := newSecureCodeGenerator(random)
	code, err := generator.Generate()
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(code) != GeneratedCodeLength || code[0] != '9' {
		t.Fatalf("generated code = %q; first accepted byte should map to '9'", code)
	}
	if !isGeneratedCode(code) {
		t.Fatalf("generated code contains unsupported characters: %q", code)
	}
}

func TestCreatePropagatesRepositoryAndGeneratorErrors(t *testing.T) {
	repositoryFailure := errors.New("database unavailable")
	repository := &serviceRepository{create: func(*ShortURL) error { return repositoryFailure }}
	service := newServiceForTest(t, repository, &sequenceGenerator{codes: []string{"Ab12Cd34Ef"}})
	_, err := service.Create(context.Background(), CreateInput{UserID: "owner", OriginalURL: "https://example.com"})
	if !errors.Is(err, repositoryFailure) {
		t.Fatalf("repository error = %v", err)
	}

	generatorFailure := errors.New("entropy unavailable")
	service = newServiceForTest(t, &serviceRepository{}, &sequenceGenerator{err: generatorFailure})
	_, err = service.Create(context.Background(), CreateInput{UserID: "owner", OriginalURL: "https://example.com"})
	if err == nil || !strings.Contains(err.Error(), "generate short code") {
		t.Fatalf("generator error = %v", err)
	}
}

func TestNewServiceRejectsNilDependencies(t *testing.T) {
	if _, err := NewService(nil); err == nil {
		t.Fatal("NewService(nil) expected an error")
	}
	if _, err := NewServiceWithGenerator(&serviceRepository{}, nil, nil); err == nil {
		t.Fatal("NewServiceWithGenerator with nil generator expected an error")
	}
}

func ExampleService_Create() {
	fmt.Println("POST /api/v1/urls")
	// Output: POST /api/v1/urls
}
