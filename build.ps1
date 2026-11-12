# 构建 mytgfs。
# 输入：无
# 输出：build\mytgfs.exe；失败时退出码 1
# 预期：CGO 能找到 WinFsp SDK 头文件与库
$winfsp = 'C:\PROGRA~2\WinFsp'
if (Test-Path "$winfsp\inc\fuse\fuse_common.h") {
    $env:CGO_CFLAGS = "-I$winfsp\inc\fuse -I$winfsp\inc\fuse3"
    $env:CGO_LDFLAGS = "-L$winfsp\lib"
} else {
    Write-Warning '未找到 WinFsp SDK，FUSE 挂载将无法编译。请安装 WinFsp 并勾选 Developer。'
}

New-Item -ItemType Directory -Force -Path 'build' | Out-Null
go build -o 'build\mytgfs.exe' ./cmd/mytgfs
if ($LASTEXITCODE -eq 0) {
    Write-Output 'OK build\mytgfs.exe'
} else {
    Write-Output 'FAIL build\mytgfs.exe'
    exit 1
}
