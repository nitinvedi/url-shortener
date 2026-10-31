package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func testTokenManager(t *testing.T) *TokenManager {
	t.Helper()
	manager, err := NewTokenManager("0123456789abcdef0123456789abcdef", "url-shortener", "url-shortener-api", AccessTokenTTL)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	return manager
}

func TestTokenManagerIssueAndValidate(t *testing.T) {
	manager := testTokenManager(t)
	token, expiresAt, err := manager.Issue("user-123")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	claims, err := manager.Validate(token)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if claims.Subject != "user-123" {
		t.Errorf("subject = %q, want user-123", claims.Subject)
	}
	if claims.Issuer != "url-shortener" || claims.Audience != "url-shortener-api" {
		t.Errorf("unexpected registered claims: %+v", claims)
	}
	if expiresAt.Sub(time.Unix(claims.IssuedAt, 0)) != AccessTokenTTL {
		t.Errorf("token lifetime = %s, want %s", expiresAt.Sub(time.Unix(claims.IssuedAt, 0)), AccessTokenTTL)
	}
}

func TestTokenManagerRejectsExpiredToken(t *testing.T) {
	manager := testTokenManager(t)
	now := time.Unix(1_800_000_000, 0).UTC()
	manager.now = func() time.Time { return now }
	token := signedTestToken(t, manager.key, tokenHeader{Algorithm: "HS256", Type: "JWT"}, TokenClaims{
		Issuer: manager.issuer, Audience: manager.audience, Subject: "user-123",
		IssuedAt: now.Add(-time.Hour).Unix(), ExpiresAt: now.Add(-time.Second).Unix(),
	})
	if _, err := manager.Validate(token); err != ErrInvalidToken {
		t.Fatalf("Validate() error = %v, want ErrInvalidToken", err)
	}
}

func TestTokenManagerRejectsWrongIssuerAndAudience(t *testing.T) {
	manager := testTokenManager(t)
	now := time.Now().UTC()
	tests := []struct {
		name   string
		claims TokenClaims
	}{
		{"issuer", TokenClaims{Issuer: "other", Audience: manager.audience, Subject: "user", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix()}},
		{"audience", TokenClaims{Issuer: manager.issuer, Audience: "other", Subject: "user", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := signedTestToken(t, manager.key, tokenHeader{Algorithm: "HS256", Type: "JWT"}, tt.claims)
			if _, err := manager.Validate(token); err != ErrInvalidToken {
				t.Fatalf("Validate() error = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestTokenManagerRejectsAlgorithmSubstitutionAndTampering(t *testing.T) {
	manager := testTokenManager(t)
	now := time.Now().UTC()
	claims := TokenClaims{Issuer: manager.issuer, Audience: manager.audience, Subject: "user", IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix()}
	noneToken := signedTestToken(t, manager.key, tokenHeader{Algorithm: "none", Type: "JWT"}, claims)
	if _, err := manager.Validate(noneToken); err != ErrInvalidToken {
		t.Fatalf("none algorithm error = %v, want ErrInvalidToken", err)
	}

	token, _, err := manager.Issue("user")
	if err != nil {
		t.Fatal(err)
	}
	tampered := token[:len(token)-1] + "A"
	if _, err := manager.Validate(tampered); err != ErrInvalidToken {
		t.Fatalf("tampered token error = %v, want ErrInvalidToken", err)
	}
}

func TestNewTokenManagerRequiresStrongSecretAndValidLifetime(t *testing.T) {
	if _, err := NewTokenManager("too-short", "issuer", "audience", AccessTokenTTL); err == nil {
		t.Fatal("short key expected an error")
	}
	if _, err := NewTokenManager("0123456789abcdef0123456789abcdef", "issuer", "audience", 16*time.Minute); err == nil {
		t.Fatal("lifetime over 15 minutes expected an error")
	}
}

func signedTestToken(t *testing.T, key []byte, header tokenHeader, claims TokenClaims) string {
	t.Helper()
	headerBytes, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	input := base64.RawURLEncoding.EncodeToString(headerBytes) + "." + base64.RawURLEncoding.EncodeToString(claimsBytes)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
