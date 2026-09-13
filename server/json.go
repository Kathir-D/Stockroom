package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"stockroom/internal/stockroom"
)

// writeJSON encodes v as the JSON response body with the given status. Every
// handler responds through this so the content type is always set.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

// writeError maps stockroom sentinel errors (errors.go) to HTTP statuses so
// handlers never pick status codes themselves. Anything unrecognised is a 500
// with a generic message; the real error is logged server-side only.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	msg := "internal error"

	switch {
	case errors.Is(err, stockroom.ErrNotFound):
		status, msg = http.StatusNotFound, err.Error()
	case errors.Is(err, stockroom.ErrUnauthorized),
		errors.Is(err, stockroom.ErrBadCredentials),
		errors.Is(err, stockroom.ErrPasswordNotSet):
		status, msg = http.StatusUnauthorized, err.Error()
	case errors.Is(err, stockroom.ErrForbidden):
		status, msg = http.StatusForbidden, err.Error()
	case errors.Is(err, stockroom.ErrConflict),
		errors.Is(err, stockroom.ErrOverdueBlocked):
		status, msg = http.StatusConflict, err.Error()
	case errors.Is(err, stockroom.ErrInvalid):
		status, msg = http.StatusBadRequest, err.Error()
	case errors.Is(err, stockroom.ErrNotConfigured):
		status, msg = http.StatusServiceUnavailable, err.Error()
	default:
		log.Printf("error: %v", err)
	}

	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSON reads a JSON request body into v. Bodies are capped at 1 MB and
// unknown fields are rejected so typos in a frontend payload fail loudly as a
// 400 (via ErrInvalid) instead of being silently ignored.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.Join(stockroom.ErrInvalid, err)
	}
	return nil
}
