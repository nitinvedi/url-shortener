package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/nitinvedi/url-shortener/internal/auth"
	"github.com/nitinvedi/url-shortener/internal/user"
)

const maxAuthRequestBody = 4 * 1024

type authHTTPHandler struct {
	service *auth.Service
	logger  *slog.Logger
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authUserResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	CreatedAt string `json:"created_at,omitempty"`
}

type authResponse struct {
	User        authUserResponse `json:"user"`
	AccessToken string           `json:"access_token"`
	TokenType   string           `json:"token_type"`
	ExpiresIn   int64            `json:"expires_in"`
}

func (h *authHTTPHandler) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
}

func (h *authHTTPHandler) register(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	var request registerRequest
	if err := decodeAuthJSON(w, r, &request); err != nil {
		WriteError(w, r, http.StatusBadRequest, "invalid_json", "request body must contain valid JSON with email and password")
		return
	}

	session, err := h.service.Register(r.Context(), request.Email, request.Password)
	if err != nil {
		var validationErr *auth.ValidationError
		switch {
		case errors.As(err, &validationErr):
			WriteError(w, r, http.StatusBadRequest, "invalid_input", validationErr.Message)
		case errors.Is(err, user.ErrEmailTaken):
			WriteError(w, r, http.StatusConflict, "email_already_registered", "an account with this email already exists")
		default:
			h.logger.ErrorContext(r.Context(), "account registration failed", "error", err)
			WriteError(w, r, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		}
		return
	}

	WriteJSON(w, http.StatusCreated, authResponse{
		User: authUserResponse{
			ID:        session.User.ID,
			Email:     session.User.Email,
			CreatedAt: session.User.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		},
		AccessToken: session.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int64(auth.AccessTokenTTL.Seconds()),
	})
}

func (h *authHTTPHandler) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	var request registerRequest
	if err := decodeAuthJSON(w, r, &request); err != nil {
		WriteError(w, r, http.StatusBadRequest, "invalid_json", "request body must contain valid JSON with email and password")
		return
	}

	session, err := h.service.Login(r.Context(), request.Email, request.Password)
	if err != nil {
		var validationErr *auth.ValidationError
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			WriteError(w, r, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect")
		case errors.As(err, &validationErr):
			WriteError(w, r, http.StatusBadRequest, "invalid_input", validationErr.Message)
		default:
			h.logger.ErrorContext(r.Context(), "account login failed", "error", err)
			WriteError(w, r, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
		}
		return
	}

	WriteJSON(w, http.StatusOK, authResponse{
		User: authUserResponse{
			ID:    session.User.ID,
			Email: session.User.Email,
		},
		AccessToken: session.AccessToken,
		TokenType:   "Bearer",
		ExpiresIn:   int64(auth.AccessTokenTTL.Seconds()),
	})
}

func decodeAuthJSON(w http.ResponseWriter, r *http.Request, target any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return errors.New("content type must be application/json")
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAuthRequestBody)
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
