package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

var (
	ErrPasswordMismatch = errors.New("password does not match")
	ErrInvalidHash      = errors.New("invalid password hash")
)

const (
	argon2Time      uint32 = 3
	argon2Memory    uint32 = 64 * 1024
	argon2Threads   uint8  = 4
	argon2KeyLength uint32 = 32
	argon2SaltSize         = 16
)

// PasswordHasher defines the password hashing boundary used by the auth service.
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(encodedHash, password string) error
}

// Argon2idHasher uses the RFC 9106 lower-memory Argon2id recommendation:
// 64 MiB, three iterations, and four lanes. Parameters are encoded with each
// hash so they can be increased later without a database migration.
type Argon2idHasher struct{}

func NewArgon2idHasher() *Argon2idHasher {
	return &Argon2idHasher{}
}

func (h *Argon2idHasher) Hash(password string) (string, error) {
	if len(password) == 0 || len(password) > 128 {
		return "", fmt.Errorf("password length must be between 1 and 128 bytes")
	}

	salt := make([]byte, argon2SaltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	derived := argon2.IDKey(
		[]byte(password),
		salt,
		argon2Time,
		argon2Memory,
		argon2Threads,
		argon2KeyLength,
	)
	encoded := fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argon2Memory,
		argon2Time,
		argon2Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derived),
	)
	return encoded, nil
}

func (h *Argon2idHasher) Verify(encodedHash, password string) error {
	if len(password) == 0 || len(password) > 128 || len(encodedHash) > 512 {
		return ErrInvalidHash
	}

	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return ErrInvalidHash
	}

	versionPart := strings.TrimPrefix(parts[2], "v=")
	version, err := strconv.ParseUint(versionPart, 10, 32)
	if err != nil || version != argon2.Version {
		return ErrInvalidHash
	}

	var memory, iterations uint32
	var parallelism uint8
	parameters := strings.Split(parts[3], ",")
	if len(parameters) != 3 {
		return ErrInvalidHash
	}
	for _, parameter := range parameters {
		keyValue := strings.SplitN(parameter, "=", 2)
		if len(keyValue) != 2 {
			return ErrInvalidHash
		}
		value, err := strconv.ParseUint(keyValue[1], 10, 32)
		if err != nil {
			return ErrInvalidHash
		}
		switch keyValue[0] {
		case "m":
			memory = uint32(value)
		case "t":
			iterations = uint32(value)
		case "p":
			if value > 255 {
				return ErrInvalidHash
			}
			parallelism = uint8(value)
		default:
			return ErrInvalidHash
		}
	}
	// Bound parameters before calling IDKey so a corrupted database value
	// cannot request unbounded CPU or memory during login.
	if memory < 8*1024 || memory > 256*1024 ||
		iterations < 1 || iterations > 10 ||
		parallelism < 1 || parallelism > 8 {
		return ErrInvalidHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return ErrInvalidHash
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return ErrInvalidHash
	}

	actual := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(expected)))
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}
