#!/usr/bin/env bash
# Double-click launcher for macOS. Finder runs .command files in a new Terminal
# window automatically. This just hands off to dev.sh so there is a single
# source of truth for the actual startup logic.
cd "$(dirname "${BASH_SOURCE[0]}")"
./dev.sh up
