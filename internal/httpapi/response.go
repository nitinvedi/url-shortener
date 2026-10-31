package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
)

type contextKey string

const requestIDKey contextKey = "request_id"

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// WriteJSON writes a JSON response with the supplied status code.
func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// WriteError writes the API's consistent error envelope. It intentionally
// avoids exposing internal implementation details to callers.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	requestID, _ := r.Context().Value(requestIDKey).(string)
	WriteJSON(w, status, errorResponse{
		Error: errorDetail{
			Code:      code,
			Message:   message,
			RequestID: requestID,
		},
	})
}

func withRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}
