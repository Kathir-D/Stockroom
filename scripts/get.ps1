# Installs Stockroom on Windows, inside Ubuntu 24.04 under WSL 2. Run it from
# an administrator PowerShell:
#
#   irm https://raw.githubusercontent.com/Kathir-D/Stockroom/main/scripts/get.ps1 | iex
#
# or, to pass the failsafe admin without being asked:
#
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/Kathir-D/Stockroom/main/scripts/get.ps1))) -AdminNumber 900100
#
# It installs the distribution if it's missing, turns systemd on inside it,
# runs get.sh there (the same .deb and setup as on Linux), then registers the
# "Stockroom WSL" task that starts the distribution at boot and keeps it
# running. Running it again is the upgrade.
#
# Stockroom-Setup.exe, the graphical installer, runs this same script with
# -Unattended (cmd/winsetup). It has no console to answer on, so that switch
# turns every question into a parameter or a Windows dialog, and reports a
# needed restart as exit code 3.
param(
    # The failsafe admin's student number. Setup asks for the password.
    [string]$AdminNumber = "",
    # A release tag such as v1.2.3. Empty means the latest release.
    [string]$Version = "",
    # A file whose first line is the failsafe admin's password, so setup
    # doesn't ask for it.
    [string]$AdminPasswordFile = "",
    # Ask nothing on the console.
    [switch]$Unattended
)

$ErrorActionPreference = "Stop"
$Distro = "Ubuntu-24.04"
$TaskName = "Stockroom WSL"
$GetSh = "https://raw.githubusercontent.com/Kathir-D/Stockroom/main/scripts/get.sh"

# The password file goes to Ubuntu through a pipe. Windows PowerShell would
# otherwise send it as ASCII and turn every other character into "?".
$OutputEncoding = New-Object System.Text.UTF8Encoding $false

# Steps are announced the way `stockroom setup` announces its own, which is
# what the graphical installer's step list follows.
function Say($msg) { Write-Host "== $msg ==" }
function NeedsRestart {
    Write-Host "Restart Windows, then run this installer again."
    exit 3
}

# 0. Administrator. Installing WSL and registering a boot task both need it.
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Error "Run this from an administrator PowerShell: right-click PowerShell, Run as administrator."
    exit 1
}

# 1. The distribution. A first WSL install may need a reboot before the
#    distribution can start, and Windows says so.
$distros = (wsl.exe --list --quiet 2>$null) -replace "`0", "" | Where-Object { $_ -ne "" }
Say "Ubuntu"
if ($distros -notcontains $Distro) {
    Write-Host "Installing $Distro under WSL"
    wsl.exe --install -d $Distro --no-launch
    if ($LASTEXITCODE -ne 0) {
        if ($Unattended) { NeedsRestart }
        Write-Error "wsl --install failed. If Windows asked for a restart, restart and run this again."
        exit 1
    }
    if ($Unattended) {
        # Nobody is at a console to choose a Linux user name, so the
        # distribution keeps root as its only account and Stockroom gets a
        # system account of its own in step 3. An older WSL registers the
        # distribution only when its launcher first runs.
        wsl.exe -d $Distro --user root --exec /bin/true 2>$null
        if ($LASTEXITCODE -ne 0 -and (Get-Command ubuntu2404.exe -ErrorAction SilentlyContinue)) {
            ubuntu2404.exe install --root
        }
        wsl.exe -d $Distro --user root --exec /bin/true 2>$null
        if ($LASTEXITCODE -ne 0) { NeedsRestart }
    } else {
        # The first start asks for a new Linux user name and password. That
        # account runs Stockroom, the way the teacher's account does on Linux.
        Write-Host "Starting $Distro. Choose a user name and password when it asks, then type exit."
        wsl.exe -d $Distro
        if ($LASTEXITCODE -ne 0) {
            Write-Host "Restart Windows, then run this script again."
            exit 0
        }
    }
} else {
    Write-Host "$Distro is already installed"
}

