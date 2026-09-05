package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"stockroom/internal/stockroom"
)

// Handler errors are logged; keep the test output clean.
func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	m.Run()
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusCreated, map[string]any{"ok": true, "n": 3})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if got["ok"] != true || got["n"] != float64(3) {
		t.Errorf("body = %v, want the encoded value", got)
	}
}

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

// Handlers return sentinels wrapped in context; the mapping must see through
// both fmt.Errorf(%w) and errors.Join.
func TestWriteErrorSeesThroughWrapping(t *testing.T) {
	wrapped := fmt.Errorf("check out asset CAM-001: %w", stockroom.ErrConflict)
	rec := httptest.NewRecorder()
	writeError(rec, wrapped)
	if rec.Code != http.StatusConflict {
		t.Errorf("wrapped sentinel: status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "check out asset CAM-001") {
		t.Errorf("wrapped sentinel: body = %q, want the surrounding context preserved", rec.Body.String())
	}

	joined := errors.Join(stockroom.ErrInvalid, errors.New("json: cannot unmarshal"))
	rec = httptest.NewRecorder()
	writeError(rec, joined)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("joined sentinel: status = %d, want 400", rec.Code)
	}
}

// An unrecognised error must be a 500 whose body says nothing about the
// underlying failure -- connection strings and SQL must not reach the client.
func TestWriteErrorHidesUnknownDetail(t *testing.T) {
	secret := `pq: password authentication failed for user "postgres" at 127.0.0.1:54322`
	rec := httptest.NewRecorder()
	writeError(rec, errors.New(secret))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "postgres") || strings.Contains(body, "54322") {
		t.Errorf("500 body leaks internal detail: %q", body)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if got["error"] != "internal error" {
		t.Errorf("error = %q, want %q", got["error"], "internal error")
	}
}

// An error that wraps two sentinels resolves by the switch's order; pinning it
// down keeps a reordering of the cases from silently changing status codes.
func TestWriteErrorPrefersEarliestMatchingCase(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, errors.Join(stockroom.ErrForbidden, stockroom.ErrNotFound))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (ErrNotFound is matched first)", rec.Code)
	}
}

type payload struct {
	Name string `json:"name"`
	Qty  int    `json:"qty"`
}

func decode(t *testing.T, body string) (payload, error) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	var p payload
	return p, decodeJSON(rec, req, &p)
}

func TestDecodeJSONValid(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"Zoom H6","qty":2}`))
	var p payload
	if err := decodeJSON(rec, req, &p); err != nil {
		t.Fatalf("decodeJSON: %v", err)
	}
	if p.Name != "Zoom H6" || p.Qty != 2 {
		t.Errorf("decoded %+v, want {Zoom H6 2}", p)
	}
}

func TestDecodeJSONRejectsBadBodies(t *testing.T) {
	cases := map[string]string{
		"unknown field":  `{"name":"a","quantity":2}`,
		"malformed":      `{"name":`,
		"wrong type":     `{"qty":"two"}`,
		"empty body":     ``,
		"not an object":  `[1,2,3]`,
		"trailing comma": `{"name":"a",}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := decode(t, body)
			if err == nil {
				t.Fatalf("decodeJSON(%q): want an error, got nil", body)
			}
			// Every decode failure must present as a 400 to the client.
			if !errors.Is(err, stockroom.ErrInvalid) {
				t.Errorf("error %v does not wrap ErrInvalid", err)
			}
		})
	}
}

// The 1 MB cap protects the server from an oversized upload on a shared
// machine; the failure still surfaces as ErrInvalid, not a 500.
func TestDecodeJSONEnforcesSizeLimit(t *testing.T) {
	big := `{"name":"` + strings.Repeat("x", 2<<20) + `"}`
	_, err := decode(t, big)
	if err == nil {
		t.Fatal("decodeJSON with a >1MB body: want an error, got nil")
	}
	if !errors.Is(err, stockroom.ErrInvalid) {
		t.Errorf("error %v does not wrap ErrInvalid", err)
	}

	// Just under the cap still decodes.
	ok := `{"name":"` + strings.Repeat("x", (1<<20)-64) + `"}`
	if _, err := decode(t, ok); err != nil {
		t.Errorf("decodeJSON with a body under the cap: %v", err)
	}
}

// Only the first JSON value is read, matching encoding/json's streaming
// decoder. Documented so a later switch to a strict single-value check is a
// deliberate change rather than an accident.
func TestDecodeJSONIgnoresTrailingData(t *testing.T) {
	p, err := decode(t, `{"name":"a"} {"name":"b"}`)
	if err != nil {
		t.Fatalf("decodeJSON: %v", err)
	}
	if p.Name != "a" {
		t.Errorf("name = %q, want the first value", p.Name)
	}
}
