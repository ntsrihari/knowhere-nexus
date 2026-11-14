// Package httpx holds the HTTP server, middleware and response helpers shared
// by every Nexus handler.
package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
)

// Error codes used in the JSON error envelope (API_SPEC.md §0).
const (
	CodeUnauthenticated  = "unauthenticated"
	CodeForbidden        = "forbidden"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeTooLarge         = "request_too_large"
	CodeValidation       = "validation_failed"
	CodeInternal         = "internal"
)

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// WriteError writes the standard error envelope.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	var b errorBody
	b.Error.Code = code
	b.Error.Message = message
	WriteJSON(w, status, b)
}

// WriteJSON writes v as JSON with no-store caching.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

// DecodeJSON decodes a request body into v, rejecting unknown fields and
// trailing data. Oversized bodies yield 413; it writes the error response
// itself and returns false on failure.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, http.StatusRequestEntityTooLarge, CodeTooLarge, "request body too large")
			return false
		}
		WriteError(w, http.StatusBadRequest, CodeValidation, "invalid JSON body")
		return false
	}
	if dec.More() {
		WriteError(w, http.StatusBadRequest, CodeValidation, "unexpected data after JSON body")
		return false
	}
	return true
}
