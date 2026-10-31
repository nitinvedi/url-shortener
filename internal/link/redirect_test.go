package link

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type redirectTestRepository struct {
	item            *ShortURL
	getErr          error
	deactivateErr   error
	getCalls        int
	deactivateCalls int
}

func (r *redirectTestRepository) Create(context.Context, *ShortURL) error { return nil }
func (r *redirectTestRepository) GetByCode(_ context.Context, _ string) (*ShortURL, error) {
	r.getCalls++
	if r.getErr != nil {
		return nil, r.getErr
	}
	if r.item == nil {
		return nil, ErrNotFound
	}
	copyOfItem := *r.item
	copyOfItem.ExpiresAt = cloneTime(r.item.ExpiresAt)
	return &copyOfItem, nil
}
func (*redirectTestRepository) ListByUser(context.Context, string, int, *Cursor) ([]ShortURL, error) {
	return nil, nil
}
func (r *redirectTestRepository) DeactivateByUserAndCode(_ context.Context, _, _ string) error {
	r.deactivateCalls++
	if r.deactivateErr != nil {
		return r.deactivateErr
	}
	if r.item == nil {
		return ErrNotFound
	}
	r.item.IsActive = false
	return nil
}

type redirectTestCache struct {
	entries   map[string]CachedRedirect
	ttls      map[string]time.Duration
	getErr    error
	setErr    error
	deleteErr error
	getCalls  int
	setCalls  int
	deletes   []string
}

func newRedirectTestCache() *redirectTestCache {
	return &redirectTestCache{entries: make(map[string]CachedRedirect), ttls: make(map[string]time.Duration)}
}
func (c *redirectTestCache) Get(_ context.Context, code string) (*CachedRedirect, error) {
	c.getCalls++
	if c.getErr != nil {
		return nil, c.getErr
	}
	value, ok := c.entries[code]
	if !ok {
		return nil, nil
	}
	copyOfValue := value
	copyOfValue.ExpiresAt = cloneTime(value.ExpiresAt)
	return &copyOfValue, nil
}
func (c *redirectTestCache) Set(_ context.Context, code string, value CachedRedirect, ttl time.Duration) error {
	c.setCalls++
	if c.setErr != nil {
		return c.setErr
	}
	value.ExpiresAt = cloneTime(value.ExpiresAt)
	c.entries[code] = value
	c.ttls[code] = ttl
	return nil
}
func (c *redirectTestCache) Delete(_ context.Context, code string) error {
	c.deletes = append(c.deletes, code)
	if c.deleteErr != nil {
		return c.deleteErr
	}
	delete(c.entries, code)
	return nil
}

func newRedirectServiceForTest(t *testing.T, repository Repository, cache RedirectCache) *RedirectService {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := NewRedirectService(repository, cache, logger)
	if err != nil {
		t.Fatalf("NewRedirectService() error = %v", err)
	}
	service.now = func() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC) }
	return service
}

func activeRedirectURL(expiry *time.Time) *ShortURL {
	return &ShortURL{
		ID: "url-123", UserID: "owner-123", ShortCode: "Ab12Cd34Ef",
		OriginalURL: "https://example.com/path?from=short",
		CreatedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC),
		ExpiresAt: cloneTime(expiry), IsActive: true,
	}
}

func TestRedirectCacheAsideUsesPostgresOnceThenCache(t *testing.T) {
	repository := &redirectTestRepository{item: activeRedirectURL(nil)}
	cache := newRedirectTestCache()
	service := newRedirectServiceForTest(t, repository, cache)

	first, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
	if err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}
	second, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
	if err != nil {
		t.Fatalf("second Resolve() error = %v", err)
	}
	if first.ID != "url-123" || second.ID != "url-123" ||
		first.OriginalURL != repository.item.OriginalURL || second.OriginalURL != repository.item.OriginalURL {
		t.Fatalf("unexpected resolved values: first=%+v second=%+v", first, second)
	}
	if repository.getCalls != 1 || cache.getCalls != 2 || cache.setCalls != 1 {
		t.Fatalf("unexpected call counts: postgres=%d cache_get=%d cache_set=%d",
			repository.getCalls, cache.getCalls, cache.setCalls)
	}
	if cache.ttls["Ab12Cd34Ef"] != MaxRedirectCacheTTL {
		t.Fatalf("cache TTL = %s, want %s", cache.ttls["Ab12Cd34Ef"], MaxRedirectCacheTTL)
	}
}

func TestRedirectUsesPostgresWhenRedisIsUnavailable(t *testing.T) {
	repository := &redirectTestRepository{item: activeRedirectURL(nil)}
	cache := newRedirectTestCache()
	cache.getErr = errors.New("Redis timeout")
	cache.setErr = errors.New("Redis unavailable")
	service := newRedirectServiceForTest(t, repository, cache)

	resolved, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.OriginalURL != repository.item.OriginalURL || repository.getCalls != 1 {
		t.Fatalf("PostgreSQL fallback failed: resolved=%+v calls=%d", resolved, repository.getCalls)
	}
}

