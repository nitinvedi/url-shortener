package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/nitinvedi/url-shortener/internal/analytics"
	"github.com/nitinvedi/url-shortener/internal/link"
)

type RedirectResolver interface {
	Resolve(ctx context.Context, code string) (*link.ShortURL, error)
}

type redirectHTTPHandler struct {
	service  RedirectResolver
	recorder analytics.ClickRecorder
	logger   *slog.Logger
}

func (h *redirectHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.redirect(w, r)
}

func (h *redirectHTTPHandler) redirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	shortURL, err := h.service.Resolve(r.Context(), code)
	if err != nil {
		switch {
		case errors.Is(err, link.ErrInvalidCode), errors.Is(err, link.ErrNotFound):
			http.Error(w, "not found", http.StatusNotFound)
		case errors.Is(err, link.ErrExpired):
			http.Error(w, "gone", http.StatusGone)
		case errors.Is(err, link.ErrUnsafeTarget):
			h.logger.ErrorContext(r.Context(), "stored redirect destination failed validation",
				"short_code", code, "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		default:
			h.logger.ErrorContext(r.Context(), "resolve public short URL failed",
				"short_code", code, "error", err)
			w.Header().Set("Retry-After", "1")
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	if shortURL == nil {
		h.logger.ErrorContext(r.Context(), "redirect resolver returned an empty result",
			"short_code", code)
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	// Implementations must enqueue the click into a bounded buffer and return;
	// analytics aggregation is not allowed to delay the visitor's redirect.
	if h.recorder != nil {
		h.recorder.RecordClick(r.Context(), shortURL.ID)
	}

	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, shortURL.OriginalURL, http.StatusFound)
}
