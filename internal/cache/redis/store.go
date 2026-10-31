package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nitinvedi/url-shortener/internal/link"
	goredis "github.com/redis/go-redis/v9"
)

// Store implements link.RedirectCache using Redis strings with JSON payloads.
type Store struct {
	client *goredis.Client
}

func New(rawURL string, dialTimeout, readTimeout, writeTimeout time.Duration) (*Store, error) {
	if dialTimeout <= 0 || readTimeout <= 0 || writeTimeout <= 0 {
		return nil, fmt.Errorf("Redis dial, read, and write timeouts must be greater than zero")
	}
	options, err := goredis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}
	options.DialTimeout = dialTimeout
	options.ReadTimeout = readTimeout
	options.WriteTimeout = writeTimeout
	options.MaxRetries = -1
	options.ContextTimeoutEnabled = true

	return &Store{client: goredis.NewClient(options)}, nil
}

func (s *Store) Get(ctx context.Context, code string) (*link.CachedURL, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("Redis client is not initialized")
	}
	payload, err := s.client.Get(ctx, cacheKey(code)).Bytes()
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read redirect cache entry: %w", err)
	}

	var value link.CachedURL
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, fmt.Errorf("decode redirect cache entry: %w", err)
	}
	if value.ShortCode != code {
		return nil, fmt.Errorf("redirect cache entry code does not match key")
	}
	return &value, nil
}

func (s *Store) Set(ctx context.Context, code string, value *link.CachedURL, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("Redis client is not initialized")
	}
	if value == nil {
		return fmt.Errorf("redirect cache value is nil")
	}
	if ttl <= 0 {
		return fmt.Errorf("redirect cache TTL must be greater than zero")
	}
	if value.ShortCode != code {
		return fmt.Errorf("redirect cache value code does not match key")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode redirect cache entry: %w", err)
	}
	if err := s.client.Set(ctx, cacheKey(code), payload, ttl).Err(); err != nil {
		return fmt.Errorf("write redirect cache entry: %w", err)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, code string) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("Redis client is not initialized")
	}
	if err := s.client.Del(ctx, cacheKey(code)).Err(); err != nil {
		return fmt.Errorf("delete redirect cache entry: %w", err)
	}
	return nil
}

func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("Redis client is not initialized")
	}
	if err := s.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping Redis: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	if err := s.client.Close(); err != nil {
		return fmt.Errorf("close Redis client: %w", err)
	}
	return nil
}

func cacheKey(code string) string {
	return "short-url:v1:{" + code + "}"
}
