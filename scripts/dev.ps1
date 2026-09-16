<#
.SYNOPSIS
  Stockroom's one script, Windows counterpart of scripts/dev.sh.

.DESCRIPTION
  Everything you can do to a working copy from a terminal is a subcommand here.

    .\scripts\dev.ps1              bring the whole dev environment up (same as up)
    .\scripts\dev.ps1 up           ... with -NoDesktop -NoWeb -NoOpen -Ci
    .\scripts\dev.ps1 deps         npm workspace + Go modules + git hooks, nothing else
    .\scripts\dev.ps1 test         the full suite (what .githooks/pre-commit and CI run)
    .\scripts\dev.ps1 stop         stop Supabase and anything left listening
    .\scripts\dev.ps1 status       what is running right now, and what is not

  `up` is idempotent: run it again after a crash and it reuses whatever is
  already healthy instead of starting a second copy that cannot bind its port.

  scripts/dev.sh is the macOS/bash counterpart. bash is not a given on the
  closet PC, which is why these are two files; they must stay in step. Keep the
  long reasoning in dev.sh and the steps identical here.

  UNTESTED on real Windows hardware (CLAUDE.md section 12, Week 4).
#>
param(
    [Parameter(Position = 0)]
    [ValidateSet("up", "deps", "test", "stop", "status", "help")]
    [string]$Command = "up",

    [switch]$NoDesktop,
    [switch]$NoWeb,
    [switch]$NoOpen,
    [switch]$Ci
)

$RepoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $RepoRoot

# ---------------------------------------------------------------------------
# The three numbers the browser has baked in
#
# localOrigins in server/router.go allows :5173 and nothing else, and
# DEFAULT_BASE_URL in packages/ui/src/lib/api/client.ts points at :8080 whatever
# SERVER_ADDR says. Drift on either side reaches the user as "cannot reach the
# Stockroom server", because a blocked CORS preflight and a dead server throw
# the same fetch error. Pinning them here is what keeps that error honest.
# ---------------------------------------------------------------------------
$WebPort = 5173
$StudioPort = 54323
$PgPort = 54322
$WailsPort = 34115
$DefaultServerAddr = "127.0.0.1:8080"
$LogDir = Join-Path $RepoRoot ".run-logs"

$script:ChildProcesses = @()

# Whether *this* invocation started the Supabase stack, and so owns stopping it.
# Cleanup runs on every exit path of `up`, including ones that started nothing,
# so stopping Supabase unconditionally tore down a stack another window was
# using -- and on the reuse-an-existing-server path it pulled the database out
# from under a server it had deliberately left running. Tear down exactly what
# we brought up. Keep in step with STARTED_SUPABASE in dev.sh.
$script:StartedSupabase = $false

function Write-Ok   { param($m) Write-Host "  [OK] $m" }
function Write-Warn { param($m) Write-Host "  [WARN] $m" }
function Write-Err  { param($m) Write-Host "  [ERROR] $m" }

function Get-ServerAddr {
    $addr = $DefaultServerAddr
    if (Test-Path ".env") {
        $match = Select-String -Path ".env" -Pattern '^\s*SERVER_ADDR\s*=\s*(.+)$' -ErrorAction SilentlyContinue |
            Select-Object -Last 1
        if ($match) { $addr = $match.Matches[0].Groups[1].Value.Trim().Trim('"').Trim("'") }
    }
    return $addr
}

function Get-PortPid {
    param([int]$Port)
    $held = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    if ($held) { return $held[0].OwningProcess }
    return $null
}

# A 2xx from a URL. Used for readiness, so a refused connection and a 500 are
# both "not ready" rather than an error to report.
function Test-Http {
    param([string]$Url, [int]$TimeoutSec = 2)
    try {
        Invoke-WebRequest -Uri $Url -TimeoutSec $TimeoutSec -UseBasicParsing | Out-Null
        return $true
    } catch { return $false }
}