func TestExpiredCachedEntryNeverRedirects(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Second)
	cache := newRedirectTestCache()
	cache.entries["Ab12Cd34Ef"] = CachedRedirect{
		ID: "url-expired", OriginalURL: "https://example.com/old",
		ExpiresAt: &expired, IsActive: true,
	}
	repository := &redirectTestRepository{item: activeRedirectURL(nil)}
	service := newRedirectServiceForTest(t, repository, cache)

	_, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("Resolve() error = %v, want ErrExpired", err)
	}
	if repository.getCalls != 0 {
		t.Fatalf("expired cache entry unexpectedly queried PostgreSQL %d times", repository.getCalls)
	}
	if len(cache.deletes) != 1 {
		t.Fatalf("expired cache entry was not invalidated: %v", cache.deletes)
	}
}

func TestPostgresExpiredAndInactiveMappingsAreNotCached(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	expired := now
	tests := []struct {
		name    string
		item    *ShortURL
		wantErr error
	}{
		{"expired", activeRedirectURL(&expired), ErrExpired},
		{"inactive", func() *ShortURL { v := activeRedirectURL(nil); v.IsActive = false; return v }(), ErrNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cache := newRedirectTestCache()
			repository := &redirectTestRepository{item: test.item}
			service := newRedirectServiceForTest(t, repository, cache)
			_, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.wantErr)
			}
			if cache.setCalls != 0 {
				t.Fatalf("invalid mapping was cached %d times", cache.setCalls)
			}
		})
	}
}

func TestRedirectPropagatesDatabaseErrorsOnCacheMiss(t *testing.T) {
	databaseFailure := errors.New("PostgreSQL unavailable")
	repository := &redirectTestRepository{getErr: databaseFailure}
	service := newRedirectServiceForTest(t, repository, newRedirectTestCache())

	_, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
	if !errors.Is(err, databaseFailure) {
		t.Fatalf("Resolve() error = %v, want database error", err)
	}
}

func TestRedirectRejectsInvalidCodesBeforeStorageAccess(t *testing.T) {
	repository := &redirectTestRepository{item: activeRedirectURL(nil)}
	cache := newRedirectTestCache()
	service := newRedirectServiceForTest(t, repository, cache)

	for _, code := range []string{"abc", "bad.code", "space code", "healthz", strings.Repeat("a", 33)} {
		if _, err := service.Resolve(context.Background(), code); !errors.Is(err, ErrInvalidCode) {
			t.Errorf("Resolve(%q) error = %v, want ErrInvalidCode", code, err)
		}
	}
	if repository.getCalls != 0 || cache.getCalls != 0 {
		t.Fatalf("invalid codes reached dependencies: postgres=%d redis=%d", repository.getCalls, cache.getCalls)
	}
}

func TestRedirectCacheTTLNeverOutlivesExpiry(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(10 * time.Second)
	ttl := redirectCacheTTL(&expiresAt, now)
	if ttl <= 0 || ttl >= expiresAt.Sub(now) || ttl > MaxRedirectCacheTTL {
		t.Fatalf("TTL = %s; must be positive and shorter than expiry", ttl)
	}
	soon := now.Add(500 * time.Millisecond)
	if ttl := redirectCacheTTL(&soon, now); ttl != 0 {
		t.Fatalf("TTL for link expiring inside safety margin = %s, want 0", ttl)
	}
}

func TestDeactivateCommitsBeforeBestEffortCacheInvalidation(t *testing.T) {
	repository := &redirectTestRepository{item: activeRedirectURL(nil)}
	cache := newRedirectTestCache()
	cache.entries["Ab12Cd34Ef"] = CachedRedirect{
		ID: "url-123", OriginalURL: repository.item.OriginalURL, IsActive: true,
	}
	cache.deleteErr = errors.New("Redis unavailable")
	service := newRedirectServiceForTest(t, repository, cache)

	if err := service.Deactivate(context.Background(), "owner-123", "Ab12Cd34Ef"); err != nil {
		t.Fatalf("Deactivate() error = %v; cache failure should be best-effort", err)
	}
	if repository.deactivateCalls != 1 || repository.item.IsActive {
		t.Fatal("PostgreSQL deactivation did not complete")
	}
	if len(cache.deletes) != 1 || cache.deletes[0] != "Ab12Cd34Ef" {
		t.Fatalf("cache invalidation calls = %v", cache.deletes)
	}
}

func TestRedirectCacheInactiveEntryFallsBackToPostgres(t *testing.T) {
	repository := &redirectTestRepository{item: activeRedirectURL(nil)}
	cache := newRedirectTestCache()
	cache.entries["Ab12Cd34Ef"] = CachedRedirect{
		ID: "old-entry", OriginalURL: "https://example.com/old", IsActive: false,
	}
	service := newRedirectServiceForTest(t, repository, cache)

	resolved, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.OriginalURL != repository.item.OriginalURL || repository.getCalls != 1 {
		t.Fatalf("inactive cache entry was trusted instead of using PostgreSQL: %+v", resolved)
	}
}
