# hitron installer for Windows (PowerShell 5.1 or newer).
#
#   irm https://hitron.netquor.dev/install.ps1 -OutFile $env:TEMP\hitron.ps1
#   & $env:TEMP\hitron.ps1 -Run
#
# Creates an isolated virtualenv under %LOCALAPPDATA%\hitron, installs the
# `hitron` package from GitHub, writes `hitron.cmd` launchers into
# %LOCALAPPDATA%\hitron\bin, and optionally starts the local docs site plus
# gateway proxy. No admin rights, no system Python packages, no registry
# changes beyond nothing at all.
#
# Examples:
#   .\install.ps1                                     # install only
#   .\install.ps1 -Run                                # start the site + proxy
#   .\install.ps1 -Run -Device 192.168.100.1 -ReadOnly
#   .\install.ps1 -Uninstall
#
# Running scripts may be blocked by ExecutionPolicy. Either use
#   powershell -ExecutionPolicy Bypass -File .\install.ps1 -Run
# or the one-liner above, which does not touch the policy.
[CmdletBinding()]
param(
    [string]$Device = $(if ($env:HITRON_DEVICE) { $env:HITRON_DEVICE } else { "192.168.100.1" }),
    [int]$Port = $(if ($env:HITRON_PORT) { [int]$env:HITRON_PORT } else { 8000 }),
    [string]$Listen = $(if ($env:HITRON_LISTEN) { $env:HITRON_LISTEN } else { "127.0.0.1" }),
    [string]$Repo = $(if ($env:HITRON_REPO) { $env:HITRON_REPO } else { "Netquor/hitron" }),
    [string]$Ref = $(if ($env:HITRON_REF) { $env:HITRON_REF } else { "main" }),
    [string]$Prefix = $(if ($env:HITRON_PREFIX) { $env:HITRON_PREFIX } else { Join-Path $env:LOCALAPPDATA "hitron" }),
    [string]$BinDir = $(if ($env:HITRON_BINDIR) { $env:HITRON_BINDIR } else { Join-Path $env:LOCALAPPDATA "hitron\bin" }),
    [switch]$Run,
    [switch]$ReadOnly,
    [switch]$NoOpen,
    [switch]$Uninstall,
    [switch]$Help
)

$ErrorActionPreference = "Stop"