function Test-DbUp {
    try { return (Test-NetConnection -ComputerName 127.0.0.1 -Port $PgPort -InformationLevel Quiet -WarningAction SilentlyContinue) }
    catch { return $false }
}

# ===========================================================================
# deps -- one definition of "the dependencies are up to date"
# ===========================================================================
function Invoke-Deps {
    Write-Host "== Dependencies =="

    # Point git at the tracked hooks directory, so .githooks/pre-commit runs the
    # suite before a commit lands (CI.md, "Pre-commit"). Fatal on failure: this
    # step decides whether the suite guards a commit at all, so a warning nobody
    # reads is worse than a stop.
    if ((Get-Command git -ErrorAction SilentlyContinue) -and (Test-Path ".githooks")) {
        if ((git config --get core.hooksPath) -ne ".githooks") {
            git config core.hooksPath .githooks
            if ($LASTEXITCODE -ne 0) {
                Write-Err "could not set core.hooksPath; the pre-commit hook will not run"
                return $false
            }
            Write-Ok "git hooks -> .githooks"
        }
    }

    # A nested node_modules under a workspace shadows the hoisted one, and the
    # failure is silent: that app gets its own Svelte and Vite, components
    # render, and their state stops updating (design-system.md 2.2). This is how
    # desktop-app/frontend ended up on Vite 7 while the root was on Vite 8.
    #
    # "Shadowing" means an installed *package*, so only non-dot entries count.
    # Vite writes its dep-optimisation cache to <app>/node_modules/.vite and
    # .vite-temp, and npm puts workspace script binaries in .bin; all three are
    # normal, and wiping them every run would reinstall the world each time.
    $nestedFound = $false
    foreach ($nested in @(
        "desktop-app/frontend/node_modules",
        "web-app/node_modules",
        "packages/ui/node_modules"
    )) {
        $nestedPath = Join-Path $RepoRoot $nested
        if (Test-Path $nestedPath) {
            $packages = Get-ChildItem -Force $nestedPath | Where-Object { $_.Name -notlike ".*" }
            if ($packages) {
                Write-Host "  Removing a nested install that would shadow the workspace: $nested"
                Remove-Item -Recurse -Force $nestedPath
                $nestedFound = $true
            }
        }
    }

    # npm hoists during install, so a tree built alongside a nested copy can be
    # missing packages that copy was satisfying. Start clean when we find one.
    $useCi = ($Ci -or $nestedFound) -and (Test-Path (Join-Path $RepoRoot "package-lock.json"))
    if ($useCi) {
        Write-Host "  npm ci (exact lockfile) across packages/ui, web-app, desktop-app/frontend..."
        npm ci
    } else {
        # A no-op in a few hundred ms when the tree already matches, and an
        # update when package.json moved, so it is safe to run on every start.
        Write-Host "  npm install across packages/ui, web-app, desktop-app/frontend..."
        npm install
    }
    if ($LASTEXITCODE -ne 0) {
        Write-Err "npm install failed. Nothing below will work; fix it and re-run."
        return $false
    }

    # Go modules are fetched on demand by `go run` and `go test`, but doing it
    # here means a missing module fails now, with a readable message, rather
    # than three lines into the server's startup log.
    Write-Host "  Go modules..."
    go mod download
    if ($LASTEXITCODE -ne 0) {
        Write-Err "go mod download failed. Check your network or GOPROXY."
        return $false
    }

    Write-Ok "Dependencies are up to date"
    return $true
}

