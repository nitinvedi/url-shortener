package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/nitinvedi/url-shortener/internal/auth"
	"github.com/nitinvedi/url-shortener/internal/link"
)

const maxURLRequestBody = 4 * 1024

// URLCreator is the application boundary needed by the create-URL HTTP handler.
type URLCreator interface {
	Create(ctx context.Context, input link.CreateInput) (*link.ShortURL, error)
}

type urlHTTPHandler struct {
	service       URLCreator
	publicBaseURL string
	logger        *slog.Logger
}

type createURLRequest struct {
	URL         string  `json:"url"`
	CustomAlias string  `json:"custom_alias"`
	ExpiresAt   *string `json:"expires_at"`
}

type createURLResponse struct {
	ID          string  `json:"id"`
	ShortCode   string  `json:"short_code"`
	ShortURL    string  `json:"short_url"`
	OriginalURL string  `json:"original_url"`
	CreatedAt   string  `json:"created_at"`
	ExpiresAt   *string `json:"expires_at"`
}

func (h *urlHTTPHandler) create(w http.ResponseWriter, r *http.Request) {
	var request createURLRequest
	if err := decodeCreateURLJSON(w, r, &request); err != nil {
		WriteError(w, r, http.StatusBadRequest, "invalid_json", "request body must contain valid JSON with a url")
		return
	}

	var expiresAt *time.Time
	if request.ExpiresAt != nil {
		parsed, err := time.Parse(time.RFC3339, *request.ExpiresAt)
		if err != nil {
			WriteError(w, r, http.StatusBadRequest, "invalid_expiry", "expires_at must be a valid RFC 3339 timestamp in the future")
			return
		}
		expiresAt = &parsed
	}

	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		WriteError(w, r, http.StatusUnauthorized, "unauthorized", "authentication is required")
		return
	}

	created, err := h.service.Create(r.Context(), link.CreateInput{
		UserID:      userID,
		OriginalURL: request.URL,
		CustomAlias: request.CustomAlias,
		ExpiresAt:   expiresAt,
	})
	if err != nil {
		var validationErr *link.ValidationError
		switch {
		case errors.As(err, &validationErr):
			WriteError(w, r, http.StatusBadRequest, validationErr.Code, validationErr.Message)
		case errors.Is(err, link.ErrCodeTaken):
			WriteError(w, r, http.StatusConflict, "short_code_taken", "the requested short code is already in use")
		case errors.Is(err, link.ErrCodeGeneration):
			h.logger.ErrorContext(r.Context(), "short-code collision retry limit reached", "error", err)
			WriteError(w, r, http.StatusInternalServerError, "code_generation_failed", "a short code could not be allocated; retry the request")
		default:
			h.logger.ErrorContext(r.Context(), "create short URL failed", "error", err)
			WriteError(w, r, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		}
		return
	}
	if created == nil {
		h.logger.ErrorContext(r.Context(), "create short URL returned an empty result")
		WriteError(w, r, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		return
	}

	var formattedExpiry *string
	if created.ExpiresAt != nil {
		value := created.ExpiresAt.UTC().Format(time.RFC3339Nano)
		formattedExpiry = &value
	}
	response := createURLResponse{
		ID:          created.ID,
		ShortCode:   created.ShortCode,
		ShortURL:    strings.TrimRight(h.publicBaseURL, "/") + "/" + created.ShortCode,
		OriginalURL: created.OriginalURL,
		CreatedAt:   created.CreatedAt.UTC().Format(time.RFC3339Nano),
		ExpiresAt:   formattedExpiry,
	}
	WriteJSON(w, http.StatusCreated, response)
}

func decodeCreateURLJSON(w http.ResponseWriter, r *http.Request, target any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return fmt.Errorf("content type must be application/json")
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxURLRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}
