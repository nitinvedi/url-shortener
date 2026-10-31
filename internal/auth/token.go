package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const AccessTokenTTL = 15 * time.Minute

var ErrInvalidToken = errors.New("invalid access token")

type TokenClaims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Subject   string `json:"sub"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

type tokenHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

// TokenManager issues and verifies JWT-shaped HS256 access tokens. The
// accepted algorithm is fixed in code; a token cannot select its verifier.
type TokenManager struct {
	key      []byte
	issuer   string
	audience string
	ttl      time.Duration
	now      func() time.Time
}

func NewTokenManager(signingKey, issuer, audience string, ttl time.Duration) (*TokenManager, error) {
	if len(signingKey) < 32 {
		return nil, fmt.Errorf("JWT signing key must contain at least 32 bytes")
	}
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(audience) == "" {
		return nil, fmt.Errorf("JWT issuer and audience are required")
	}
	if ttl <= 0 || ttl > AccessTokenTTL {
		return nil, fmt.Errorf("JWT lifetime must be greater than zero and no more than 15 minutes")
	}
	return &TokenManager{
		key:      []byte(signingKey),
		issuer:   issuer,
		audience: audience,
		ttl:      ttl,
		now:      time.Now,
	}, nil
}

func (m *TokenManager) Issue(subject string) (string, time.Time, error) {
	if strings.TrimSpace(subject) == "" {
		return "", time.Time{}, fmt.Errorf("token subject is required")
	}
	now := m.now().UTC()
	expiresAt := now.Add(m.ttl)
	header := tokenHeader{Algorithm: "HS256", Type: "JWT"}
	claims := TokenClaims{
		Issuer:    m.issuer,
		Audience:  m.audience,
		Subject:   subject,
		IssuedAt:  now.Unix(),
		ExpiresAt: expiresAt.Unix(),
	}
	headerPart, err := encodeSegment(header)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("encode token header: %w", err)
	}
	claimsPart, err := encodeSegment(claims)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("encode token claims: %w", err)
	}
	signingInput := headerPart + "." + claimsPart
	signature := m.sign(signingInput)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), expiresAt, nil
}

func (m *TokenManager) Validate(raw string) (TokenClaims, error) {
	if len(raw) == 0 || len(raw) > 8192 {
		return TokenClaims{}, ErrInvalidToken
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return TokenClaims{}, ErrInvalidToken
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return TokenClaims{}, ErrInvalidToken
	}
	var header tokenHeader
	if err := decodeStrict(headerBytes, &header); err != nil {
		return TokenClaims{}, ErrInvalidToken
	}
	if header.Algorithm != "HS256" || header.Type != "JWT" {
		return TokenClaims{}, ErrInvalidToken
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return TokenClaims{}, ErrInvalidToken
	}
	expected := m.sign(parts[0] + "." + parts[1])
	if !hmac.Equal(signature, expected) {
		return TokenClaims{}, ErrInvalidToken
	}

	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return TokenClaims{}, ErrInvalidToken
	}
	var claims TokenClaims
	if err := decodeStrict(claimsBytes, &claims); err != nil {
		return TokenClaims{}, ErrInvalidToken
	}

	now := m.now().UTC().Unix()
	if claims.Issuer != m.issuer ||
		claims.Audience != m.audience ||
		strings.TrimSpace(claims.Subject) == "" ||
		claims.IssuedAt <= 0 ||
		claims.ExpiresAt <= now ||
		claims.ExpiresAt <= claims.IssuedAt ||
		claims.IssuedAt > now+60 ||
		claims.ExpiresAt-claims.IssuedAt > int64(m.ttl.Seconds()) {
		return TokenClaims{}, ErrInvalidToken
	}
	return claims, nil
}

func (m *TokenManager) sign(input string) []byte {
	mac := hmac.New(sha256.New, m.key)
	_, _ = mac.Write([]byte(input))
	return mac.Sum(nil)
}

func encodeSegment(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}
