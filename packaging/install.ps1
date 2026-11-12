# mytgfs installer.
# Input: -Dir install directory (default %LOCALAPPDATA%\mytgfs)
# Output: exit code 0 on success, 1 on failure
# Behavior:
#   - copies mytgfs.exe and mytgfs-tray.exe into the install directory
#   - keeps an existing config.toml untouched
#   - creates a Start Menu shortcut for the tray app
#   - no WinFsp, no drivers: drives are mapped over WebDAV by setup-webdav.ps1
param(
    [string]$Dir = (Join-Path $env:LOCALAPPDATA 'mytgfs')
)

$ErrorActionPreference = 'Stop'

New-Item -ItemType Directory -Force -Path $Dir | Out-Null
foreach ($name in @('mytgfs.exe', 'mytgfs-tray.exe')) {
    $src = Join-Path $PSScriptRoot $name
    if (-not (Test-Path $src)) { throw "Missing $src" }
    Copy-Item $src (Join-Path $Dir $name) -Force
}

$cfg = Join-Path $Dir 'config.toml'
if (-not (Test-Path $cfg)) {
    Copy-Item (Join-Path $PSScriptRoot 'config.example.toml') $cfg
    Write-Host "Created $cfg - fill in api_id / api_hash / bot token before first run."
}

$startMenu = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs'
$lnk = Join-Path $startMenu 'mytgfs.lnk'
$shell = New-Object -ComObject WScript.Shell
$sc = $shell.CreateShortcut($lnk)
$sc.TargetPath = Join-Path $Dir 'mytgfs-tray.exe'
$sc.WorkingDirectory = $Dir
$sc.IconLocation = Join-Path $Dir 'mytgfs-tray.exe'
$sc.Save()

Write-Host "Installed to $Dir"
Write-Host "Next: run 'mytgfs webdav' in $Dir, then run setup-webdav.ps1 as Administrator to map a drive."
