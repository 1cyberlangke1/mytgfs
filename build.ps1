# 构建 mytgfs。
# 输入：-Package 额外产出可分发的 zip（含安装/卸载脚本与配置模板）
# 输出：build\mytgfs.exe、build\mytgfs-tray.exe；失败时退出码 1
# 预期：CGO 能找到 WinFsp SDK 头文件与库
param([switch]$Package)

$winfsp = 'C:\PROGRA~2\WinFsp'
if (Test-Path "$winfsp\inc\fuse\fuse_common.h") {
    $env:CGO_CFLAGS = "-I$winfsp\inc\fuse -I$winfsp\inc\fuse3"
    $env:CGO_LDFLAGS = "-L$winfsp\lib"
} else {
    Write-Warning '未找到 WinFsp SDK，FUSE 挂载将无法编译。请安装 WinFsp 并勾选 Developer。'
}

New-Item -ItemType Directory -Force -Path 'build' | Out-Null
go build -trimpath -ldflags '-s -w' -o 'build\mytgfs.exe' ./cmd/mytgfs
if ($LASTEXITCODE -eq 0) {
    Write-Output 'OK build\mytgfs.exe'
} else {
    Write-Output 'FAIL build\mytgfs.exe'
    exit 1
}

# GUI 子系统版本：双击直接进托盘，不弹控制台
go build -trimpath -ldflags '-s -w -H=windowsgui' -o 'build\mytgfs-tray.exe' ./cmd/mytray
if ($LASTEXITCODE -eq 0) {
    Write-Output 'OK build\mytgfs-tray.exe'
} else {
    Write-Output 'FAIL build\mytgfs-tray.exe'
    exit 1
}

# 可分发 zip：解压后右键运行 install.ps1 即可，不需要装 Go 或 WinFsp SDK
if ($Package) {
    $out = 'build\package'
    if (Test-Path $out) { Remove-Item -Recurse -Force $out }
    New-Item -ItemType Directory -Force -Path $out | Out-Null
    Copy-Item 'build\mytgfs.exe', 'build\mytgfs-tray.exe', 'config.example.toml' $out -Force
    Copy-Item 'packaging\install.ps1', 'packaging\uninstall.ps1' $out -Force

    $tag = (git describe --tags --always 2>$null)
    if (-not $tag) { $tag = 'dev' }
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
