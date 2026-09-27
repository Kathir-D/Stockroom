package stockroom

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// rcloneBinary is the rclone every Drive call runs. It starts as the bare
// name, which is what the tests' fake rclone on PATH relies on, and serve
// replaces it once at startup through ResolveRclone, because a service
// manager's PATH is short and may not reach rclone at all.
var rcloneBinary = "rclone"

// MinRcloneVersion is the oldest rclone Stockroom supports. Debian 12 and
// Ubuntu 24.04 both package 1.60.1, and every command Stockroom runs was
// checked against it (docs/decisions.md, 2026-09-27).
const MinRcloneVersion = "1.60.0"

// rcloneFallbacks are the places rclone lives off PATH: the Debian package,
// rclone.org's install script, and Homebrew on Apple silicon and Intel.
var rcloneFallbacks = []string{"/usr/bin/rclone", "/usr/local/bin/rclone", "/opt/homebrew/bin/rclone"}

// RcloneInfo is what ResolveRclone found. The backup screen and doctor show it.
type RcloneInfo struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Found   bool   `json:"found"`
	// TooOld is a version below MinRcloneVersion. Stockroom still runs it;
	// the warning says which commands may fail.
	TooOld     bool   `json:"too_old"`
	MinVersion string `json:"min_version"`
	Error      string `json:"error,omitempty"`
}

var resolvedRclone struct {
	mu   sync.Mutex
	info *RcloneInfo
}

// ResolveRclone finds rclone and records it: explicit (RCLONE_BINARY) when
// set, then PATH, then rcloneFallbacks. It sets the binary every Drive call
// runs. Missing is not an error here, because Drive is optional; the result
// says so and the screens show it.
func ResolveRclone(ctx context.Context, explicit string) RcloneInfo {
	info := RcloneInfo{MinVersion: MinRcloneVersion}
	switch {
	case explicit != "":
		if isExecutable(explicit) {
			info.Path = explicit
		} else {
			info.Error = fmt.Sprintf("RCLONE_BINARY is %q, which is not an executable file", explicit)
		}
	default:
		if p, err := exec.LookPath("rclone"); err == nil {
			info.Path = p
		} else {
			for _, p := range rcloneFallbacks {
				if isExecutable(p) {
					info.Path = p
					break
				}
			}
		}
	}
	if info.Path != "" {
		info.Found = true
		v, err := rcloneVersion(ctx, info.Path)
		if err != nil {
			info.Error = err.Error()
		} else {
			info.Version = v
			info.TooOld = versionLess(v, MinRcloneVersion)
		}
		rcloneBinary = info.Path
	} else if info.Error == "" {
		info.Error = "rclone is not installed. Run `sudo apt install rclone` (Linux) or `brew install rclone` (macOS)"
	}

	resolvedRclone.mu.Lock()
	resolvedRclone.info = &info
	resolvedRclone.mu.Unlock()
	return info
}

// Rclone returns what ResolveRclone recorded, or nil when it never ran
// (tests, and every command but serve and doctor).
func Rclone() *RcloneInfo {
	resolvedRclone.mu.Lock()
	defer resolvedRclone.mu.Unlock()
	if resolvedRclone.info == nil {
		return nil
	}
	info := *resolvedRclone.info
	return &info
}

var rcloneVersionLine = regexp.MustCompile(`rclone v?([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)

// rcloneVersion runs `rclone version` and returns the number from its first
// line, "rclone v1.60.1" giving "1.60.1".
func rcloneVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return "", fmt.Errorf("%s version: %w", path, err)
	}
	m := rcloneVersionLine.FindStringSubmatch(string(out))
	if m == nil {
		return "", fmt.Errorf("%s version printed no version number", path)
	}
	return m[1], nil
}

// versionLess compares dotted version numbers. Missing parts count as zero,
// and a suffix such as "-DEV" on a part is ignored.
func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(pa), len(pb)) {
		x, y := versionPart(pa, i), versionPart(pb, i)
		if x != y {
			return x < y
		}
	}
	return false
}

func versionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	digits := strings.TrimLeftFunc(parts[i], func(r rune) bool { return r == 'v' })
	if j := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }); j >= 0 {
		digits = digits[:j]
	}
	n, _ := strconv.Atoi(digits)
	return n
}
