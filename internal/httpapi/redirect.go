package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/nitinvedi/url-shortener/internal/link"
)

// URLRedirector is the application boundary for resolving public short codes.
type URLRedirector interface {
	Resolve(ctx context.Context, code string) (*link.RedirectTarget, error)
}

type redirectHTTPHandler struct {
	service URLRedirector
	logger  *slog.Logger
}

func (h *redirectHTTPHandler) redirect(w http.ResponseWriter, r *http.Request) {
	target, err := h.service.Resolve(r.Context(), r.PathValue("code"))
	if err != nil {
		switch {
		case errors.Is(err, link.ErrInvalidCode), errors.Is(err, link.ErrNotFound):
			http.NotFound(w, r)
		case errors.Is(err, link.ErrExpired):
			http.Error(w, "short link has expired", http.StatusGone)
		case errors.Is(err, link.ErrInvalidDestination):
			h.logger.ErrorContext(r.Context(), "stored short URL destination is invalid", "error", err)
			http.Error(w, "short link is unavailable", http.StatusServiceUnavailable)
		default:
			h.logger.ErrorContext(r.Context(), "resolve short URL failed", "error", err)
			http.Error(w, "short link is temporarily unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	if target == nil || target.OriginalURL == "" {
		h.logger.ErrorContext(r.Context(), "redirect service returned an empty destination")
		http.Error(w, "short link is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target.OriginalURL, http.StatusFound)
}