# ===========================================================================
# test -- every check CI runs
# ===========================================================================
function Invoke-Test {
    $failed = @()

    if (-not (Invoke-Deps)) {
        Write-Host ""
        Write-Host "FAILED: dependencies could not be installed; nothing else was run."
        return 1
    }

    function Invoke-Suite {
        param([string]$Name, [scriptblock]$Body)
        Write-Host ""
        Write-Host "=== $Name ==="
        & $Body
        if ($LASTEXITCODE -eq 0) {
            Write-Host "--- $Name`: PASS"
        } else {
            Write-Host "--- $Name`: FAIL"
            $script:failed += $Name
        }
    }
    $script:failed = $failed

    # Go's integration tests skip themselves when Postgres is unreachable. Set
    # STOCKROOM_REQUIRE_DB whenever the database is actually up, so a silent
    # skip can never pass for a success.
    $dbUp = Test-DbUp
    if ($dbUp) {
        $env:STOCKROOM_REQUIRE_DB = "1"
    } else {
        Write-Host "warning: Postgres is not reachable on 127.0.0.1:$PgPort."
        Write-Host "         Run '.\scripts\dev.ps1 up' to include the database-backed tests."
    }

    Invoke-Suite "go vet" { go vet ./... }
    Invoke-Suite "go"     { go test ./... -count=1 }

    if ($dbUp) {
        Invoke-Suite "pgtap" { supabase test db }
    } else {
        $script:failed += "pgtap (skipped: database down)"
    }

    # From the repo root, through the workspace. A --prefix install/run gives
    # that app its own copy of Svelte and Vite, which breaks reactivity silently
    # (docs/design/design-system.md 2.2).
    Invoke-Suite "svelte-check (packages/ui + both hosts)" { npm run check }
    Invoke-Suite "vitest (packages/ui + both hosts)"       { npm test }
    Invoke-Suite "build (both hosts)"                      { npm run build }

    Write-Host ""
    if ($script:failed.Count -eq 0) {
        Write-Host "All suites passed."
        return 0
    }
    foreach ($f in $script:failed) { Write-Host "FAILED: $f" }
    return 1
}

# ===========================================================================
# up -- the whole environment
# ===========================================================================
function Cleanup {
    # Nothing of ours ever started: leave without a shutdown banner for a
    # shutdown that is not happening.
    if (($script:ChildProcesses.Count -eq 0) -and (-not $script:StartedSupabase)) { return }

    Write-Host ""
    Write-Host "== Shutting down Stockroom =="
    foreach ($p in $script:ChildProcesses) {
        try {
            if ($p -and -not $p.HasExited) {
                # Stop-Process only kills the direct process, not the child tree
                # (vite/esbuild/node spawned by wails/npm); taskkill /T does.
                taskkill /PID $p.Id /T /F *> $null
            }
        } catch {}
    }
    if ($script:StartedSupabase) {
        Write-Host "Stopping Supabase (data is preserved)..."
        supabase stop *> $null
    } else {
        Write-Host "Leaving Supabase running: it was already up before this run."
    }
    Write-Host "Done. All processes stopped."
}

function Test-Tools {
    Write-Host "== Checking dependencies =="
    $fail = $false

    function Test-Cmd($name, $cmd, $url) {
        if (-not (Get-Command $cmd -ErrorAction SilentlyContinue)) {
            Write-Host "  [MISSING] $name - install from: $url"
            $script:fail = $true
        } else {
            Write-Ok $name
        }
    }
    $script:fail = $false

    Test-Cmd "Go" "go" "https://go.dev/dl/"
    Test-Cmd "Node.js/npm" "npm" "https://nodejs.org/en/download"
    Test-Cmd "Docker" "docker" "https://www.docker.com/products/docker-desktop/"
    Test-Cmd "Supabase CLI" "supabase" "https://github.com/supabase/cli/releases"

    # Wails is the one tool we can install ourselves, so we do rather than
    # bouncing the user to a docs page for a one-line `go install`.
    if (-not (Get-Command wails -ErrorAction SilentlyContinue)) {
        if (Get-Command go -ErrorAction SilentlyContinue) {
            Write-Host "  [MISSING] Wails CLI - installing via 'go install'..."
            go install github.com/wailsapp/wails/v2/cmd/wails@latest
            $goBin = Join-Path (go env GOPATH) "bin"
            $env:Path += ";$goBin"
            if (Get-Command wails -ErrorAction SilentlyContinue) {
                Write-Ok "Wails CLI (installed)"
            } else {
                Write-Host "  [MISSING] Wails CLI install failed. See https://wails.io/docs/gettingstarted/installation"
                $script:fail = $true
            }
        } else {
            Write-Host "  [MISSING] Wails CLI - cannot auto-install without Go. See https://go.dev/dl/"
            $script:fail = $true
        }
    } else {
        Write-Ok "Wails CLI"
    }

    if ($script:fail) {
        Write-Host ""
        Write-Host "Please install the missing dependencies above, then re-run this script."
        return $false
    }
    return $true
}

