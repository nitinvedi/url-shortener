package link

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"io"
	"testing"
	"time"
)

type redirectTestRepository struct {
	item       *ShortURL
	getErr     error
	getCalls   int
	deactivate error
	deactivateCalls int
}

func (r *redirectTestRepository) Create(context.Context, *ShortURL) error { return nil }
func (r *redirectTestRepository) GetByCode(context.Context, string) (*ShortURL, error) {
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
func (r *redirectTestRepository) DeactivateByUserAndCode(context.Context, string, string) error {
	r.deactivateCalls++
	if r.deactivate != nil {
		return r.deactivate
	}
	return nil
}

type redirectTestCache struct {
	value       *CachedURL
	getErr      error
	setErr      error
	deleteErr   error
	getCalls    int
	setCalls    int
	deleteCalls int
	lastTTL     time.Duration
}

func (c *redirectTestCache) Get(_ context.Context, _ string) (*CachedURL, error) {
	c.getCalls++
	if c.getErr != nil {
		return nil, c.getErr
	}
	if c.value == nil {
		return nil, nil
	}
	copyOfValue := *c.value
	copyOfValue.ExpiresAt = cloneTime(c.value.ExpiresAt)
	return &copyOfValue, nil
}
func (c *redirectTestCache) Set(_ context.Context, _ string, value *CachedURL, ttl time.Duration) error {
	c.setCalls++
	c.lastTTL = ttl
	if c.setErr != nil {
		return c.setErr
	}
	copyOfValue := *value
	copyOfValue.ExpiresAt = cloneTime(value.ExpiresAt)
	c.value = &copyOfValue
	return nil
}
func (c *redirectTestCache) Delete(context.Context, string) error {
	c.deleteCalls++
	if c.deleteErr != nil {
		return c.deleteErr
	}
	c.value = nil
	return nil
}

type redirectTestClickRecorder struct {
	events []ClickEvent
}

func (r *redirectTestClickRecorder) Enqueue(event ClickEvent) {
	r.events = append(r.events, event)
}

func newRedirectServiceForTest(t *testing.T, repository Repository, cache RedirectCache, clicks ClickRecorder, now time.Time) *RedirectService {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := NewRedirectServiceWithClock(repository, cache, clicks, logger, func() time.Time {
		return now
	}, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("NewRedirectServiceWithClock() error = %v", err)
	}
	return service
}

func TestResolveCacheMissReadsPostgresAndPopulatesRedis(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	repository := &redirectTestRepository{item: &ShortURL{
		ID: "url-id", ShortCode: "Ab12Cd34Ef", OriginalURL: "https://example.com/path",
		CreatedAt: now.Add(-time.Hour), IsActive: true,
	}}
	cache := &redirectTestCache{}
	clicks := &redirectTestClickRecorder{}
	service := newRedirectServiceForTest(t, repository, cache, clicks, now)

	target, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if target.URLID != "url-id" || target.OriginalURL != "https://example.com/path" {
		t.Fatalf("unexpected redirect target: %+v", target)
	}
	if repository.getCalls != 1 || cache.getCalls != 1 || cache.setCalls != 1 {
		t.Fatalf("unexpected calls: postgres=%d cache_get=%d cache_set=%d", repository.getCalls, cache.getCalls, cache.setCalls)
	}
	if cache.lastTTL != RedirectCacheMaxTTL {
		t.Fatalf("cache TTL = %s, want %s", cache.lastTTL, RedirectCacheMaxTTL)
	}
	if len(clicks.events) != 1 || clicks.events[0].URLID != "url-id" || !clicks.events[0].OccurredAt.Equal(now) {
		t.Fatalf("click event was not enqueued: %+v", clicks.events)
	}
}

func TestResolveCacheHitSkipsPostgres(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cache := &redirectTestCache{value: &CachedURL{
		ShortCode: "Ab12Cd34Ef", URLID: "cached-url", OriginalURL: "https://cached.example/path", IsActive: true,
	}}
	repository := &redirectTestRepository{getErr: errors.New("postgres must not be called")}
	clicks := &redirectTestClickRecorder{}
	service := newRedirectServiceForTest(t, repository, cache, clicks, now)

	target, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if target.URLID != "cached-url" || target.OriginalURL != "https://cached.example/path" {
		t.Fatalf("unexpected cache result: %+v", target)
	}
	if repository.getCalls != 0 || cache.getCalls != 1 || cache.setCalls != 0 {
		t.Fatalf("cache hit did not bypass PostgreSQL: postgres=%d cache_get=%d cache_set=%d", repository.getCalls, cache.getCalls, cache.setCalls)
	}
	if len(clicks.events) != 1 {
		t.Fatalf("click should be enqueued for cache hits: %+v", clicks.events)
	}
}

func TestResolveCacheOutageFallsBackToPostgres(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	repository := &redirectTestRepository{item: &ShortURL{
		ID: "url-id", ShortCode: "Ab12Cd34Ef", OriginalURL: "https://example.com", IsActive: true,
	}}
	cache := &redirectTestCache{getErr: errors.New("redis unavailable"), setErr: errors.New("redis unavailable")}
	service := newRedirectServiceForTest(t, repository, cache, nil, now)

	target, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if target.URLID != "url-id" || repository.getCalls != 1 {
		t.Fatalf("Redis outage prevented PostgreSQL fallback: target=%+v calls=%d", target, repository.getCalls)
	}
}

func TestResolveExpiredCacheEntryNeverRedirects(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(-time.Second)
	cache := &redirectTestCache{value: &CachedURL{
		ShortCode: "Ab12Cd34Ef", URLID: "url-id", OriginalURL: "https://example.com",
		ExpiresAt: &expiry, IsActive: true,
	}}
	repository := &redirectTestRepository{getErr: errors.New("expired cache should resolve directly")}
	service := newRedirectServiceForTest(t, repository, cache, nil, now)

	_, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("Resolve() error = %v, want ErrExpired", err)
	}
	if repository.getCalls != 0 || cache.deleteCalls != 1 {
		t.Fatalf("expired cache entry was not rejected and removed: postgres=%d delete=%d", repository.getCalls, cache.deleteCalls)
	}
}

