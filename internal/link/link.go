package link

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound  = errors.New("short URL not found")
	ErrCodeTaken = errors.New("short code already exists")
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
