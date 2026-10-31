package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nitinvedi/url-shortener/internal/link"
	"github.com/nitinvedi/url-shortener/internal/user"
)

func TestLoadMigrations(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations() error = %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("expected at least one embedded migration")
	}
	if migrations[0].version != 1 {
		t.Fatalf("first migration version = %d, want 1", migrations[0].version)
	}
	if migrations[0].name != "initial_schema" {
		t.Fatalf("first migration name = %q, want initial_schema", migrations[0].name)
	}
	if len(migrations[0].checksum) != 64 {
		t.Fatalf("checksum length = %d, want 64", len(migrations[0].checksum))
	}
}

func TestPostgresRepositoriesIntegration(t *testing.T) {
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
	users := NewUserRepository(db)
	links := NewLinkRepository(db)

	owner := &user.User{
		Email:        "owner-" + suffix + "@example.test",
		PasswordHash: "test-only-argon2id-hash",
	}
	if err := users.Create(ctx, owner); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1::uuid", owner.ID)
	}()

	duplicateEmail := &user.User{
		Email:        strings.ToUpper(owner.Email),
		PasswordHash: "another-test-hash",
	}
	if err := users.Create(ctx, duplicateEmail); !errors.Is(err, user.ErrEmailTaken) {
		t.Fatalf("duplicate email error = %v, want ErrEmailTaken", err)
	}

	foundUser, err := users.GetByEmail(ctx, strings.ToUpper(owner.Email))
	if err != nil {
		t.Fatalf("GetByEmail() error = %v", err)
	}
	if foundUser.ID != owner.ID {
		t.Fatalf("GetByEmail() id = %s, want %s", foundUser.ID, owner.ID)
	}
	if _, err := users.GetByID(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, user.ErrNotFound) {
		t.Fatalf("missing user error = %v, want ErrNotFound", err)
	}

	otherOwner := &user.User{
		Email:        "other-" + suffix + "@example.test",
		PasswordHash: "test-only-argon2id-hash",
	}
	if err := users.Create(ctx, otherOwner); err != nil {
		t.Fatalf("create second owner: %v", err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1::uuid", otherOwner.ID)
	}()

	first := &link.ShortURL{
		UserID:      owner.ID,
		ShortCode:   "a-" + suffix[:12],
		OriginalURL: "https://example.test/first",
	}
	if err := links.Create(ctx, first); err != nil {
		t.Fatalf("create first URL: %v", err)
	}

	second := &link.ShortURL{
		UserID:      owner.ID,
		ShortCode:   "b-" + suffix[:12],
		OriginalURL: "https://example.test/second",
	}
	if err := links.Create(ctx, second); err != nil {
		t.Fatalf("create second URL: %v", err)
	}

	got, err := links.GetByCode(ctx, first.ShortCode)
	if err != nil {
		t.Fatalf("GetByCode() error = %v", err)
	}
	if got.UserID != owner.ID || got.OriginalURL != first.OriginalURL || !got.IsActive {
		t.Fatalf("GetByCode() returned unexpected URL: %+v", got)
	}
	if got.ExpiresAt != nil {
		t.Fatalf("ExpiresAt = %v, want nil", got.ExpiresAt)
	}

	firstPage, err := links.ListByUser(ctx, owner.ID, 1, nil)
	if err != nil {
		t.Fatalf("ListByUser() first page error = %v", err)
	}
	if len(firstPage) != 1 {
		t.Fatalf("first page length = %d, want 1", len(firstPage))
	}
	cursor := &link.Cursor{CreatedAt: firstPage[0].CreatedAt, ID: firstPage[0].ID}
	secondPage, err := links.ListByUser(ctx, owner.ID, 1, cursor)
	if err != nil {
		t.Fatalf("ListByUser() second page error = %v", err)
	}
	if len(secondPage) != 1 || secondPage[0].ID == firstPage[0].ID {
		t.Fatalf("unexpected second page: %+v", secondPage)
	}

	otherUsersURLs, err := links.ListByUser(ctx, otherOwner.ID, 10, nil)
	if err != nil {
		t.Fatalf("ListByUser() for second owner error = %v", err)
	}
	if len(otherUsersURLs) != 0 {
		t.Fatalf("second owner received %d URLs, want 0", len(otherUsersURLs))
	}
	if err := links.DeactivateByUserAndCode(ctx, otherOwner.ID, first.ShortCode); !errors.Is(err, link.ErrNotFound) {
		t.Fatalf("cross-owner deactivation error = %v, want ErrNotFound", err)
	}

	duplicateCode := &link.ShortURL{
		UserID:      otherOwner.ID,
		ShortCode:   first.ShortCode,
		OriginalURL: "https://example.test/duplicate",
	}
	if err := links.Create(ctx, duplicateCode); !errors.Is(err, link.ErrCodeTaken) {
		t.Fatalf("duplicate short code error = %v, want ErrCodeTaken", err)
	}

	if err := links.DeactivateByUserAndCode(ctx, owner.ID, first.ShortCode); err != nil {
		t.Fatalf("deactivate URL: %v", err)
	}
	if err := links.DeactivateByUserAndCode(ctx, owner.ID, first.ShortCode); err != nil {
		t.Fatalf("repeated deactivation should be idempotent: %v", err)
	}
	got, err = links.GetByCode(ctx, first.ShortCode)
	if err != nil {
		t.Fatalf("GetByCode() after deactivation error = %v", err)
	}
	if got.IsActive {
		t.Fatal("URL remains active after deactivation")
	}
}

func randomTestSuffix(t *testing.T) string {
	t.Helper()
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatalf("generate test suffix: %v", err)
	}
	return hex.EncodeToString(value[:])
}
