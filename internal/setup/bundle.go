package setup

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"time"

	"stockroom/internal/stockroom"
)

// A support bundle is one zip somebody can attach to an issue: what doctor
// says, the version, the machine, the config and the service's recent log,
// with every secret removed. It holds no database rows, so no student's name
// or number leaves the machine through it.

// bundleLogLines is how much of the service log the bundle keeps.
const bundleLogLines = 2000

// BundleInfo is what the command knows that setup doesn't.
type BundleInfo struct {
	// Version is `stockroom version`'s text.
	Version string
	// Out is the zip to write; empty picks a dated name in the working
	// directory.
	Out string
}

// SupportBundle writes the bundle and returns its path.
func SupportBundle(ctx context.Context, e *Env, configPath string, info BundleInfo) (string, error) {
	out := info.Out
	if out == "" {
		out = "stockroom-support-" + time.Now().Format("20060102-150405") + ".zip"
	}

	files := []struct{ name, body string }{
		{"README.txt", bundleReadme},
		{"version.txt", info.Version},
		{"system.txt", describeSystem(e)},
	}

	// Doctor writes to e.Out; run it against a copy that writes here.
	var doc bytes.Buffer
	de := *e
	de.Out = &doc
	fails := Doctor(ctx, &de, configPath)
	fmt.Fprintf(&doc, "\n%d failure(s)\n", fails)
	files = append(files, struct{ name, body string }{"doctor.txt", doc.String()})

	// A config that doesn't load is when a bundle is most needed, so the
	// file is read on its own, from --config or the system path.
	cfg, cfgErr := stockroom.LoadConfigFrom(configPath)
	cfgFile := configPath
	switch {
	case cfgErr == nil && cfg.EnvPath != "":
		cfgFile = cfg.EnvPath
	case cfgFile == "":
		cfgFile = e.Paths.ConfigFile
	}
	cfgText := ""
	if cfgErr != nil {
		cfgText = "# the config does not load: " + cfgErr.Error() + "\n"
	}
	if raw, err := os.ReadFile(cfgFile); err != nil {
		cfgText += "could not read " + cfgFile + ": " + err.Error() + "\n"
	} else {
		cfgText += "# " + cfgFile + "\n" + redactEnv(string(raw))
	}
	files = append(files, struct{ name, body string }{"config.txt", cfgText})

	// A working copy has no service; anything else might.
	logText := "no service log: this is a working copy, not an install. The server's output is in the terminal that runs it."
	if cfgErr != nil || cfg.Installed() {
		logText = newService(e).recentLog(ctx, bundleLogLines)
	}
	files = append(files, struct{ name, body string }{"service.log", logText})

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	now := time.Now()
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return "", err
		}
		if _, err := w.Write([]byte(RedactSecrets(f.body))); err != nil {
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		return "", err
	}
	// Under sudo, hand the file to whoever ran it, so they can attach it.
	if e.Root && e.SudoUser != "" && e.LookupUser != nil && e.Chown != nil {
		if u, err := e.LookupUser(e.SudoUser); err == nil {
			uid, _ := strconv.Atoi(u.Uid)
			gid, _ := strconv.Atoi(u.Gid)
			_ = e.Chown(out, uid, gid)
		}
	}
	return out, nil
}

const bundleReadme = `Stockroom support bundle

doctor.txt   what "stockroom doctor" reported
version.txt  the version, commit and schema
system.txt   the operating system and how the service runs
config.txt   the config file, with passwords, tokens and the admin's number removed
service.log  the service's recent output, with secrets removed

No records from the database are in here: no names, no student numbers, no
checkouts. From the database it holds only what doctor.txt says about it: the
PostgreSQL version, whether the schema is current, the folders chosen in the
admin panel and how long ago the last backup succeeded.
Read it before you send it anywhere.
`

func describeSystem(e *Env) string {
	var b strings.Builder
	fmt.Fprintf(&b, "os       %s\n", e.GOOS)
	fmt.Fprintf(&b, "wsl      %t\n", e.WSL)
	fmt.Fprintf(&b, "systemd  %t\n", e.Systemd)
	fmt.Fprintf(&b, "root     %t\n", e.Root)
	if u, err := user.Current(); err == nil {
		fmt.Fprintf(&b, "user     %s\n", u.Username)
	}
	if host, err := os.Hostname(); err == nil {
		fmt.Fprintf(&b, "host     %s\n", host)
	}
	fmt.Fprintf(&b, "binary   %s\n", e.Paths.Binary)
	fmt.Fprintf(&b, "config   %s\n", e.Paths.ConfigFile)
	fmt.Fprintf(&b, "data     %s\n", e.Paths.DataDir)
	fmt.Fprintf(&b, "written  %s\n", time.Now().Format(time.RFC3339))
	return b.String()
}

// secretKey matches the config keys whose values never leave the machine.
// ADMIN_STUDENT_NUMBER is one: a student number is a working scan login.
var secretKey = regexp.MustCompile(`(?i)(PASSWORD|PASSPHRASE|SECRET|TOKEN|STUDENT_NUMBER|_KEY$)`)

// redactEnv blanks the value of every secret key in a .env file, keeping the
// key so the reader can see it was set.
func redactEnv(text string) string {
	var out strings.Builder
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		key, value, ok := strings.Cut(line, "=")
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(key), "export "))
		if ok && !strings.HasPrefix(strings.TrimSpace(line), "#") && secretKey.MatchString(trimmed) && strings.TrimSpace(value) != "" {
			line = key + "=[removed]"
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

var secretPatterns = []struct {
	re   *regexp.Regexp
	with string
}{
	// A password inside a connection URL.
	{regexp.MustCompile(`(://[^:/@\s]+):[^@\s]+@`), "$1:[removed]@"},
	// password=..., "token": "...", and the like.
	{regexp.MustCompile(`(?i)((?:password|passphrase|secret|token)[a-z_]*["']?\s*[:=]\s*["']?)[^\s"',&}]+`), "${1}[removed]"},
	// GitHub tokens.
	{regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`), "[removed]"},
	// Google OAuth access and refresh tokens.
	{regexp.MustCompile(`\bya29\.[A-Za-z0-9._-]+|\b1//[A-Za-z0-9._-]{20,}`), "[removed]"},
	// A bearer token in a logged header.
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`), "${1}[removed]"},
}

// RedactSecrets removes anything that looks like a credential from text.
func RedactSecrets(text string) string {
	for _, p := range secretPatterns {
		text = p.re.ReplaceAllString(text, p.with)
	}
	return text
}
