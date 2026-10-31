package link

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"
)

const (
	MaxRedirectCacheTTL     = 5 * time.Minute
	cacheExpirySafetyMargin = time.Second
)

type RedirectService struct {
	repository Repository
	cache      RedirectCache
	logger     *slog.Logger
	now        func() time.Time
}

func NewRedirectService(repository Repository, cache RedirectCache, logger *slog.Logger) (*RedirectService, error) {
	if repository == nil {
		return nil, fmt.Errorf("link repository is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &RedirectService{repository: repository, cache: cache, logger: logger, now: time.Now}, nil
}

// Resolve uses cache-aside lookup while rechecking the absolute expiration on
// every cache hit. PostgreSQL remains authoritative after a cache miss/error.
func (s *RedirectService) Resolve(ctx context.Context, code string) (*ShortURL, error) {
	if !validPublicCode(code) {
		return nil, ErrInvalidCode
	}
	if s.cache != nil {
		cached, err := s.cache.Get(ctx, code)
		switch {
		case err != nil:
			s.logger.WarnContext(ctx, "redirect cache read failed; falling back to PostgreSQL",
				"short_code", code, "error", err)
		case cached != nil:
			if !cached.IsActive {
				// Inactive entries should never be cached. Consult PostgreSQL.
				if err := s.cache.Delete(ctx, code); err != nil {
					s.logger.WarnContext(ctx, "could not remove inactive redirect cache entry",
						"short_code", code, "error", err)
				}
			} else if cached.ExpiresAt != nil && !s.now().Before(*cached.ExpiresAt) {
				if err := s.cache.Delete(ctx, code); err != nil {
					s.logger.WarnContext(ctx, "could not remove expired redirect cache entry",
						"short_code", code, "error", err)
				}
				return nil, ErrExpired
			} else if cached.ID == "" || validateDestinationURL(cached.OriginalURL) != nil {
				s.logger.WarnContext(ctx, "redirect cache contained an invalid entry", "short_code", code)
				if err := s.cache.Delete(ctx, code); err != nil {
					s.logger.WarnContext(ctx, "could not remove invalid redirect cache entry",
						"short_code", code, "error", err)
				}
			} else {
				return &ShortURL{
					ID:          cached.ID,
					ShortCode:   code,
					OriginalURL: cached.OriginalURL,
					ExpiresAt:   cloneTime(cached.ExpiresAt),
					IsActive:    true,
				}, nil
			}
		}
	}
	return s.resolveFromRepository(ctx, code)
}

func (s *RedirectService) resolveFromRepository(ctx context.Context, code string) (*ShortURL, error) {
	shortURL, err := s.repository.GetByCode(ctx, code)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read short URL from PostgreSQL: %w", err)
	}
	if !shortURL.IsActive {
		return nil, ErrNotFound
	}
	if shortURL.ExpiresAt != nil && !s.now().Before(*shortURL.ExpiresAt) {
		return nil, ErrExpired
	}
	if validateDestinationURL(shortURL.OriginalURL) != nil {
		return nil, ErrUnsafeTarget
	}
	if s.cache != nil {
		if ttl := redirectCacheTTL(shortURL.ExpiresAt, s.now()); ttl > 0 {
			value := CachedRedirect{
				ID: shortURL.ID, OriginalURL: shortURL.OriginalURL,
				ExpiresAt: cloneTime(shortURL.ExpiresAt), IsActive: true,
			}
			if err := s.cache.Set(ctx, code, value, ttl); err != nil {
				s.logger.WarnContext(ctx, "redirect cache write failed",
					"short_code", code, "error", err)
			}
		}
	}
	return shortURL, nil
}

// Deactivate updates PostgreSQL first, then best-effort invalidates Redis.
func (s *RedirectService) Deactivate(ctx context.Context, userID, code string) error {
	if !validPublicCode(code) {
		return ErrNotFound
	}
	if err := s.repository.DeactivateByUserAndCode(ctx, userID, code); err != nil {
		return err
	}
	if err := s.Invalidate(ctx, code); err != nil {
		s.logger.WarnContext(ctx, "redirect cache invalidation failed after deactivation",
			"short_code", code, "error", err)
	}
	return nil
}

// Invalidate removes a cached mapping after the associated database change
// commits. A failure is returned to the caller for logging/observability.
func (s *RedirectService) Invalidate(ctx context.Context, code string) error {
	if s.cache == nil {
		return nil
	}
	if !validPublicCode(code) {
		return ErrInvalidCode
	}
	if err := s.cache.Delete(ctx, code); err != nil {
		return fmt.Errorf("delete redirect cache entry: %w", err)
	}
	return nil
}

func redirectCacheTTL(expiresAt *time.Time, now time.Time) time.Duration {
	ttl := MaxRedirectCacheTTL
	if expiresAt == nil {
		return ttl
	}
	remaining := expiresAt.Sub(now) - cacheExpirySafetyMargin
	if remaining <= 0 {
		return 0
	}
	if remaining < ttl {
		return remaining
	}
	return ttl
}

func validPublicCode(code string) bool {
	if len(code) < 4 || len(code) > 32 || !utf8.ValidString(code) {
		return false
	}
	if _, reserved := reservedAliases[code]; reserved {
		return false
	}
	for i := 0; i < len(code); i++ {
		value := code[i]
		valid := value >= 'a' && value <= 'z' ||
			value >= 'A' && value <= 'Z' ||
			value >= '0' && value <= '9' ||
			value == '-' || value == '_'
		if !valid {
			return false
		}
	}
	return true
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyOfValue := *value
	return &copyOfValue
}
