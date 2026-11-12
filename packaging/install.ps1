# mytgfs installer.
# Input: -Dir install directory (default %LOCALAPPDATA%\mytgfs)
#        -WinFspMsi local MSI path, optional; download is used when omitted
# Output: exit code 0 on success, 1 on failure
# Behavior:
#   - installs WinFsp from the official release when it is missing
#   - copies mytgfs.exe and mytgfs-tray.exe into the install directory
#   - keeps an existing config.toml untouched
#   - creates a Start Menu shortcut for the tray app
param(
    [string]$Dir = (Join-Path $env:LOCALAPPDATA 'mytgfs'),
    [string]$WinFspMsi = ''
)

$ErrorActionPreference = 'Stop'
$winfspMsiName = 'winfsp-2.1.25156.msi'
$winfspUrl = 'https://github.com/winfsp/winfsp/releases/download/v2.1/winfsp-2.1.25156.msi'

function Test-WinFsp {
    $x86 = Join-Path ${env:ProgramFiles(x86)} 'WinFsp\bin\winfsp-x64.dll'
    $x64 = Join-Path $env:ProgramFiles 'WinFsp\bin\winfsp-x64.dll'
    return (Test-Path $x86) -or (Test-Path $x64)
}

if (-not (Test-WinFsp)) {
    if (-not $WinFspMsi) {
        $WinFspMsi = Join-Path $env:TEMP $winfspMsiName
        Write-Host "Downloading WinFsp from $winfspUrl"
        Invoke-WebRequest -Uri $winfspUrl -OutFile $WinFspMsi -UseBasicParsing
    }
    Write-Host 'Installing WinFsp (silent)...'
    $p = Start-Process msiexec.exe -ArgumentList "/i `"$WinFspMsi`" /qn ADDLOCAL=ALL /norestart" -Wait -PassThru
    if ($p.ExitCode -ne 0 -and $p.ExitCode -ne 3010) {
        throw "WinFsp installer failed with exit code $($p.ExitCode)"
    }
    if (-not (Test-WinFsp)) {
        throw 'WinFsp still missing after installation'
    }
}

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
Write-Host 'Start mytgfs from the Start Menu; it mounts the configured volumes and stays in the tray.'
