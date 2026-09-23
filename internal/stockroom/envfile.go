package stockroom

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// setEnvValues rewrites KEY=value lines in a .env file in place, appending any
// key that is not there yet, and leaves every other line -- comments,
// DATABASE_URL, blank lines -- byte for byte as it was.
//
// This is the only code that writes the file, and it exists for exactly one
// thing: the failsafe admin, which must survive the database being lost and so
// cannot live in it (CLAUDE.md §7). Everything else that used to be in .env
// moved into app_settings in Phase 7.
//
// The write is a temp file in the same directory and a rename, so a crash
// mid-write leaves the old file rather than half of a new one -- and a
// half-written .env is a server that cannot find its database. Mode 600 is
// kept, because the file holds the database superuser password.
//
// Values are written single-quoted, which godotenv and Docker Compose both
// read literally (verified for `$`, `#`, `"` and `\`). A single quote or a
// line break cannot be represented that way and is refused.
func setEnvValues(path string, values map[string]string) error {
	for k, v := range values {
		if strings.ContainsAny(v, "'\r\n") {
			return fmt.Errorf("%w: %s cannot contain a single quote or a line break", ErrInvalid, k)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("read settings file: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read settings file: %w", err)
	}

	done := map[string]bool{}
	var out []string
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		key := envKey(line)
		if v, ok := values[key]; ok && !done[key] {
			out = append(out, key+"='"+v+"'")
			done[key] = true
			continue
		}
		if _, ok := values[key]; ok {
			// A duplicate of a key already rewritten: dropped, or the later
			// line would win when the file is next read.
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read settings file: %w", err)
	}
	for _, k := range sortedKeys(values) {
		if !done[k] {
			out = append(out, k+"='"+values[k]+"'")
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".env.tmp-*")
	if err != nil {
		return fmt.Errorf("write settings file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return fmt.Errorf("write settings file: %w", err)
	}
	if _, err := tmp.WriteString(strings.Join(out, "\n") + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write settings file: %w", err)
	}
	// On disk before the rename, or a power cut can keep the rename and lose
	// the data: an empty .env, which is a server with no DATABASE_URL.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("write settings file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write settings file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write settings file: %w", err)
	}
	return nil
}

// envKey is the key a KEY=value line sets, or "" for a comment or blank line.
// `export KEY=value` counts, since godotenv accepts it.
func envKey(line string) string {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") {
		return ""
	}
	t = strings.TrimPrefix(t, "export ")
	k, _, ok := strings.Cut(t, "=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(k)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
