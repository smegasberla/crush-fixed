# Install or remove this build of Crush on Windows.
#
# Builds the binary from this checkout (never downloads one), installs it under a
# name of your choosing, and verifies the result. Removal is the same script with
# -Action uninstall.
#
# Sessions, config and project state live in files this script never writes, so
# the installed binary reads the same sessions as any other Crush build. Purging
# that state is a separate, explicit switch.
#
# Usage:
#   .\scripts\install.ps1                          install as "crush-fixed"
#   .\scripts\install.ps1 -As crush                install as "crush"
#   .\scripts\install.ps1 -Prefix C:\bin           install into C:\bin
#   .\scripts\install.ps1 -DryRun                  print what would happen
#   .\scripts\install.ps1 -Action uninstall        remove the installed binary
#   .\scripts\install.ps1 -Action uninstall -Purge remove local state too

[CmdletBinding()]
param(
    [ValidateSet('install', 'uninstall')]
    [string]$Action = 'install',
    [string]$As = 'crush-fixed',
    [string]$Prefix = "$env:LOCALAPPDATA\Programs\crush-fixed",
    [switch]$DryRun,
    [switch]$Purge
)

$ErrorActionPreference = 'Stop'

function Write-Note([string]$Message) { Write-Host $Message }

function Invoke-Step {
    param([scriptblock]$Step, [string]$Description)
    if ($DryRun) {
        Write-Note "would run: $Description"
        return
    }
    & $Step
}

if ($As -match '[\\/:*?"<>|]') {
    throw "-As must be a plain file name, not a path"
}

# Resolve the checkout to build from.
#
# Run from a checkout, that is the checkout. Invoked from a URL through `irm`
# there is no script file, so the repository is cloned to a temporary directory
# instead: the build always comes from source, never from a downloaded binary.
$Reposlug = 'smegasberla/crush-fixed'
$RepoUrl = "https://github.com/$Reposlug.git"
$RepoRef = if ($env:CRUSH_INSTALL_REF) { $env:CRUSH_INSTALL_REF } else { 'main' }

$CloneDir = $null
function Resolve-Source {
    $scriptDir = $null
    if ($PSCommandPath) { $scriptDir = Split-Path -Parent $PSCommandPath }
    if ($scriptDir) {
        $candidate = Split-Path -Parent $scriptDir
        if (Test-Path (Join-Path $candidate 'go.mod')) { return $candidate }
    }
    if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
        throw 'git is required to fetch the source when running this script from a URL'
    }
    $script:CloneDir = Join-Path ([System.IO.Path]::GetTempPath()) ("crush-src-" + [guid]::NewGuid().ToString('N'))
    Write-Note "fetching $Reposlug ($RepoRef)"
    & git clone --depth 1 --branch $RepoRef $RepoUrl (Join-Path $CloneDir 'src') | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "could not clone $RepoUrl" }
    return (Join-Path $CloneDir 'src')
}

$RepoDir = $null
$Target = Join-Path $Prefix "$As.exe"

# The directories a Crush build keeps user data in. They are shared with any
# other Crush install, which is why purging them is opt-in.
function Get-StateDirs {
    @(
        (Join-Path $env:APPDATA 'crush'),
        (Join-Path $env:LOCALAPPDATA 'crush'),
        (Join-Path $env:USERPROFILE '.config\crush')
    ) | Where-Object { Test-Path $_ }
}

function Remove-InstalledBinary {
    if (-not (Test-Path $Target)) {
        Write-Note "nothing to remove at $Target"
        return
    }
    Invoke-Step -Description "remove $Target" -Step { Remove-Item -Force $Target }
    Write-Note "removed $Target"
}

