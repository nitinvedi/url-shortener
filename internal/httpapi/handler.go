package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"github.com/nitinvedi/url-shortener/internal/auth"
)

const readinessTimeout = 2 * time.Second

// DependencyCheck describes a required or optional runtime dependency. A
// failing required check marks readiness as degraded; liveness is independent.
type DependencyCheck struct {
	Name     string
	Required bool
	Check    func(context.Context) error
}

// NewHandler builds the HTTP handler. Dependency checks can be added as
// PostgreSQL and Redis clients are introduced in later implementation steps.
func NewHandler(logger *slog.Logger, checks ...DependencyCheck) http.Handler {
	return newHandler(logger, nil, nil, nil, nil, "", checks...)
}

// NewHandlerWithAuth registers public authentication endpoints alongside health
// routes. Protected business routes are added in later milestones.
func NewHandlerWithAuth(logger *slog.Logger, authService *auth.Service, checks ...DependencyCheck) http.Handler {
	return newHandler(logger, authService, nil, nil, nil, "", checks...)
}

// NewHandlerWithServices registers authentication and the protected URL
// creation endpoint. The token validator and URL creator are explicit
// dependencies so the HTTP layer cannot invent identity or persistence state.
func NewHandlerWithServices(
	logger *slog.Logger,
	authService *auth.Service,
	tokens auth.TokenValidator,
	urlCreator URLCreator,
	publicBaseURL string,
	checks ...DependencyCheck,
) http.Handler {
	return newHandler(logger, authService, tokens, urlCreator, nil, publicBaseURL, checks...)
}

// NewHandlerWithRedirects registers the public redirect route in addition to
// registration, login, and authenticated URL creation routes.
func NewHandlerWithRedirects(
	logger *slog.Logger,
	authService *auth.Service,
	tokens auth.TokenValidator,
	urlCreator URLCreator,
	redirector URLRedirector,
	publicBaseURL string,
	checks ...DependencyCheck,
) http.Handler {
	return newHandler(logger, authService, tokens, urlCreator, redirector, publicBaseURL, checks...)
}

func newHandler(
	logger *slog.Logger,
	authService *auth.Service,
	tokens auth.TokenValidator,
	urlCreator URLCreator,
	redirector URLRedirector,
	publicBaseURL string,
	checks ...DependencyCheck,
) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()
	if authService != nil {
		(&authHTTPHandler{service: authService, logger: logger}).registerRoutes(mux)
	}
	if tokens != nil && urlCreator != nil && publicBaseURL != "" {
		urlHandler := &urlHTTPHandler{
			service:       urlCreator,
			publicBaseURL: publicBaseURL,
			logger:        logger,
		}
		mux.Handle("POST /api/v1/urls", auth.Middleware(tokens, http.HandlerFunc(urlHandler.create)))
	}
	if redirector != nil {
		mux.Handle("GET /{code}", http.HandlerFunc((&redirectHTTPHandler{
			service: redirector,
			logger:  logger,
		}).redirect))
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		checkCtx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()

		dependencies := make(map[string]string, len(checks))
		ready := true
		for _, dependency := range checks {
			if dependency.Name == "" || dependency.Check == nil {
				dependencies[dependency.Name] = "degraded"
				if dependency.Required {
					ready = false
				}
				continue
			}

			if err := dependency.Check(checkCtx); err != nil {
				dependencies[dependency.Name] = "degraded"
				if dependency.Required {
					ready = false
				}
				logger.WarnContext(r.Context(), "readiness dependency check failed",
					"dependency", dependency.Name,
					"error", err,
				)
				continue
			}
			dependencies[dependency.Name] = "ok"
		}

		status := http.StatusOK
		readiness := "ready"
		if !ready {
			status = http.StatusServiceUnavailable
			readiness = "not_ready"
		}
		WriteJSON(w, status, map[string]any{
			"status":       readiness,
			"dependencies": dependencies,
		})
	})

	var handler http.Handler = mux
	handler = recoverMiddleware(logger, handler)
	handler = accessLogMiddleware(logger, handler)
	handler = requestIDMiddleware(logger, handler)
	return handler
}

func requestIDMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, err := newRequestID()
		if err != nil {
			logger.ErrorContext(r.Context(), "generate request id failed", "error", err)
			requestID = "unavailable"
		}
		w.Header().Set("X-Request-ID", requestID)
		next.ServeHTTP(w, r.WithContext(withRequestID(r.Context(), requestID)))
	})
}

func accessLogMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		requestID, _ := r.Context().Value(requestIDKey).(string)
		logger.InfoContext(r.Context(), "http request completed",
			"request_id", requestID,
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func recoverMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(r.Context(), "http handler panic",
					"request_id", r.Context().Value(requestIDKey),
					"panic", recovered,
				)
				WriteError(w, r, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(body)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func newRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
