package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nitinvedi/url-shortener/internal/link"
	redislib "github.com/redis/go-redis/v9"
)

const maxCachedRedirectBytes = 8 * 1024

// Client is the Redis cache adapter for public redirect mappings. Every Redis
// operation receives its own deadline so a stalled cache cannot hold up the
// PostgreSQL fallback for the full HTTP request timeout.
type Client struct {
	client           *redislib.Client
	operationTimeout time.Duration
}

func NewClient(address, password string, database int, operationTimeout time.Duration) (*Client, error) {
	if strings.TrimSpace(address) == "" {
		return nil, fmt.Errorf("Redis address is required")
	}
	if database < 0 {
		return nil, fmt.Errorf("Redis database number cannot be negative")
	}
	if operationTimeout <= 0 {
		return nil, fmt.Errorf("Redis operation timeout must be greater than zero")
	}

	client := redislib.NewClient(&redislib.Options{
		Addr:         address,
		Password:     password,
		DB:           database,
		DialTimeout:  operationTimeout,
		ReadTimeout:  operationTimeout,
		WriteTimeout: operationTimeout,
	})
	return &Client{client: client, operationTimeout: operationTimeout}, nil
}

// Ping is intended for optional readiness reporting. A Redis failure must not
// make PostgreSQL-backed redirect or URL creation operations unavailable.
func (c *Client) Ping(ctx context.Context) error {
	opCtx, cancel := context.WithTimeout(ctx, c.operationTimeout)
	defer cancel()
	if err := c.client.Ping(opCtx).Err(); err != nil {
		return fmt.Errorf("ping Redis: %w", err)
	}
	return nil
}

func (c *Client) Get(ctx context.Context, code string) (*link.CachedRedirect, error) {
	opCtx, cancel := context.WithTimeout(ctx, c.operationTimeout)
	defer cancel()

	raw, err := c.client.Get(opCtx, cacheKey(code)).Result()
	if errors.Is(err, redislib.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read redirect cache entry: %w", err)
	}
	if len(raw) == 0 || len(raw) > maxCachedRedirectBytes {
		return nil, fmt.Errorf("redirect cache entry has an invalid size")
	}

	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value link.CachedRedirect
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode redirect cache entry: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("redirect cache entry contains trailing data")
	}
	if value.ID == "" || value.OriginalURL == "" {
		return nil, fmt.Errorf("redirect cache entry is missing required fields")
	}
	return &value, nil
}

func (c *Client) Set(ctx context.Context, code string, value link.CachedRedirect, ttl time.Duration) error {
	if ttl <= 0 || ttl > link.MaxRedirectCacheTTL {
		return fmt.Errorf("redirect cache TTL must be greater than zero and no more than %s", link.MaxRedirectCacheTTL)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode redirect cache entry: %w", err)
	}
	if len(encoded) > maxCachedRedirectBytes {
		return fmt.Errorf("redirect cache entry exceeds %d bytes", maxCachedRedirectBytes)
	}

	opCtx, cancel := context.WithTimeout(ctx, c.operationTimeout)
	defer cancel()
	if err := c.client.Set(opCtx, cacheKey(code), encoded, ttl).Err(); err != nil {
		return fmt.Errorf("write redirect cache entry: %w", err)
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, code string) error {
	opCtx, cancel := context.WithTimeout(ctx, c.operationTimeout)
	defer cancel()
	if err := c.client.Del(opCtx, cacheKey(code)).Err(); err != nil {
		return fmt.Errorf("delete redirect cache entry: %w", err)
	}
	return nil
}

func (c *Client) Close() error {
	if err := c.client.Close(); err != nil {
		return fmt.Errorf("close Redis client: %w", err)
	}
	return nil
}

func cacheKey(code string) string {
	return "short-url:v1:{" + code + "}"
}
