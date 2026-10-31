package redis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nitinvedi/url-shortener/internal/link"
)

func TestRedisRedirectCacheIntegration(t *testing.T) {
	rawURL := os.Getenv("TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("set TEST_REDIS_URL to run Redis integration tests")
	}

	store, err := New(rawURL, time.Second, 200*time.Millisecond, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("create Redis store: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	code := fmt.Sprintf("it-%d", time.Now().UnixNano())
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	value := &link.CachedURL{
		ShortCode:   code,
		URLID:       "integration-url-id",
		OriginalURL: "https://example.test/redis-cache",
		ExpiresAt:   &expiresAt,
		IsActive:    true,
	}
	if err := store.Set(ctx, code, value, 30*time.Second); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	ttl, err := store.client.TTL(ctx, cacheKey(code)).Result()
	if err != nil {
		t.Fatalf("read Redis TTL: %v", err)
	}
	if ttl <= 0 || ttl > 30*time.Second {
		t.Fatalf("Redis TTL = %s, want in (0, 30s]", ttl)
	}

	got, err := store.Get(ctx, code)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got == nil || got.URLID != value.URLID || got.OriginalURL != value.OriginalURL ||
		got.ExpiresAt == nil || !got.ExpiresAt.Equal(expiresAt) || !got.IsActive {
		t.Fatalf("Get() returned unexpected value: %+v", got)
	}

	if err := store.Delete(ctx, code); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	got, err = store.Get(ctx, code)
	if err != nil {
		t.Fatalf("Get() after Delete() error = %v", err)
	}
	if got != nil {
		t.Fatalf("deleted cache entry still exists: %+v", got)
	}
}

func TestRedisCacheRejectsMalformedAndMismatchedEntries(t *testing.T) {
	rawURL := os.Getenv("TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("set TEST_REDIS_URL to run Redis integration tests")
	}

	store, err := New(rawURL, time.Second, 200*time.Millisecond, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("create Redis store: %v", err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	code := fmt.Sprintf("it-%d", time.Now().UnixNano())
	if err := store.client.Set(ctx, cacheKey(code), "{not-json", time.Minute).Err(); err != nil {
		t.Fatalf("write malformed value: %v", err)
	}
	if _, err := store.Get(ctx, code); err == nil {
		t.Fatal("Get() expected malformed JSON error")
	}

	if err := store.client.Set(ctx, cacheKey(code), `{"short_code":"another-code"}`, time.Minute).Err(); err != nil {
		t.Fatalf("write mismatched value: %v", err)
	}
	if _, err := store.Get(ctx, code); err == nil {
		t.Fatal("Get() expected cache key/value mismatch error")
	}
	_ = store.Delete(ctx, code)
}

func TestCacheKeyUsesVersionedHashTag(t *testing.T) {
	if got, want := cacheKey("Ab12Cd34Ef"), "short-url:v1:{Ab12Cd34Ef}"; got != want {
		t.Fatalf("cacheKey() = %q, want %q", got, want)
	}
}