func TestResolveDatabaseExpiredAndInactiveLinksAreNotCached(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Second)
	tests := []struct {
		name string
		item *ShortURL
		want error
	}{
		{"expired", &ShortURL{ID: "expired", ShortCode: "Ab12Cd34Ef", OriginalURL: "https://example.com", ExpiresAt: &past, IsActive: true}, ErrExpired},
		{"inactive", &ShortURL{ID: "inactive", ShortCode: "Ab12Cd34Ef", OriginalURL: "https://example.com", IsActive: false}, ErrNotFound},
		{"unknown", nil, ErrNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cache := &redirectTestCache{}
			repository := &redirectTestRepository{item: test.item}
			service := newRedirectServiceForTest(t, repository, cache, nil, now)
			_, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
			if !errors.Is(err, test.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.want)
			}
			if cache.setCalls != 0 {
				t.Fatalf("non-redirectable URL was cached %d times", cache.setCalls)
			}
		})
	}
}

func TestResolveCacheTTLNeverExceedsExpiry(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(45 * time.Second)
	repository := &redirectTestRepository{item: &ShortURL{
		ID: "url-id", ShortCode: "Ab12Cd34Ef", OriginalURL: "https://example.com",
		ExpiresAt: &expiry, IsActive: true,
	}}
	cache := &redirectTestCache{}
	service := newRedirectServiceForTest(t, repository, cache, nil, now)

	if _, err := service.Resolve(context.Background(), "Ab12Cd34Ef"); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if cache.lastTTL != 45*time.Second {
		t.Fatalf("cache TTL = %s, want expiry remainder %s", cache.lastTTL, 45*time.Second)
	}
}

func TestResolveInvalidCodesDoNotTouchDependencies(t *testing.T) {
	repository := &redirectTestRepository{}
	cache := &redirectTestCache{}
	service := newRedirectServiceForTest(t, repository, cache, nil, time.Now())

	for _, code := range []string{"", "abc", "with/slash", "bad space", strings.Repeat("a", 33)} {
		if _, err := service.Resolve(context.Background(), code); !errors.Is(err, ErrInvalidCode) {
			t.Errorf("Resolve(%q) error = %v, want ErrInvalidCode", code, err)
		}
	}
	if repository.getCalls != 0 || cache.getCalls != 0 {
		t.Fatalf("invalid codes reached dependencies: postgres=%d cache=%d", repository.getCalls, cache.getCalls)
	}
}

func TestResolveDatabaseErrorsAreReturned(t *testing.T) {
	repositoryFailure := errors.New("postgres unavailable")
	repository := &redirectTestRepository{getErr: repositoryFailure}
	service := newRedirectServiceForTest(t, repository, &redirectTestCache{}, nil, time.Now())

	_, err := service.Resolve(context.Background(), "Ab12Cd34Ef")
	if !errors.Is(err, repositoryFailure) {
		t.Fatalf("Resolve() error = %v, want wrapped repository error", err)
	}
}

func TestDeactivateCommitsDatabaseThenInvalidatesCache(t *testing.T) {
	repository := &redirectTestRepository{}
	cache := &redirectTestCache{value: &CachedURL{ShortCode: "Ab12Cd34Ef"}}
	service := newRedirectServiceForTest(t, repository, cache, nil, time.Now())

	if err := service.Deactivate(context.Background(), "owner-id", "Ab12Cd34Ef"); err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	if repository.deactivateCalls != 1 || cache.deleteCalls != 1 || cache.value != nil {
		t.Fatalf("deactivation did not invalidate cache: db=%d delete=%d cached=%+v", repository.deactivateCalls, cache.deleteCalls, cache.value)
	}
}

func TestDeactivateRemainsSuccessfulWhenRedisInvalidationFails(t *testing.T) {
	repository := &redirectTestRepository{}
	cache := &redirectTestCache{deleteErr: errors.New("redis unavailable")}
	service := newRedirectServiceForTest(t, repository, cache, nil, time.Now())

	if err := service.Deactivate(context.Background(), "owner-id", "Ab12Cd34Ef"); err != nil {
		t.Fatalf("committed deactivation should not fail on cache outage: %v", err)
	}
	if repository.deactivateCalls != 1 || cache.deleteCalls != 1 {
		t.Fatalf("deactivation/invalidation calls = %d/%d", repository.deactivateCalls, cache.deleteCalls)
	}
}

func TestRedirectCacheTTLBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	if got := redirectCacheTTL(now, nil); got != RedirectCacheMaxTTL {
		t.Fatalf("non-expiring TTL = %s", got)
	}
	if got := redirectCacheTTL(now, ptrTime(now.Add(10*time.Minute))); got != RedirectCacheMaxTTL {
		t.Fatalf("long expiry TTL = %s", got)
	}
	if got := redirectCacheTTL(now, ptrTime(now)); got != 0 {
		t.Fatalf("expiry-at-now TTL = %s, want zero", got)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
