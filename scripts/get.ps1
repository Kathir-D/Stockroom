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
param(
    # The failsafe admin's student number. Setup asks for the password.
    [string]$AdminNumber = "",
    # A release tag such as v1.2.3. Empty means the latest release.
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"
$Distro = "Ubuntu-24.04"
$TaskName = "Stockroom WSL"
$GetSh = "https://raw.githubusercontent.com/Kathir-D/Stockroom/main/scripts/get.sh"

function Say($msg) { Write-Host "==> $msg" }

# 0. Administrator. Installing WSL and registering a boot task both need it.
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Error "Run this from an administrator PowerShell: right-click PowerShell, Run as administrator."
    exit 1
}

# 1. The distribution. A first WSL install may need a reboot before the
#    distribution can start, and Windows says so.
$distros = (wsl.exe --list --quiet 2>$null) -replace "`0", "" | Where-Object { $_ -ne "" }
if ($distros -notcontains $Distro) {
    Say "Installing $Distro under WSL"
    wsl.exe --install -d $Distro --no-launch
    if ($LASTEXITCODE -ne 0) {
        Write-Error "wsl --install failed. If Windows asked for a restart, restart and run this again."
        exit 1
    }
    # The first start asks for a new Linux user name and password. That
    # account runs Stockroom, the way the teacher's account does on Linux.
    Say "Starting $Distro. Choose a user name and password when it asks, then type exit."
    wsl.exe -d $Distro
    if ($LASTEXITCODE -ne 0) {
        Write-Host "Restart Windows, then run this script again."
        exit 0
    }
}

function InDistro([string]$script) {
    wsl.exe -d $Distro --user root --exec /bin/bash -c $script
    if ($LASTEXITCODE -ne 0) { throw "failed inside $Distro`: $script" }
}

# 2. systemd. The Stockroom and PostgreSQL services need it, and WSL only runs
#    it when wsl.conf asks. Changing it takes a restart of the distribution.
wsl.exe -d $Distro --user root --exec /bin/grep -qs "^systemd=true" /etc/wsl.conf
if ($LASTEXITCODE -ne 0) {
    Say "Turning on systemd in $Distro"
    InDistro "printf '\n[boot]\nsystemd=true\n' >> /etc/wsl.conf"
    wsl.exe --shutdown
}

# 3. get.sh, inside the distribution, as root. Setup runs the service as the
#    distribution's default user, the way it runs as the teacher on Linux.
$user = ((wsl.exe -d $Distro --exec /usr/bin/whoami) -replace "`0", "").Trim()
$setupArgs = @("--no-open")
if ($user -eq "root") { $setupArgs += @("--service-user", "stockroom") }
if ($AdminNumber -ne "") { $setupArgs += @("--admin-number", $AdminNumber) }
$envs = "SUDO_USER=$user"
if ($Version -ne "") { $envs += " STOCKROOM_VERSION=$Version" }
Say "Installing Stockroom inside $Distro"
InDistro "apt-get update -q && apt-get install -y -q curl && curl -fsSL $GetSh | env $envs bash -s -- $($setupArgs -join ' ')"

# 4. The boot task. WSL runs only while something holds it open, so the task
#    starts the distribution at boot, whether or not anyone logs in, and a
#    sleeping process keeps it from idling out. systemd then starts
#    PostgreSQL and Stockroom.
Say "Registering the '$TaskName' startup task"
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
