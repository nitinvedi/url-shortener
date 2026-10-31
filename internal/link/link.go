package link

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("short URL not found")
	ErrExpired        = errors.New("short URL has expired")
	ErrInvalidCode    = errors.New("short code is invalid")
	ErrUnsafeTarget   = errors.New("stored short URL destination is invalid")
	ErrCodeTaken      = errors.New("short code already exists")
)

// ShortURL is a persisted short-link mapping.
type ShortURL struct {
	ID          string
	UserID      string
	ShortCode   string
	OriginalURL string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	IsActive    bool
}

// CachedRedirect contains only the fields needed to resolve a public redirect.
// It is versioned by the Redis key namespace and never contains user data.
type CachedRedirect struct {
	ID          string     `json:"id"`
	OriginalURL string     `json:"original_url"`
	ExpiresAt   *time.Time `json:"expires_at"`
	IsActive    bool       `json:"is_active"`
}

// RedirectCache is the disposable cache boundary used by the redirect use case.
type RedirectCache interface {
	Get(ctx context.Context, code string) (*CachedRedirect, error)
	Set(ctx context.Context, code string, value CachedRedirect, ttl time.Duration) error
	Delete(ctx context.Context, code string) error
}

// Cursor identifies the last item in a stable keyset-paginated listing.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// Repository defines storage operations used by the URL application service.
type Repository interface {
	Create(ctx context.Context, shortURL *ShortURL) error
	GetByCode(ctx context.Context, code string) (*ShortURL, error)
	ListByUser(ctx context.Context, userID string, limit int, before *Cursor) ([]ShortURL, error)
	DeactivateByUserAndCode(ctx context.Context, userID, code string) error
}
