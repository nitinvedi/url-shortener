package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/nitinvedi/url-shortener/internal/link"
	redislib "github.com/redis/go-redis/v9"
)

func TestRedisRedirectCacheIntegration(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set TEST_REDIS_ADDR to run Redis integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cache, err := NewClient(address, "", 0, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer cache.Close()

	if err := cache.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	code := uniqueRedisTestCode(t)
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	value := link.CachedRedirect{
		ID:          "url-test-id",
		OriginalURL: "https://example.test/path?from=redis",
		ExpiresAt:   &expiresAt,
		IsActive:    true,
	}
	if err := cache.Set(ctx, code, value, 30*time.Second); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	got, err := cache.Get(ctx, code)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got == nil || got.ID != value.ID || got.OriginalURL != value.OriginalURL ||
		got.ExpiresAt == nil || !got.ExpiresAt.Equal(expiresAt) || !got.IsActive {
		t.Fatalf("Get() returned unexpected value: %+v", got)
	}

	ttl, err := cache.client.TTL(ctx, cacheKey(code)).Result()
	if err != nil || ttl <= 0 || ttl > 30*time.Second {
		t.Fatalf("Redis TTL = %s, error = %v; want positive TTL no greater than 30s", ttl, err)
	}

	missing, err := cache.Get(ctx, code+"missing")
	if err != nil || missing != nil {
		t.Fatalf("missing Get() = (%+v, %v), want (nil, nil)", missing, err)
	}

	if err := cache.Delete(ctx, code); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	deleted, err := cache.Get(ctx, code)
	if err != nil || deleted != nil {
		t.Fatalf("Get() after Delete() = (%+v, %v), want (nil, nil)", deleted, err)
	}
}

func TestRedisRejectsMalformedCacheValues(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set TEST_REDIS_ADDR to run Redis integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cache, err := NewClient(address, "", 0, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer cache.Close()

	code := uniqueRedisTestCode(t)
	if err := cache.client.Set(ctx, cacheKey(code), `{"id":"ok","unexpected":true}`, time.Minute).Err(); err != nil {
		t.Fatalf("seed malformed value: %v", err)
	}
	if _, err := cache.Get(ctx, code); err == nil {
		t.Fatal("Get() expected an error for an unknown JSON field")
	}
}

func TestRedisValidatesConstructionAndTTL(t *testing.T) {
	if _, err := NewClient("", "", 0, time.Second); err == nil {
		t.Fatal("NewClient() expected an error for empty address")
	}
	if _, err := NewClient("localhost:6379", "", -1, time.Second); err == nil {
		t.Fatal("NewClient() expected an error for negative database")
	}
	if _, err := NewClient("localhost:6379", "", 0, 0); err == nil {
		t.Fatal("NewClient() expected an error for zero timeout")
	}
}

func TestRedisRejectsUnboundedTTL(t *testing.T) {
	address := os.Getenv("TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("set TEST_REDIS_ADDR to run Redis integration tests")
	}
	cache, err := NewClient(address, "", 0, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer cache.Close()

	err = cache.Set(context.Background(), "test-code", link.CachedRedirect{
		ID: "id", OriginalURL: "https://example.test", IsActive: true,
	}, link.MaxRedirectCacheTTL+time.Second)
	if err == nil {
		t.Fatal("Set() expected an error for TTL above the 5-minute policy")
	}
}

func uniqueRedisTestCode(t *testing.T) string {
	t.Helper()
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatalf("generate test key: %v", err)
	}
	return "test" + hex.EncodeToString(value[:])
}

// Keep the client import referenced to make the exact Redis.Nil semantics
// explicit in this integration-test package.
var _ = redislib.Nil
