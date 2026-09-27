package stockroom

import "sync"

// The binary's version, stamped by the linker into server/main.go and handed
// here at startup so the backup manifest can carry it.
var buildVersion = struct {
	mu sync.Mutex
	v  string
}{v: "dev"}

// SetVersion records the running binary's version.
func SetVersion(v string) {
	buildVersion.mu.Lock()
	defer buildVersion.mu.Unlock()
	if v != "" {
		buildVersion.v = v
	}
}

// Version is what SetVersion recorded, "dev" before it runs.
func Version() string {
	buildVersion.mu.Lock()
	defer buildVersion.mu.Unlock()
	return buildVersion.v
}
