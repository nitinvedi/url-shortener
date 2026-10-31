package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestArgon2idHashAndVerify(t *testing.T) {
	hasher := NewArgon2idHasher()
	password := "correct horse battery staple"

	encoded, err := hasher.Hash(password)
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("hash does not use Argon2id PHC format: %q", encoded)
	}
	if encoded == password {
		t.Fatal("stored password hash equals the plaintext password")
	}
	if err := hasher.Verify(encoded, password); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := hasher.Verify(encoded, "incorrect password"); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("wrong password error = %v, want ErrPasswordMismatch", err)
	}
}

func TestArgon2idRejectsMalformedAndOverCostHashes(t *testing.T) {
	hasher := NewArgon2idHasher()
	for _, value := range []string{
		"not-a-hash",
		"$argon2id$v=19$m=999999999,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2i$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
	} {
		if err := hasher.Verify(value, "some password"); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("Verify(%q) error = %v, want ErrInvalidHash", value, err)
		}
	}
}

func TestArgon2idRejectsInvalidPasswordLength(t *testing.T) {
	hasher := NewArgon2idHasher()
	if _, err := hasher.Hash(""); err == nil {
		t.Fatal("Hash empty password expected an error")
	}
	if _, err := hasher.Hash(strings.Repeat("a", 129)); err == nil {
		t.Fatal("Hash oversized password expected an error")
	}
}