function InDistro([string]$script) {
    wsl.exe -d $Distro --user root --exec /bin/bash -c $script
    if ($LASTEXITCODE -ne 0) { throw "failed inside $Distro`: $script" }
}

# 2. systemd. The Stockroom and PostgreSQL services need it, and WSL only runs
#    it when wsl.conf asks. Changing it takes a restart of the distribution.
Say "systemd"
wsl.exe -d $Distro --user root --exec /bin/grep -qs "^systemd=true" /etc/wsl.conf
if ($LASTEXITCODE -ne 0) {
    Write-Host "Turning on systemd in $Distro"
    InDistro "printf '\n[boot]\nsystemd=true\n' >> /etc/wsl.conf"
    wsl.exe --shutdown
}

# 3. get.sh, inside the distribution, as root. Setup runs the service as the
#    distribution's default user, the way it runs as the teacher on Linux.
$user = ((wsl.exe -d $Distro --exec /usr/bin/whoami) -replace "`0", "").Trim()
$setupArgs = @("--no-open")
if ($user -eq "root") { $setupArgs += @("--service-user", "stockroom") }
if ($AdminNumber -ne "") { $setupArgs += @("--admin-number", $AdminNumber) }
# With no console there is no /dev/tty for setup to ask on either.
if ($Unattended) { $setupArgs += "--non-interactive" }
$envs = "SUDO_USER=$user"
if ($Version -ne "") { $envs += " STOCKROOM_VERSION=$Version" }
Say "Stockroom"
# The password crosses into Ubuntu on stdin, into a file only root can read,
# and never appears in a command line on either side.
$pwInDistro = "/root/.stockroom-admin-password"
if ($AdminPasswordFile -ne "") {
    Get-Content -Raw -LiteralPath $AdminPasswordFile |
        wsl.exe -d $Distro --user root --exec /bin/sh -c "umask 077; cat > $pwInDistro"
    if ($LASTEXITCODE -ne 0) { throw "could not pass the failsafe admin's password to $Distro" }
    $setupArgs += @("--admin-password-file", $pwInDistro)
}
Write-Host "Installing Stockroom inside $Distro"
try {
    InDistro "apt-get update -q && apt-get install -y -q curl && curl -fsSL $GetSh | env $envs bash -s -- $($setupArgs -join ' ')"
} finally {
    wsl.exe -d $Distro --user root --exec /bin/rm -f $pwInDistro
}

# 4. The boot task. WSL runs only while something holds it open, so the task
#    starts the distribution at boot, whether or not anyone logs in, and a
#    sleeping process keeps it from idling out. systemd then starts
#    PostgreSQL and Stockroom.
Say "Startup task"
Write-Host "Registering the '$TaskName' startup task"
$action = New-ScheduledTaskAction -Execute "wsl.exe" -Argument "-d $Distro --exec /usr/bin/sleep infinity"
$trigger = New-ScheduledTaskTrigger -AtStartup
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
Write-Host "Windows needs your password so the task can run with nobody logged in."
$cred = Get-Credential -UserName "$env:USERDOMAIN\$env:USERNAME" -Message "Your Windows password, for the $TaskName task"
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings `
    -User $cred.UserName -Password $cred.GetNetworkCredential().Password -RunLevel Highest -Force | Out-Null
Start-ScheduledTask -TaskName $TaskName

Write-Host ""
Write-Host "Stockroom is installed. Open http://localhost:8080 and follow the setup wizard."
Write-Host "Backups go to Documents\Stockroom Backups until you choose another folder."
Write-Host "Set this PC never to sleep (Settings, System, Power), then restart it without"
Write-Host "logging in and check that http://localhost:8080 answers once you do log in."
# Said outright for the graphical installer: without it the exit code is
# whatever the last wsl.exe left. Not through `irm | iex`, where exit would
# close the PowerShell window on the lines above.
if ($Unattended) { exit 0 }
