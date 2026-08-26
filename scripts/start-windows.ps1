# One-step start for Stockroom on Windows: verifies dependencies, starts
# Docker + the local Supabase stack, installs frontend packages, then
# launches the desktop app and web app. Ctrl+C shuts everything down cleanly.

$RepoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $RepoRoot

$script:ChildProcesses = @()

function Cleanup {
    Write-Host ""
    Write-Host "== Shutting down Stockroom =="
    foreach ($p in $script:ChildProcesses) {
        try {
            if ($p -and -not $p.HasExited) {
                # Stop-Process only kills the direct process, not the child tree
                # (vite/esbuild/node spawned by wails/npm) - taskkill /T does a tree kill.
                taskkill /PID $p.Id /T /F *> $null
            }
        } catch {}
    }
    Write-Host "Stopping Supabase (data is preserved)..."
    supabase stop *> $null
    Write-Host "Done. All processes stopped."
}

try {
    Write-Host "== Checking dependencies =="
    $fail = $false

    function Test-Cmd($name, $cmd, $url) {
        if (-not (Get-Command $cmd -ErrorAction SilentlyContinue)) {
            Write-Host "  [MISSING] $name - install from: $url"
            $script:fail = $true
        } else {
            Write-Host "  [OK] $name"
        }
    }

    Test-Cmd "Go" "go" "https://go.dev/dl/"
    Test-Cmd "Node.js/npm" "npm" "https://nodejs.org/en/download"
    Test-Cmd "Docker" "docker" "https://www.docker.com/products/docker-desktop/"
    Test-Cmd "Supabase CLI" "supabase" "https://github.com/supabase/cli/releases"

    if (-not (Get-Command wails -ErrorAction SilentlyContinue)) {
        if (Get-Command go -ErrorAction SilentlyContinue) {
            Write-Host "  [MISSING] Wails CLI - installing via 'go install'..."
            go install github.com/wailsapp/wails/v2/cmd/wails@latest
            $goBin = Join-Path (go env GOPATH) "bin"
            $env:Path += ";$goBin"
            if (Get-Command wails -ErrorAction SilentlyContinue) {
                Write-Host "  [OK] Wails CLI (installed)"
            } else {
                Write-Host "  [MISSING] Wails CLI install failed. See https://wails.io/docs/gettingstarted/installation"
                $fail = $true
            }
        } else {
            Write-Host "  [MISSING] Wails CLI - cannot auto-install without Go. See https://go.dev/dl/"
            $fail = $true
        }
    } else {
        Write-Host "  [OK] Wails CLI"
    }

    if ($fail) {
        Write-Host ""
        Write-Host "Please install the missing dependencies above, then re-run this script."
        exit 1
    }

    Write-Host ""
    Write-Host "== Starting Docker (required for Supabase) =="
    $dockerReady = $false
    try { docker info *> $null; $dockerReady = $true } catch {}
    if (-not $dockerReady) {
        Write-Host "Docker isn't running - starting Docker Desktop..."
        $dockerExe = "C:\Program Files\Docker\Docker\Docker Desktop.exe"
        if (Test-Path $dockerExe) {
            Start-Process $dockerExe
        } else {
            Start-Process "Docker Desktop"
        }
        Write-Host "Waiting for Docker to become ready..."
        for ($i = 0; $i -lt 60; $i++) {
            try {
                docker info *> $null
                $dockerReady = $true
                break
            } catch {}
            Start-Sleep -Seconds 2
        }
        if (-not $dockerReady) {
            Write-Host "  [ERROR] Docker did not start in time. Please start Docker Desktop manually and re-run."
            exit 1
        }
    }
    Write-Host "  [OK] Docker is ready"

    Write-Host ""
    Write-Host "== Starting local Supabase stack =="
    supabase start

    Write-Host ""
    Write-Host "== Installing frontend dependencies (if needed) =="
    Push-Location "desktop-app/frontend"; npm install; Pop-Location
    Push-Location "web-app"; npm install; Pop-Location

    Write-Host ""
    Write-Host "== Launching Stockroom =="
    Write-Host "Starting desktop app (Wails)..."
    $desktopProc = Start-Process -FilePath "wails" -ArgumentList "dev" -WorkingDirectory (Join-Path $RepoRoot "desktop-app") -PassThru -NoNewWindow
    $script:ChildProcesses += $desktopProc

    Write-Host "Starting web app (localhost)..."
    $webProc = Start-Process -FilePath "npm" -ArgumentList "run","dev" -WorkingDirectory (Join-Path $RepoRoot "web-app") -PassThru -NoNewWindow
    $script:ChildProcesses += $webProc

    Write-Host ""
    Write-Host "Stockroom is running."
    Write-Host "  Desktop app: a native window should open automatically"
    Write-Host "  Web app:     see the URL printed above (usually http://localhost:5173)"
    Write-Host "  Studio:      http://127.0.0.1:54323"
    Write-Host ""
    Write-Host "Press Ctrl+C to stop everything and close all processes."

    while ($true) {
        Start-Sleep -Seconds 1
        if ($desktopProc.HasExited -and $webProc.HasExited) { break }
    }
}
finally {
    Cleanup
}
