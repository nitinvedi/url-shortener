package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nitinvedi/url-shortener/internal/link"
	"github.com/nitinvedi/url-shortener/internal/user"
)

func TestLinkServiceCreatePostgresIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := Open(ctx, databaseURL, PoolConfig{
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: 10 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
		ConnectTimeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer db.Close()
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	suffix := randomTestSuffix(t)
	owner := &user.User{
		Email:        "link-service-" + suffix + "@example.test",
		PasswordHash: "test-only-argon2id-hash",
	}
	if err := NewUserRepository(db).Create(ctx, owner); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1::uuid", owner.ID)
	}()

	repository := NewLinkRepository(db)
	service, err := link.NewService(repository)
	if err != nil {
		t.Fatalf("create link service: %v", err)
	}

	created, err := service.Create(ctx, link.CreateInput{
		UserID:      owner.ID,
		OriginalURL: "https://example.test/docs?source=integration",
	})
	if err != nil {
		t.Fatalf("create generated URL: %v", err)
	}
	if len(created.ShortCode) != link.GeneratedCodeLength || created.ID == "" ||
		created.UserID != owner.ID || !created.IsActive {
		t.Fatalf("unexpected generated URL: %+v", created)
	}
	if strings.Trim(created.ShortCode, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789") != "" {
		t.Fatalf("generated code contains non-Base62 characters: %q", created.ShortCode)
	}

	persisted, err := repository.GetByCode(ctx, created.ShortCode)
	if err != nil {
		t.Fatalf("read persisted URL: %v", err)
	}
	if persisted.ID != created.ID || persisted.OriginalURL != created.OriginalURL || persisted.UserID != owner.ID {
		t.Fatalf("persisted URL does not match creation result: %+v", persisted)
	}

	expiry := time.Now().UTC().Add(24 * time.Hour)
	alias := "custom-" + suffix[:8]
	custom, err := service.Create(ctx, link.CreateInput{
		UserID:      owner.ID,
		OriginalURL: "http://example.test/custom",
		CustomAlias: alias,
		ExpiresAt:   &expiry,
	})
	if err != nil {
		t.Fatalf("create custom alias URL: %v", err)
	}
	if custom.ShortCode != alias || custom.ExpiresAt == nil ||
		custom.ExpiresAt.UTC().UnixMicro() != expiry.UTC().UnixMicro() {
		t.Fatalf("custom URL has unexpected persisted fields: %+v", custom)
	}

	_, err = service.Create(ctx, link.CreateInput{
		UserID:      owner.ID,
		OriginalURL: "https://example.test/duplicate",
		CustomAlias: alias,
	})
	if err != link.ErrCodeTaken {
		t.Fatalf("duplicate alias error = %v, want ErrCodeTaken", err)
	}
}
