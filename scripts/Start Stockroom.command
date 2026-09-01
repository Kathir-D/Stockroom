#!/usr/bin/env bash
# Double-click launcher for macOS. Finder runs .command files in a new
# Terminal window automatically. This just hands off to start-mac.sh so
# there's a single source of truth for the actual startup logic.
cd "$(dirname "${BASH_SOURCE[0]}")"
./start-mac.sh
