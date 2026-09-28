package setup

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logRunner answers journalctl with a log that holds secrets.
type logRunner struct{ *fakeRunner }

func (l logRunner) Run(ctx context.Context, c Cmd) (string, error) {
	if c.Name == "journalctl" {
		return "Sep 27 stockroom[1]: rclone: token = {\"access_token\":\"ya29.a0AfB_secret\",\"refresh_token\":\"1//0gSecretSecretSecretSecret\"}\n" +
			"Sep 27 stockroom[1]: push failed: Authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz123456\n" +
			"Sep 27 stockroom[1]: backup ok\n", nil
	}
	return l.fakeRunner.Run(ctx, c)
}

func TestRedactSecrets(t *testing.T) {
	cases := map[string]string{
		"postgresql://stockroom:hunter2@127.0.0.1:5432/stockroom": "postgresql://stockroom:[removed]@127.0.0.1:5432/stockroom",
		`{"refresh_token":"1//0gAbcdefghijklmnopqrstuv"}`:         `{"refresh_token":"[removed]"}`,
		"github_pat_11ABCDEFG0123456789_abcdefghijklmnop":         "[removed]",
		"password=hunter2 next":                                   "password=[removed] next",
		"Authorization: Bearer abc.def.ghi12345":                  "Authorization: Bearer [removed]",
		"backup ok: 12 files":                                     "backup ok: 12 files",
	}
	for in, want := range cases {
		if got := RedactSecrets(in); got != want {
			t.Errorf("RedactSecrets(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactEnv(t *testing.T) {
	in := "# ADMIN_PASSWORD=is documented here\nADMIN_STUDENT_NUMBER=123456\nADMIN_PASSWORD='hunter2'\nSERVER_ADDR=127.0.0.1:8080\nGITHUB_TOKEN=\n"
	got := redactEnv(in)
	for _, want := range []string{"# ADMIN_PASSWORD=is documented here", "ADMIN_STUDENT_NUMBER=[removed]", "ADMIN_PASSWORD=[removed]", "SERVER_ADDR=127.0.0.1:8080", "GITHUB_TOKEN=\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("redactEnv lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "hunter2") || strings.Contains(got, "123456") {
		t.Errorf("redactEnv kept a secret:\n%s", got)
	}
}

// The bundle holds every part, and no secret from the config or the log.
func TestSupportBundle(t *testing.T) {
	env, run, _ := testEnv(t)
	env.Run = logRunner{run}
	dir := t.TempDir()
	cfg := filepath.Join(dir, "stockroom.env")
	os.WriteFile(cfg, []byte("DATABASE_URL=postgresql://stockroom:hunter2@127.0.0.1:1/stockroom\nADMIN_STUDENT_NUMBER=987654\nADMIN_PASSWORD=hunter2\nSERVER_ADDR=127.0.0.1:1\nUPLOADS_DIR="+dir+"\n"), 0o600)
	out := filepath.Join(dir, "bundle.zip")

	path, err := SupportBundle(context.Background(), env, cfg, BundleInfo{Version: "stockroom test", Out: out})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(b)
	}
	for _, name := range []string{"README.txt", "version.txt", "system.txt", "doctor.txt", "config.txt", "service.log"} {
		if _, ok := got[name]; !ok {
			t.Errorf("the bundle lacks %s", name)
		}
	}
	for name, body := range got {
		for _, secret := range []string{"hunter2", "987654", "ya29.a0AfB", "1//0gSecret", "ghp_abcdef"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s holds %q:\n%s", name, secret, body)
			}
		}
	}
	if !strings.Contains(got["service.log"], "backup ok") {
		t.Errorf("service.log lost the ordinary lines:\n%s\n%s", got["service.log"], got["config.txt"])
	}
	if !strings.Contains(got["doctor.txt"], "failure(s)") {
		t.Errorf("doctor.txt:\n%s", got["doctor.txt"])
	}
}