function Start-DockerDesktop {
    Write-Host ""
    Write-Host "== Starting Docker (required for Supabase) =="
    $dockerReady = $false
    try { docker info *> $null; $dockerReady = ($LASTEXITCODE -eq 0) } catch {}
    if ($dockerReady) { Write-Ok "Docker is ready"; return $true }

    Write-Host "Docker isn't running - starting Docker Desktop..."
    $dockerExe = "C:\Program Files\Docker\Docker\Docker Desktop.exe"
    if (Test-Path $dockerExe) { Start-Process $dockerExe } else { Start-Process "Docker Desktop" }
    Write-Host "Waiting for Docker to become ready..."
    for ($i = 0; $i -lt 60; $i++) {
        try {
            docker info *> $null
            if ($LASTEXITCODE -eq 0) { $dockerReady = $true; break }
        } catch {}
        Start-Sleep -Seconds 2
    }
    if (-not $dockerReady) {
        Write-Err "Docker did not start in time. Please start Docker Desktop manually and re-run."
        return $false
    }
    Write-Ok "Docker is ready"
    return $true
}

# Wait until a URL actually answers. Readiness only; the opening is separate, so
# every tab goes to the browser in one go and lands in one window.
#
# Nothing is opened before its URL responds. A tab pointed at a port nothing is
# listening on yet shows a browser error page as the first thing the user sees,
# which reads exactly like a broken app.
function Wait-ForUrl {
    param([string]$Url, [string]$Label, [int]$Tries = 60)
    for ($i = 0; $i -lt $Tries; $i++) {
        if (Test-Http $Url 2) {
            Write-Ok "$Label ready: $Url"
            return $true
        }
        Start-Sleep -Seconds 1
    }
    Write-Warn "$Label never came up at $Url; not opening a tab."
    return $false
}

# Open every URL as a tab in one browser window. Windows browsers reuse the
# existing window for a URL handed to them, so opening them back to back with no
# window created in between gives tabs rather than windows.
function Open-Tabs {
    param([string[]]$Urls)
    if (-not $Urls -or $Urls.Count -eq 0) { return }
    foreach ($u in $Urls) { Start-Process $u; Start-Sleep -Milliseconds 300 }
    Write-Ok "Opened $($Urls.Count) tab(s): $($Urls -join ' ')"
}

