package stockroom

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/joho/godotenv"
)

// The wizard writes the failsafe into the install's .env. Everything else in
// the file must survive, the mode must stay 600, and what godotenv reads back
// must be exactly what was typed.
func TestSetEnvValuesRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	before := "# keep me\nDATABASE_URL=postgresql://x\nADMIN_PASSWORD=old\nADMIN_PASSWORD=dupe\n\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	password := `a$HOME b#c "q" back\slash`
	if err := setEnvValues(path, map[string]string{
		"ADMIN_PASSWORD":       password,
		"ADMIN_STUDENT_NUMBER": "999999",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := godotenv.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got["ADMIN_PASSWORD"] != password || got["ADMIN_STUDENT_NUMBER"] != "999999" || got["DATABASE_URL"] != "postgresql://x" {
		t.Errorf("read back %v", got)
	}
	raw, _ := os.ReadFile(path)
	if string(raw[:10]) != "# keep me\n" {
		t.Errorf("the comment did not survive: %q", raw)
	}
	// Windows has no Unix permission bits: Go reports every writable file as
	// 0666 there, so the mode is only a promise this code can keep elsewhere.
	if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}

	if err := setEnvValues(path, map[string]string{"ADMIN_PASSWORD": "it's"}); err == nil {
		t.Error("a single quote was accepted; it cannot be written literally")
	}
}
