package user

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound   = errors.New("user not found")
	ErrEmailTaken = errors.New("email already registered")
)

// User is the persisted account model. PasswordHash stores only an encoded
// password hash, never a plaintext password.
type User struct {
	ID           string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Repository defines persistence operations required by the user service.
type Repository interface {
	Create(ctx context.Context, account *User) error
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
}