function Invoke-Up {
    if (-not (Test-Tools)) { return 1 }
    if (-not (Start-DockerDesktop)) { return 1 }

    Write-Host ""
    Write-Host "== Starting local Supabase stack =="
    # `supabase start` is idempotent, so it runs either way; the probe first is
    # only about *ownership*. If Postgres was already answering, somebody else
    # started this stack and Ctrl+C here must not take it away from them.
    if (Test-DbUp) {
        Write-Ok "Supabase is already up; leaving it running when this script exits"
        supabase start
    } else {
        supabase start
        $script:StartedSupabase = $true
    }

    Write-Host ""
    if (-not (Invoke-Deps)) {
        Write-Host "  Fix the error above and re-run; nothing below will work without these."
        return 1
    }

    if (-not (Test-Path ".env")) {
        Write-Host ""
        Write-Host "== No .env found - creating one from .env.example =="
        Copy-Item ".env.example" ".env"
        Write-Host "  Edit .env to set ADMIN_STUDENT_NUMBER / ADMIN_PASSWORD (see CLAUDE.md section 9)."
    }

    $serverAddr = Get-ServerAddr
    $healthUrl = "http://$serverAddr/health"
    if ($serverAddr -ne $DefaultServerAddr) {
        Write-Host ""
        Write-Warn ".env sets SERVER_ADDR=$serverAddr, but the frontends are hardcoded to"
        Write-Host "         http://$DefaultServerAddr (DEFAULT_BASE_URL). They will not connect."
    }

    New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

    Write-Host ""
    Write-Host "== Launching Stockroom =="

    # An already-running server would make `go run ./server` fail to bind and
    # exit within a second, leaving the frontends pointed at whatever else holds
    # the port. Reuse it if it answers /health; stop if it does not.
    $serverProc = $null
    $serverPort = [int]($serverAddr -split ":")[-1]
    $heldPid = Get-PortPid $serverPort
    if ($heldPid) {
        if (Test-Http $healthUrl 3) {
            Write-Host "Go API server: already running and healthy (pid $heldPid), reusing it."
        } else {
            Write-Err "Something holds $serverAddr (pid $heldPid) but does not answer /health."
            Write-Host "          Stop it and re-run:  Stop-Process -Id $heldPid"
            return 1
        }
    } else {
        Write-Host "Starting Go API server..."
        $serverProc = Start-Process -FilePath "go" -ArgumentList "run","./server" -WorkingDirectory $RepoRoot -PassThru -NoNewWindow
        $script:ChildProcesses += $serverProc

        # Gate the frontends on a real 200. Starting them first is what produced
        # a UI that loads, scans, and only then says it cannot reach the server.
        Write-Host -NoNewline "Waiting for $healthUrl "
        $ready = $false
        for ($i = 0; $i -lt 90; $i++) {
            if (Test-Http $healthUrl 2) { $ready = $true; break }
            if ($serverProc.HasExited) {
                Write-Host ""
                Write-Err "The Go server exited before it became healthy. See the output above."
                return 1
            }
            Write-Host -NoNewline "."
            Start-Sleep -Seconds 1
        }
        Write-Host ""
        if (-not $ready) {
            Write-Err "The Go server never answered /health. See the output above."
            return 1
        }
        Write-Ok "API server healthy"
    }

    $desktopProc = $null
    if (-not $NoDesktop) {
        Write-Host "Starting desktop app (Wails)..."
        $desktopProc = Start-Process -FilePath "wails" -ArgumentList "dev" -WorkingDirectory (Join-Path $RepoRoot "desktop-app") -PassThru -NoNewWindow
        $script:ChildProcesses += $desktopProc
    }

    $webProc = $null
    if (-not $NoWeb) {
        # web-app/package.json pins `vite --port 5173 --strictPort`. Without
        # strictPort Vite silently moves to 5174 when 5173 is taken, and 5174 is
        # not an allowed origin, so every request fails CORS and the UI blames
        # an unreachable server. It lives in package.json, not here, so a bare
        # `npm run dev:web` gets it too.
        $webHolder = Get-PortPid $WebPort
        if ($webHolder) {
            Write-Warn "Port $WebPort is held by pid $webHolder; the web app cannot use it."
            Write-Host "         Only the web app is affected. Free it and re-run."
        }
        Write-Host "Starting web app (http://localhost:$WebPort)..."
        $webProc = Start-Process -FilePath "npm" -ArgumentList "run","dev:web" -WorkingDirectory $RepoRoot -PassThru -NoNewWindow
        $script:ChildProcesses += $webProc
    }

    if (-not $NoOpen) {
        Write-Host ""
        Write-Host "== Opening tabs =="
        $tabs = @()
        if ((-not $NoWeb) -and (Wait-ForUrl "http://localhost:$WebPort" "Web app")) {
            $tabs += "http://localhost:$WebPort"
        }
        if (Wait-ForUrl "http://127.0.0.1:$StudioPort" "Supabase Studio" 20) {
            $tabs += "http://127.0.0.1:$StudioPort"
        }
        Open-Tabs $tabs
    }

    Write-Host ""
    Write-Host "Stockroom is running."
    Write-Host "  API server:  $healthUrl  (healthy)"
    if (-not $NoDesktop) { Write-Host "  Desktop app: a native window should open automatically" }
    if (-not $NoWeb)     { Write-Host "  Web app:     http://localhost:$WebPort" }
    Write-Host "  Studio:      http://127.0.0.1:$StudioPort"
    Write-Host ""
    Write-Host "  Sign in with 123456 / password (admin) or 234567 / password (student)."
    Write-Host ""
    Write-Host "Press Ctrl+C to stop everything and close all processes."

    while ($true) {
        Start-Sleep -Seconds 1
        $serverDone  = ($null -eq $serverProc)  -or $serverProc.HasExited
        $desktopDone = ($null -eq $desktopProc) -or $desktopProc.HasExited
        $webDone     = ($null -eq $webProc)     -or $webProc.HasExited
        if ($serverDone -and $desktopDone -and $webDone) { break }
    }
    return 0
}