function Show-Usage {
    @"
hitron installer for Windows

Usage:
  .\install.ps1 [options]

Options:
  -Device HOST      gateway address used by -Run (default: $Device)
  -Port PORT        port for -Run (default: $Port)
  -Listen ADDR      bind address for -Run (default: $Listen)
  -Run              start the local docs site + gateway proxy after install
  -ReadOnly         with -Run, block write verbs at the proxy
  -NoOpen           do not open the default browser (it opens automatically)
  -Ref REF          branch or tag to install (default: $Ref)
  -Prefix DIR       install venv here (default: $Prefix)
  -BinDir DIR       write the launchers here (default: $BinDir)
  -Uninstall        remove the install above and the launchers
  -Help             show this help

One-liner:
  irm https://hitron.netquor.dev/install.ps1 -OutFile `$env:TEMP\hitron.ps1
  & `$env:TEMP\hitron.ps1 -Run

Then open http://127.0.0.1:8000 - the API console's base URL is /proxy.
"@
}

if ($Help) { Show-Usage; exit 0 }

if ($Uninstall) {
    Get-ChildItem -Path $BinDir -Filter "hitron*.cmd" -ErrorAction SilentlyContinue | Remove-Item -Force
    if (Test-Path $Prefix) { Remove-Item -Recurse -Force $Prefix }
    Write-Host "removed $Prefix and the hitron launchers in $BinDir"
    exit 0
}

# ---- find Python 3.11+ ----------------------------------------------------
function Find-Python {
    $candidates = @()
    if ($env:PYTHON) { $candidates += ,@($env:PYTHON) }
    $candidates += ,@("py", "-3")
    $candidates += ,@("python")
    $candidates += ,@("python3")

    foreach ($candidate in $candidates) {
        $exe = $candidate[0]
        $pre = @()
        if ($candidate.Count -gt 1) { $pre = $candidate[1..($candidate.Count - 1)] }
        $found = Get-Command $exe -ErrorAction SilentlyContinue
        if (-not $found) { continue }
        try {
            & $exe @pre -c "import sys; raise SystemExit(0 if sys.version_info >= (3, 11) else 1)" 2>$null
            if ($LASTEXITCODE -eq 0) {
                return [pscustomobject]@{ Exe = $exe; Pre = $pre }
            }
        } catch {
            continue
        }
    }
    return $null
}

$python = Find-Python
if (-not $python) {
    Write-Host "hitron: Python 3.11+ was not found." -ForegroundColor Red
    Write-Host "Install it from https://www.python.org/downloads/windows/ (tick 'Add python.exe to PATH'),"
    Write-Host "or run: winget install Python.Python.3.12"
    exit 1
}

Write-Host "hitron: installing into $Prefix"
New-Item -ItemType Directory -Force -Path $Prefix | Out-Null
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

$Venv = Join-Path $Prefix "venv"
$VenvPython = Join-Path $Venv "Scripts\python.exe"

if (-not (Test-Path $VenvPython)) {
    Write-Host "hitron: creating virtualenv"
    & $python.Exe @($python.Pre) -m venv $Venv
    if ($LASTEXITCODE -ne 0) { throw "python -m venv failed" }
}

# Resolve the ref to a commit so a rerun downloads a different archive instead
# of answering from pip's cache. git is optional on Windows.
$Resolved = $Ref
if (Get-Command git -ErrorAction SilentlyContinue) {
    try {
        $line = & git ls-remote "https://github.com/$Repo" "refs/heads/$Ref" 2>$null | Select-Object -First 1
        if ($line) { $Resolved = ($line -split "\s+")[0] }
    } catch {
        $Resolved = $Ref
    }
}
$Url = "https://github.com/$Repo/archive/$Resolved.tar.gz"

Write-Host "hitron: installing $Ref from $Repo"
# First pass resolves dependencies; the second always replaces the app itself,
# because pip skips an unchanged direct-URL version on reruns.
& $VenvPython -m pip install --quiet --disable-pip-version-check $Url
if ($LASTEXITCODE -ne 0) { throw "pip install failed" }
& $VenvPython -m pip install --quiet --disable-pip-version-check --no-cache-dir --force-reinstall --no-deps $Url
if ($LASTEXITCODE -ne 0) { throw "pip force-reinstall failed" }

# ---- launchers ------------------------------------------------------------
$hitronShim = Join-Path $BinDir "hitron.cmd"
$exporterShim = Join-Path $BinDir "hitron-exporter.cmd"
@"
@echo off
"$Prefix\venv\Scripts\hitron.exe" %*
"@ | Set-Content -Path $hitronShim -Encoding ASCII
@"
@echo off
"$Prefix\venv\Scripts\hitron-exporter.exe" %*
"@ | Set-Content -Path $exporterShim -Encoding ASCII

$onPath = ($env:PATH -split ";") -contains $BinDir
if (-not $onPath) {
    Write-Host ""
    Write-Host "Add this folder to PATH so 'hitron' works in new terminals:"
    Write-Host "  [Environment]::SetEnvironmentVariable('Path', `$env:Path + ';$BinDir', 'User')"
    Write-Host "For this session only:"
    Write-Host "  `$env:Path += ';$BinDir'"
}

Write-Host ""
Write-Host "Installed. Verify with:"
Write-Host "  `"$BinDir\hitron.cmd`" --help"
Write-Host ""

if ($Run) {
    $siteArgs = @("site", "--listen", $Listen, "--port", "$Port", "--device", $Device)
    if ($ReadOnly) { $siteArgs += "--read-only" }
    if ($NoOpen) { $siteArgs += "--no-open" }
    Write-Host "Starting the local site + proxy for gateway $Device"
    Write-Host "Open http://127.0.0.1:$Port (Ctrl-C to stop)"
    & (Join-Path $Venv "Scripts\hitron.exe") @siteArgs
} else {
    Write-Host "Start the local site + proxy with:"
    Write-Host "  `"$BinDir\hitron.cmd`" site --device $Device --port $Port"
    Write-Host ""
    Write-Host "Then open http://127.0.0.1:$Port and keep the API base URL at /proxy."
}
