package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"stockroom/internal/stockroom"
)

func TestWriteErrorStatusMapping(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		msg    string
	}{
		{"not found", stockroom.ErrNotFound, http.StatusNotFound, "not found"},
		{"unauthorized", stockroom.ErrUnauthorized, http.StatusUnauthorized, "unauthorized"},
		{"bad credentials", stockroom.ErrBadCredentials, http.StatusUnauthorized, "bad credentials"},
		{"password not set", stockroom.ErrPasswordNotSet, http.StatusUnauthorized, "password not set"},
		{"forbidden", stockroom.ErrForbidden, http.StatusForbidden, "forbidden"},
		{"conflict", stockroom.ErrConflict, http.StatusConflict, "conflict"},
		{"overdue blocked", stockroom.ErrOverdueBlocked, http.StatusConflict, "custodian has overdue items"},
		{"invalid", stockroom.ErrInvalid, http.StatusBadRequest, "invalid input"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeError(rec, c.err)

			if rec.Code != c.status {
				t.Errorf("status = %d, want %d", rec.Code, c.status)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
			}
			if body["error"] != c.msg {
				t.Errorf("error = %q, want %q", body["error"], c.msg)
			}
		})
	}
}
