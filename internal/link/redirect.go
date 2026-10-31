package link

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const (
	RedirectCacheMaxTTL = 5 * time.Minute
	defaultCacheTimeout = 100 * time.Millisecond
)

var (
	ErrInvalidCode        = errors.New("invalid short code")
	ErrExpired            = errors.New("short URL has expired")
	ErrInvalidDestination = errors.New("stored URL destination is invalid")
)

// CachedURL is the non-authoritative redirect representation stored in Redis.
// ExpiresAt is absolute so a cache hit can never outlive the link itself.
type CachedURL struct {
	ShortCode   string     `json:"short_code"`
	URLID       string     `json:"url_id"`
	OriginalURL string     `json:"original_url"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	IsActive    bool       `json:"is_active"`
}

// RedirectCache is deliberately small: Redis is a disposable read-through
// acceleration layer, not a source of truth.
type RedirectCache interface {
	Get(ctx context.Context, code string) (*CachedURL, error)
	Set(ctx context.Context, code string, value *CachedURL, ttl time.Duration) error
	Delete(ctx context.Context, code string) error
}

// RedirectTarget is safe to use as the Location value of a public redirect.
type RedirectTarget struct {
	URLID       string
	OriginalURL string
}

// ClickEvent contains only the URL identifier and UTC event time. No IP address
// or user-agent data is collected by the redirect path.
type ClickEvent struct {
	URLID      string
	OccurredAt time.Time
}

// ClickRecorder must enqueue quickly and must not perform reporting or database
// aggregation inline with a redirect. A later analytics adapter can implement
// this contract with a bounded in-process queue.
type ClickRecorder interface {
	Enqueue(event ClickEvent)
}

// NoopClickRecorder keeps the redirect service usable before the analytics
// persistence milestone is implemented.
type NoopClickRecorder struct{}

func (NoopClickRecorder) Enqueue(ClickEvent) {}

// RedirectService resolves public codes, optionally caches active destinations,
// and invalidates cache entries after owner-scoped deactivation.
type RedirectService struct {
	repository   Repository
	cache        RedirectCache
	clicks       ClickRecorder
	logger       *slog.Logger
	now          func() time.Time
	cacheTimeout time.Duration
}

func NewRedirectService(repository Repository, cache RedirectCache, clicks ClickRecorder, logger *slog.Logger) (*RedirectService, error) {
	return NewRedirectServiceWithClock(repository, cache, clicks, logger, time.Now, defaultCacheTimeout)
}

// NewRedirectServiceWithClock provides deterministic time and cache-timeout
// seams for tests. Production code should use NewRedirectService.
func NewRedirectServiceWithClock(
	repository Repository,
	cache RedirectCache,
	clicks ClickRecorder,
	logger *slog.Logger,
	now func() time.Time,
	cacheTimeout time.Duration,
) (*RedirectService, error) {
	if repository == nil {
		return nil, fmt.Errorf("link repository is required")
	}
	if now == nil {
		now = time.Now
	}
	if cacheTimeout <= 0 {
		return nil, fmt.Errorf("cache timeout must be greater than zero")
	}
	if clicks == nil {
		clicks = NoopClickRecorder{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &RedirectService{
		repository:   repository,
		cache:        cache,
		clicks:       clicks,
		logger:       logger,
		now:          now,
		cacheTimeout: cacheTimeout,
	}, nil
}

// Resolve returns an active, unexpired destination. Cache failures are treated
// as misses; PostgreSQL remains authoritative for all uncached lookups.
func (s *RedirectService) Resolve(ctx context.Context, code string) (*RedirectTarget, error) {
	if !validShortCode(code) {
		return nil, ErrInvalidCode
	}

	now := s.now().UTC()
	if s.cache != nil {
		cached, err := s.getCached(ctx, code)
		switch {
		case err != nil:
			s.logger.WarnContext(ctx, "redirect cache read failed; falling back to PostgreSQL",
				"short_code", code, "error", err)
		case cached != nil:
			if cached.ShortCode == code && cached.IsActive && cached.URLID != "" {
				if cached.ExpiresAt != nil && !cached.ExpiresAt.After(now) {
					s.deleteCached(ctx, code)
					return nil, ErrExpired
				}
				if err := validateDestinationURL(cached.OriginalURL); err == nil {
					target := &RedirectTarget{URLID: cached.URLID, OriginalURL: cached.OriginalURL}
					s.enqueueClick(target.URLID, now)
					return target, nil
				}
			}
			// Malformed or inactive cache values are never trusted. Resolve
			// from PostgreSQL so a bad cache entry cannot hide a valid row.
			s.deleteCached(ctx, code)
		}
	}

	shortURL, err := s.repository.GetByCode(ctx, code)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load redirect destination: %w", err)
	}
	if shortURL == nil || !shortURL.IsActive {
		return nil, ErrNotFound
	}
	if shortURL.ExpiresAt != nil && !shortURL.ExpiresAt.After(now) {
		return nil, ErrExpired
	}
	if err := validateDestinationURL(shortURL.OriginalURL); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDestination, err)
	}

	target := &RedirectTarget{
		URLID:       shortURL.ID,
		OriginalURL: shortURL.OriginalURL,
	}
	if s.cache != nil {
		entry := &CachedURL{
			ShortCode:   shortURL.ShortCode,
			URLID:       shortURL.ID,
			OriginalURL: shortURL.OriginalURL,
			ExpiresAt:   cloneTime(shortURL.ExpiresAt),
			IsActive:    true,
		}
		ttl := redirectCacheTTL(now, shortURL.ExpiresAt)
		if ttl > 0 {
			if err := s.setCached(ctx, code, entry, ttl); err != nil {
				s.logger.WarnContext(ctx, "redirect cache write failed",
					"short_code", code, "error", err)
			}
		}
	}
	s.enqueueClick(target.URLID, now)
	return target, nil
}

// Deactivate changes PostgreSQL first, then invalidates Redis. A cache deletion
// failure is observable but does not undo a committed deactivation; the
// five-minute cache bound limits any stale active entry.
func (s *RedirectService) Deactivate(ctx context.Context, userID, code string) error {
	if strings.TrimSpace(userID) == "" || !validShortCode(code) {
		return ErrNotFound
	}
	if err := s.repository.DeactivateByUserAndCode(ctx, userID, code); err != nil {
		return err
	}
	if s.cache != nil {
		if err := s.deleteCached(ctx, code); err != nil {
			s.logger.WarnContext(ctx, "redirect cache invalidation failed after deactivation",
				"short_code", code, "error", err)
		}
	}
	return nil
}

func (s *RedirectService) enqueueClick(urlID string, occurredAt time.Time) {
	// ClickRecorder implementations are contractually enqueue-only. Reporting
	// and aggregation must happen outside this request's critical path.
	s.clicks.Enqueue(ClickEvent{URLID: urlID, OccurredAt: occurredAt.UTC()})
}

func (s *RedirectService) getCached(ctx context.Context, code string) (*CachedURL, error) {
	cacheCtx, cancel := context.WithTimeout(ctx, s.cacheTimeout)
	defer cancel()
	return s.cache.Get(cacheCtx, code)
}

func (s *RedirectService) setCached(ctx context.Context, code string, value *CachedURL, ttl time.Duration) error {
	cacheCtx, cancel := context.WithTimeout(ctx, s.cacheTimeout)
	defer cancel()
	return s.cache.Set(cacheCtx, code, value, ttl)
}

func (s *RedirectService) deleteCached(ctx context.Context, code string) error {
	cacheCtx, cancel := context.WithTimeout(ctx, s.cacheTimeout)
	defer cancel()
	return s.cache.Delete(cacheCtx, code)
}

func redirectCacheTTL(now time.Time, expiresAt *time.Time) time.Duration {
	ttl := RedirectCacheMaxTTL
	if expiresAt == nil {
		return ttl
	}
	remaining := expiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}
	if remaining < ttl {
		return remaining
	}
	return ttl
}

func validShortCode(code string) bool {
	if len(code) < 4 || len(code) > 32 {
		return false
	}
	for i := 0; i < len(code); i++ {
		value := code[i]
		if (value >= 'a' && value <= 'z') ||
			(value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') ||
			value == '-' || value == '_' {
			continue
		}
		return false
	}
	return true
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}
