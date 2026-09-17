package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// The Phase 7 routes over HTTP. The rules are pinned one layer down in
// internal/stockroom; these check the wire -- that the routes exist, that a
// student is refused at every one of them, and that an archive survives a
// multipart round trip into a real restore.

// backupDeps is testDeps with a backup folder of its own, so a test never
// writes into whatever this machine has configured.
func backupDeps(t *testing.T) (http.Handler, deps, string) {
	t.Helper()
	h, d := testDeps(t)
	dir := t.TempDir()
	d.db.BackupDir = dir
	d.db.PhotoBackupDir = t.TempDir()
	return h, d, dir
}

// Every route here is admin-only, and the refusal comes from inside
// internal/stockroom rather than from the router -- which is exactly why it is
// worth checking over HTTP that the router did not accidentally skip the
// session wrapper on one of them.
func TestBackupRoutesRefuseAStudent(t *testing.T) {
	h, d, _ := backupDeps(t)
	_, sn := seedUser(t, d, false, "student-route-password")
	token := login(t, h, sn, "student-route-password")

	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/admin/settings", nil},
		{http.MethodPut, "/admin/settings", map[string]any{"schedule_hour": 3}},
		{http.MethodPost, "/admin/settings/test", map[string]any{"target": "github"}},
		{http.MethodPost, "/admin/drive/connect", nil},
		{http.MethodPost, "/admin/drive/finish", map[string]any{"id": "x", "code": "{}"}},
		{http.MethodGet, "/admin/backup/status", nil},
		{http.MethodGet, "/admin/backup/versions?target=local", nil},
		{http.MethodPost, "/admin/restore/remote", map[string]any{"target": "local", "id": "x", "confirm": "RESTORE"}},
		{http.MethodGet, "/admin/photos/generations", nil},
		{http.MethodPost, "/admin/photos/restore", map[string]any{"generation": "gen-2026-01-01"}},
		{http.MethodDelete, "/admin/photos/generations/gen-2026-01-01", nil},
	}
	for _, tc := range cases {
		code, body := call(t, h, tc.method, tc.path, token, tc.body)
		if code != http.StatusForbidden {
			t.Errorf("%s %s as a student = %d %v, want 403", tc.method, tc.path, code, body)
		}
	}
}

func TestSettingsAndStatusRoutes(t *testing.T) {
	h, d, dir := backupDeps(t)
	_, sn := seedUser(t, d, true, "admin-route-password")
	token := login(t, h, sn, "admin-route-password")

	code, body := call(t, h, http.MethodGet, "/admin/settings", token, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /admin/settings = %d %v", code, body)
	}
	if _, ok := body["github_token_set"]; !ok {
		t.Errorf("the settings response does not say whether a token is stored: %v", body)
	}
	if body["github_token"] != "" {
		t.Errorf("the settings response carries a token value %v; it must never leave the server", body["github_token"])
	}

	// A bad number is a 400 naming the field, not a 500 carrying a Postgres
	// constraint name at somebody who cannot read one.
	code, body = call(t, h, http.MethodPut, "/admin/settings", token, map[string]any{"keep_days": 0})
	if code != http.StatusBadRequest {
		t.Errorf("PUT /admin/settings with keep_days = 0 gave %d %v, want 400", code, body)
	}

	code, body = call(t, h, http.MethodGet, "/admin/backup/status", token, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /admin/backup/status = %d %v", code, body)
	}
	if body["dir"] != dir {
		t.Errorf("status reports the folder %v, want %q", body["dir"], dir)
	}

	// A target that is not configured is a 503 with its message intact: an
	// unfilled setting is neither the client's fault nor a bug.
	code, body = call(t, h, http.MethodPost, "/admin/settings/test", token, map[string]any{"target": "drive"})
	if code != http.StatusServiceUnavailable {
		t.Errorf("testing an unconfigured target = %d %v, want 503", code, body)
	}
	code, body = call(t, h, http.MethodPost, "/admin/settings/test", token, map[string]any{"target": "dropbox"})
	if code != http.StatusBadRequest {
		t.Errorf("testing a target that does not exist = %d %v, want 400", code, body)
	}
}

// Back up, then restore the same archive through the upload route. The rules
// are tested one layer down; what this proves is that a 200 MB-capped
// multipart upload reaches RestoreFromReader with its bytes intact.
func TestBackupThenRestoreOverHTTP(t *testing.T) {
	h, d, _ := backupDeps(t)
	_, sn := seedUser(t, d, true, "admin-route-password")
	token := login(t, h, sn, "admin-route-password")

	code, body := call(t, h, http.MethodPost, "/admin/backup", token, nil)
	if code != http.StatusOK {
		t.Fatalf("POST /admin/backup = %d %v", code, body)
	}
	archivePath, _ := body["archive"].(string)
	if archivePath == "" {
		t.Fatalf("the backup response names no archive: %v", body)
	}

	// The versions list is what the restore-by-date picker reads.
	code, body = call(t, h, http.MethodGet, "/admin/backup/versions?target=local", token, nil)
	if code != http.StatusOK {
		t.Fatalf("GET /admin/backup/versions = %d %v", code, body)
	}
	if list, ok := body["_list"].([]any); !ok || len(list) == 0 {
		t.Fatalf("the local backup just written is not in the versions list: %v", body)
	}

	// Without the typed confirmation, nothing happens.
	code, body = uploadRestore(t, h, token, archivePath, "")
	if code != http.StatusBadRequest {
		t.Errorf("restoring with no confirmation = %d %v, want 400", code, body)
	}

	// The login above is a live session, and a restore signs everybody out --
	// which is the point: every token now names a profile row that was just
	// replaced.
	code, body = uploadRestore(t, h, token, archivePath, "RESTORE")
	if code != http.StatusOK {
		t.Fatalf("POST /admin/restore = %d %v", code, body)
	}
	if rows, _ := body["rows"].(float64); rows <= 0 {
		t.Errorf("the restore reports %v rows: %v", body["rows"], body)
	}

	code, _ = call(t, h, http.MethodGet, "/me", token, nil)
	if code != http.StatusUnauthorized {
		t.Errorf("GET /me after a restore = %d, want 401: the sessions were not cleared", code)
	}
}

// uploadRestore posts an archive to /admin/restore the way the admin panel
// does.
func uploadRestore(t *testing.T, h http.Handler, token, path, confirm string) (int, map[string]any) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the archive: %v", err)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "backup.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if confirm != "" {
		if err := w.WriteField("confirm", confirm); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/restore", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	out := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("POST /admin/restore: body is not a JSON object: %q", rec.Body.String())
		}
	}
	return rec.Code, out
}
