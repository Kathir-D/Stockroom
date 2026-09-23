package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

// TestUIHandlerRouting pins the three answers uiHandler gives: a real file as
// itself, an unknown app path as index.html, and a missing bundle as a 404 --
// never index.html, which a browser would try to run as JavaScript.
func TestUIHandlerRouting(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":         &fstest.MapFile{Data: []byte("<html>app</html>")},
		"static/index-a1.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	}
	h := uiHandlerFor(dist, true)

	for _, tc := range []struct {
		path string
		code int
		body string
	}{
		{"/", http.StatusOK, "<html>app</html>"},
		{"/kits", http.StatusOK, "<html>app</html>"},
		{"/static/index-a1.js", http.StatusOK, "console.log(1)"},
		{"/static/index-gone.js", http.StatusNotFound, ""},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.code {
			t.Errorf("GET %s = %d, want %d", tc.path, rec.Code, tc.code)
		}
		if tc.body != "" && rec.Body.String() != tc.body {
			t.Errorf("GET %s body = %q, want %q", tc.path, rec.Body.String(), tc.body)
		}
	}
}
