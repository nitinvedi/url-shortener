package auth

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nitinvedi/url-shortener/internal/storage/postgres"
)

func TestRegistrationAndLoginWithPostgres(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL auth integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := postgres.Open(ctx, databaseURL, postgres.PoolConfig{
		MaxOpenConns: 5, MaxIdleConns: 2,
		ConnMaxLifetime: 10 * time.Minute, ConnMaxIdleTime: 2 * time.Minute,
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("open PostgreSQL: %v", err)
	}
	defer db.Close()
	if err := postgres.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate(): %v", err)
	}

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	email := "auth-integration-" + suffix + "@example.test"
	defer func() {
		_, _ = db.ExecContext(context.Background(), "DELETE FROM users WHERE email = $1", email)
	}()

	tokens, err := NewTokenManager("0123456789abcdef0123456789abcdef", "url-shortener", "url-shortener-api", AccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(postgres.NewUserRepository(db), NewArgon2idHasher(), tokens)
	if err != nil {
		t.Fatal(err)
	}

	registered, err := service.Register(ctx, email, "correct horse battery staple")
	if err != nil {
		t.Fatalf("Register(): %v", err)
	}
	if registered.User.PasswordHash == "correct horse battery staple" {
		t.Fatal("plaintext password was persisted")
	}
	if _, err := tokens.Validate(registered.AccessToken); err != nil {
		t.Fatalf("registration token invalid: %v", err)
	}

	loggedIn, err := service.Login(ctx, strings.ToUpper(email), "correct horse battery staple")
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}
	if loggedIn.User.ID != registered.User.ID {
		t.Fatalf("login user id = %s, want %s", loggedIn.User.ID, registered.User.ID)
	}
	if _, err := service.Login(ctx, email, "wrong password"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password error = %v, want ErrInvalidCredentials", err)
	}

	_, err = db.ExecContext(ctx, "UPDATE users SET password_hash = $1 WHERE id = $2::uuid", "corrupted", registered.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(ctx, email, "correct horse battery staple"); err != ErrInvalidCredentials {
		t.Fatalf("corrupted hash error = %v, want ErrInvalidCredentials", err)
	}
}

