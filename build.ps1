# 构建 mytgfs。
# 输入：-Package 额外产出可分发的 zip（含安装/卸载脚本与配置模板）
# 输出：build\mytgfs.exe、build\mytgfs-tray.exe；失败时退出码 1
# 预期：纯 Go 构建，不需要 CGO
param([switch]$Package)

New-Item -ItemType Directory -Force -Path 'build' | Out-Null

# 版本信息注入二进制：排障时能直接看到构建来源
$ver = (git describe --tags --always 2>$null)
if (-not $ver) { $ver = 'dev' }
$commit = (git rev-parse --short HEAD 2>$null)
if (-not $commit) { $commit = 'none' }
$date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
$base = "-s -w -X mytgfs/internal/version.Version=$ver -X mytgfs/internal/version.Commit=$commit -X mytgfs/internal/version.Date=$date"

go build -trimpath -ldflags $base -o 'build\mytgfs.exe' ./cmd/mytgfs
if ($LASTEXITCODE -ne 0) {
    Write-Output 'FAIL build\mytgfs.exe'
    exit 1
}
Write-Output "OK build\mytgfs.exe $ver ($commit)"

# GUI 子系统版本：双击直接进托盘，不弹控制台
go build -trimpath -ldflags "$base -H=windowsgui" -o 'build\mytgfs-tray.exe' ./cmd/mytray
if ($LASTEXITCODE -ne 0) {
    Write-Output 'FAIL build\mytgfs-tray.exe'
    exit 1
}
Write-Output 'OK build\mytgfs-tray.exe'

# 可分发 zip：解压后运行 install.ps1，用户不需要装任何运行库
if ($Package) {
    $out = 'build\package'
    if (Test-Path $out) { Remove-Item -Recurse -Force $out }
    New-Item -ItemType Directory -Force -Path $out | Out-Null
    Copy-Item 'build\mytgfs.exe', 'build\mytgfs-tray.exe', 'config.example.toml', 'LICENSE' $out -Force
    Copy-Item 'packaging\install.ps1', 'packaging\uninstall.ps1' $out -Force

    $tag = $ver
    $zip = "build\mytgfs-$tag-win64.zip"
    if (Test-Path $zip) { Remove-Item $zip -Force }
    Compress-Archive -Path "$out\*" -DestinationPath $zip
    if ($LASTEXITCODE -eq 0) {
        Write-Output "OK $zip"
    } else {
        Write-Output "FAIL $zip"
        exit 1
    }
}
