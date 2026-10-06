// Package scripts embeds the one install script another program runs:
// Stockroom-Setup.exe (cmd/winsetup) carries get.ps1 and runs it elevated, so
// the graphical Windows installer and the PowerShell one-liner are the same
// install.
package scripts

import _ "embed"

//go:embed get.ps1
var GetPS1 []byte