function Remove-CrushState {
    $dirs = Get-StateDirs
    if (-not $dirs) {
        Write-Note "no local state found; nothing to purge"
        return
    }
    Write-Note 'These directories hold sessions, config and history, and are shared'
    Write-Note 'with any other Crush build on this machine:'
    $dirs | ForEach-Object { Write-Note "  $_" }
    if (-not $DryRun) {
        # Invoked through `irm | iex` there is no interactive host, so a purge
        # is refused rather than assumed: deleting sessions is not something a
        # non-interactive run should decide.
        if (-not [Environment]::UserInteractive) {
            Write-Note 'no terminal to confirm on; kept state. Run the script interactively to purge it.'
            return
        }
        $answer = Read-Host 'Delete them? This cannot be undone. [y/N]'
        if ($answer -notmatch '^(y|yes)$') {
            Write-Note 'kept state; nothing was deleted'
            return
        }
    }
    foreach ($dir in $dirs) {
        Invoke-Step -Description "delete $dir" -Step { Remove-Item -Recurse -Force $dir }
        if (-not $DryRun) { Write-Note "deleted $dir" }
    }
}

if ($Action -eq 'uninstall') {
    Remove-InstalledBinary
    if ($Purge) { Remove-CrushState }
    Write-Note "uninstalled $As"
    exit 0
}

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go is required to build this from source (https://go.dev/dl/)'
}
$RepoDir = Resolve-Source

$Version = 'fixed'
$Commit = 'unknown'
try {
    $Version = (& git -C $RepoDir describe --tags --always --dirty 2>$null)
    if (-not $Version) { $Version = 'fixed' }
    $Commit = (& git -C $RepoDir rev-parse --short HEAD 2>$null)
    if (-not $Commit) { $Commit = 'unknown' }
} catch {
    # git is optional: a source archive installs fine without it.
}

Write-Note "building $As $Version ($Commit) from $RepoDir"

$BuildDir = Join-Path ([System.IO.Path]::GetTempPath()) ("crush-build-" + [guid]::NewGuid().ToString('N'))
$BuildBinary = Join-Path $BuildDir "$As.exe"
$LdFlags = "-X github.com/charmbracelet/crush/internal/version.Version=$Version -X github.com/charmbracelet/crush/internal/version.Commit=$Commit"

if ($DryRun) {
    Write-Note "would run: (cd $RepoDir; go build -trimpath -ldflags '$LdFlags' -o $Target .)"
    Write-Note 'dry run: nothing was built or installed'
    exit 0
}

try {
    New-Item -ItemType Directory -Force -Path $BuildDir | Out-Null
    Push-Location $RepoDir
    $hadCGO = Test-Path Env:CGO_ENABLED
    $oldCGO = $env:CGO_ENABLED
    try {
        $env:CGO_ENABLED = '0'
        & go build -trimpath -ldflags $LdFlags -o $BuildBinary .
        if ($LASTEXITCODE -ne 0) { throw 'build failed' }
    } finally {
        # Leave the session's environment as it was found.
        if ($hadCGO) { $env:CGO_ENABLED = $oldCGO } else { Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue }
        Pop-Location
    }

    New-Item -ItemType Directory -Force -Path $Prefix | Out-Null

    # Install through a temporary name in the destination directory, so
    # replacing a running binary cannot fail and a crash cannot leave a
    # half-written executable on PATH.
    $Stage = "$Target.install.$PID"
    Copy-Item -Force $BuildBinary $Stage
    Move-Item -Force $Stage $Target

    # Verify the installed file actually runs: a copy that cannot execute is
    # worse than no install, because it looks like one.
    try {
        & $Target --version | Out-Null
    } catch {
        Remove-Item -Force $Target -ErrorAction SilentlyContinue
        throw 'the installed binary does not run; removed it'
    }

    Write-Note "installed $Target"
    Write-Note "version: $((& $Target --version | Select-Object -First 1))"
    Write-Note ''
    Write-Note 'Sessions and config are shared with any other Crush build:'
    Write-Note "  config  $(Join-Path $env:APPDATA 'crush\crush.json')"
    Write-Note '  data    <project>\.crush'
    Write-Note ''
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -split ';' -contains $Prefix) {
        Write-Note "$Prefix is on PATH"
    } else {
        Write-Note "add $Prefix to PATH to run $As from anywhere"
    }
} finally {
    Remove-Item -Recurse -Force $BuildDir -ErrorAction SilentlyContinue
    if ($CloneDir) { Remove-Item -Recurse -Force $CloneDir -ErrorAction SilentlyContinue }
}
