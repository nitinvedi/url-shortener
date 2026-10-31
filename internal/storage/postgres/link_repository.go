package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nitinvedi/url-shortener/internal/link"
)

// LinkRepository stores short URL mappings in PostgreSQL.
type LinkRepository struct {
	db *sql.DB
}

func NewLinkRepository(db *sql.DB) *LinkRepository {
	return &LinkRepository{db: db}
}

func (r *LinkRepository) Create(ctx context.Context, shortURL *link.ShortURL) error {
	if shortURL == nil {
		return fmt.Errorf("short URL is nil")
	}

	var expiresAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO urls (user_id, short_code, original_url, expires_at)
		VALUES ($1::uuid, $2, $3, $4)
		RETURNING id::text, created_at, expires_at, is_active
	`,
		shortURL.UserID,
		shortURL.ShortCode,
		shortURL.OriginalURL,
		shortURL.ExpiresAt,
	).Scan(
		&shortURL.ID,
		&shortURL.CreatedAt,
		&expiresAt,
		&shortURL.IsActive,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return link.ErrCodeTaken
		}
		return fmt.Errorf("create short URL: %w", err)
	}
	shortURL.ExpiresAt = nullTimePointer(expiresAt)
	return nil
}

func (r *LinkRepository) GetByCode(ctx context.Context, code string) (*link.ShortURL, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id::text, user_id::text, short_code, original_url,
		       created_at, expires_at, is_active
		FROM urls
		WHERE short_code = $1
	`, code)

	shortURL, err := scanShortURL(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, link.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get short URL by code: %w", err)
	}
	return shortURL, nil
}

func (r *LinkRepository) ListByUser(ctx context.Context, userID string, limit int, before *link.Cursor) ([]link.ShortURL, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100")
	}

	var (
		rows *sql.Rows
		err  error
	)
	if before == nil {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id::text, user_id::text, short_code, original_url,
			       created_at, expires_at, is_active
			FROM urls
			WHERE user_id = $1::uuid
			ORDER BY created_at DESC, id DESC
			LIMIT $2
		`, userID, limit)
	} else {
		rows, err = r.db.QueryContext(ctx, `
			SELECT id::text, user_id::text, short_code, original_url,
			       created_at, expires_at, is_active
			FROM urls
			WHERE user_id = $1::uuid
			  AND (created_at, id) < ($2, $3::uuid)
			ORDER BY created_at DESC, id DESC
			LIMIT $4
		`, userID, before.CreatedAt, before.ID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("list URLs by user: %w", err)
	}
	defer rows.Close()

	items := make([]link.ShortURL, 0)
	for rows.Next() {
		item, err := scanShortURL(rows)
		if err != nil {
			return nil, fmt.Errorf("scan URL listing: %w", err)
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate URL listing: %w", err)
	}
	return items, nil
}

func (r *LinkRepository) DeactivateByUserAndCode(ctx context.Context, userID, code string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE urls
		SET is_active = FALSE
		WHERE user_id = $1::uuid AND short_code = $2
	`, userID, code)
	if err != nil {
		return fmt.Errorf("deactivate short URL: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deactivation result: %w", err)
	}
	if affected == 0 {
		return link.ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanShortURL(row rowScanner) (*link.ShortURL, error) {
	item := &link.ShortURL{}
	var expiresAt sql.NullTime
	err := row.Scan(
		&item.ID,
		&item.UserID,
		&item.ShortCode,
		&item.OriginalURL,
		&item.CreatedAt,
		&expiresAt,
		&item.IsActive,
	)
	if err != nil {
		return nil, err
	}
	item.ExpiresAt = nullTimePointer(expiresAt)
	return item, nil
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time
	return &t
}
