package auth

import (
	"context"
	"net/http"
	"strings"
)

type TokenValidator interface {
	Validate(raw string) (TokenClaims, error)
}

type principalContextKey struct{}

type Principal struct {
	UserID string
}

// UserIDFromContext returns the authenticated user ID set by Middleware.
func UserIDFromContext(ctx context.Context) (string, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	if !ok || principal.UserID == "" {
		return "", false
	}
	return principal.UserID, true
}

// Middleware authenticates a Bearer token and places the verified subject in
// request context. It never trusts a user ID supplied by request data.
func Middleware(tokens TokenValidator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tokens == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fields := strings.Fields(r.Header.Get("Authorization"))
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		claims, err := tokens.Validate(fields[1])
		if err != nil || claims.Subject == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), principalContextKey{}, Principal{UserID: claims.Subject})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
