package setup

import (
	"fmt"
	"path/filepath"
	"strings"
)

// linuxConfigFile is the config the packaged unit names. setup --config can
// choose another, and the drop-in then points the unit at it.
const linuxConfigFile = "/etc/stockroom/stockroom.env"

// systemdUnit is the service unit. The package ships it rendered with
// /usr/bin/stockroom as packaging/linux/stockroom.service, and a test keeps
// that file equal to this. `stockroom service install` writes it to
// /etc/systemd/system only for a binary that didn't come from the package.
//
// ProtectSystem=full rather than strict, and no ProtectHome: an admin can pick
// any backup folder in the panel, the teacher's Documents and USB drives under
// /media included, and those two options would make the backup fail on
// exactly the folders the picker offers. The service user comes from a
// drop-in setup writes, so the unit has no User= line.
func systemdUnit(binary, config string) string {
	return fmt.Sprintf(`[Unit]
Description=Stockroom equipment checkout
Documentation=https://github.com/Kathir-D/Stockroom
After=network.target postgresql.service
Wants=postgresql.service

[Service]
Type=simple
ExecStart=%s serve --config %s
Restart=on-failure
RestartSec=2
ProtectSystem=full
ReadWritePaths=%s
PrivateTmp=yes
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
`, binary, config, filepath.Dir(config))
}

// userDropIn is /etc/systemd/system/stockroom.service.d/user.conf: the
// account the service runs as and, when config is not "", the config the
// packaged unit should read instead of its own. The empty ExecStart= clears
// the unit's line before replacing it.
func userDropIn(user, group, binary, config string) string {
	s := fmt.Sprintf("# Written by stockroom setup. The account the service runs as.\n[Service]\nUser=%s\nGroup=%s\n", user, group)
	if config != "" {
		s += fmt.Sprintf("# The config chosen with --config.\nExecStart=\nExecStart=%s serve --config %s\nReadWritePaths=%s\n",
			binary, config, filepath.Dir(config))
	}
	return s
}

// checkConfigPath makes a --config path absolute and refuses one a unit's
// ExecStart or a shell would split or expand.
func checkConfigPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(abs, " \t\r\n\"'\\$%;`") {
		return "", fmt.Errorf("--config %q: use a path without spaces, quotes or any of $ %% ; ` \\", path)
	}
	return abs, nil
}

// launchDaemon renders a LaunchDaemon plist. RunAtLoad and KeepAlive start it
// at boot with nobody logged in and restart it if it dies; UserName keeps it
// off root, which Postgres refuses anyway.
func launchDaemon(label, user string, args []string, logPath string, env map[string]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	fmt.Fprintf(&b, "  <key>Label</key>\n  <string>%s</string>\n", xmlEscape(label))
	fmt.Fprintf(&b, "  <key>UserName</key>\n  <string>%s</string>\n", xmlEscape(user))
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, a := range args {
		fmt.Fprintf(&b, "    <string>%s</string>\n", xmlEscape(a))
	}
	b.WriteString("  </array>\n")
	if len(env) > 0 {
		b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
		for _, k := range sortedKeys(env) {
			fmt.Fprintf(&b, "    <key>%s</key>\n    <string>%s</string>\n", xmlEscape(k), xmlEscape(env[k]))
		}
		b.WriteString("  </dict>\n")
	}
	b.WriteString("  <key>RunAtLoad</key>\n  <true/>\n  <key>KeepAlive</key>\n  <true/>\n")
	fmt.Fprintf(&b, "  <key>StandardOutPath</key>\n  <string>%s</string>\n", xmlEscape(logPath))
	fmt.Fprintf(&b, "  <key>StandardErrorPath</key>\n  <string>%s</string>\n", xmlEscape(logPath))
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
