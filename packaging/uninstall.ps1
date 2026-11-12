# mytgfs uninstaller.
# Input: -Dir install directory (default %LOCALAPPDATA%\mytgfs)
#        -Purge also deletes data\, secrets\ and config.toml
# Output: exit code 0 on success
# Behavior:
#   - removes the Start Menu shortcut and the autostart registry value
#   - without -Purge keeps data\, secrets\ and config.toml in place
param(
    [string]$Dir = (Join-Path $env:LOCALAPPDATA 'mytgfs'),
    [switch]$Purge
)

$ErrorActionPreference = 'Stop'

$lnk = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\mytgfs.lnk'
if (Test-Path $lnk) { Remove-Item $lnk -Force }

$runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
if (Test-Path $runKey) {
    Remove-ItemProperty -Path $runKey -Name 'mytgfs' -ErrorAction SilentlyContinue
}

if (Test-Path $Dir) {
    if ($Purge) {
        Remove-Item -Recurse -Force $Dir
        Write-Host "Removed $Dir"
    } else {
        foreach ($name in @('mytgfs.exe', 'mytgfs-tray.exe')) {
            Remove-Item (Join-Path $Dir $name) -Force -ErrorAction SilentlyContinue
        }
        Write-Host "Removed binaries from $Dir; data, secrets and config.toml were kept."
    }
}

Write-Host 'Uninstalled.'
