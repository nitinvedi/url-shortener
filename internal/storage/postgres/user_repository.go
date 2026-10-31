package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"
	"github.com/nitinvedi/url-shortener/internal/user"
)

// UserRepository stores user accounts in PostgreSQL.
type UserRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) Create(ctx context.Context, account *user.User) error {
	if account == nil {
		return fmt.Errorf("user account is nil")
	}
	email := strings.ToLower(strings.TrimSpace(account.Email))
	if email == "" {
		return fmt.Errorf("email is required")
	}
	if account.PasswordHash == "" {
		return fmt.Errorf("password hash is required")
	}

	err := r.db.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash)
		VALUES ($1, $2)
		RETURNING id::text, created_at, updated_at
	`, email, account.PasswordHash).Scan(
		&account.ID,
		&account.CreatedAt,
		&account.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return user.ErrEmailTaken
		}
		return fmt.Errorf("create user: %w", err)
	}
	account.Email = email
	return nil
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*user.User, error) {
	account := &user.User{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, email, password_hash, created_at, updated_at
		FROM users
		WHERE lower(email) = lower($1)
	`, strings.TrimSpace(email)).Scan(
		&account.ID,
		&account.Email,
		&account.PasswordHash,
		&account.CreatedAt,
		&account.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, user.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return account, nil
}

func (r *UserRepository) GetByID(ctx context.Context, id string) (*user.User, error) {
	account := &user.User{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id::text, email, password_hash, created_at, updated_at
		FROM users
		WHERE id = $1::uuid
	`, id).Scan(
		&account.ID,
		&account.Email,
		&account.PasswordHash,
		&account.CreatedAt,
		&account.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, user.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return account, nil
}

func isUniqueViolation(err error) bool {
	var postgresErr *pq.Error
	return errors.As(err, &postgresErr) && postgresErr.Code == "23505"
}