# ===========================================================================
# stop / status
# ===========================================================================
function Invoke-Stop {
    Write-Host "== Stopping Stockroom =="
    $serverPort = [int]((Get-ServerAddr) -split ":")[-1]
    foreach ($port in @($serverPort, $WebPort, $WailsPort)) {
        $heldPid = Get-PortPid $port
        if ($heldPid) {
            Write-Host "  Killing pid $heldPid on port $port"
            taskkill /PID $heldPid /T /F *> $null
        }
    }
    Write-Host "  Stopping Supabase (data is preserved)..."
    supabase stop *> $null
    Write-Ok "Stopped."
    return 0
}

function Invoke-Status {
    $serverAddr = Get-ServerAddr
    $healthUrl = "http://$serverAddr/health"
    Write-Host "== Stockroom status =="

    $dockerOk = $false
    try { docker info *> $null; $dockerOk = ($LASTEXITCODE -eq 0) } catch {}
    if ($dockerOk) { Write-Ok "Docker running" } else { Write-Warn "Docker not running" }

    if (Test-DbUp) { Write-Ok "Postgres on $PgPort" } else { Write-Warn "Postgres not reachable on $PgPort" }

    $serverPort = [int]($serverAddr -split ":")[-1]
    $heldPid = Get-PortPid $serverPort
    if (-not $heldPid) {
        Write-Warn "No API server on $serverAddr"
    } elseif (Test-Http $healthUrl 3) {
        Write-Ok "API server healthy on $serverAddr (pid $heldPid)"
    } else {
        Write-Warn "pid $heldPid holds $serverAddr but /health is not OK (Postgres down?)"
    }

    $heldPid = Get-PortPid $WebPort
    if ($heldPid) { Write-Ok "Web app on $WebPort (pid $heldPid)" } else { Write-Warn "No web app on $WebPort" }

    if (Test-Http "http://127.0.0.1:$StudioPort" 3) { Write-Ok "Studio on $StudioPort" } else { Write-Warn "No Studio on $StudioPort" }
    return 0
}

# ===========================================================================
# Dispatch
# ===========================================================================
$code = 0
try {
    switch ($Command) {
        "up"     { $code = Invoke-Up }
        "deps"   { $code = if (Invoke-Deps) { 0 } else { 1 } }
        "test"   { $code = Invoke-Test }
        "stop"   { $code = Invoke-Stop }
        "status" { $code = Invoke-Status }
        "help"   { Get-Help $PSCommandPath -Detailed; $code = 0 }
    }
}
finally {
    # Only `up` starts long-lived children; the others have nothing to tear down
    # and must not stop Supabase out from under a running environment.
    if ($Command -eq "up") { Cleanup }
}
exit $code
