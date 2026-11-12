# 构建 mytgfs。
# FUSE 需要 WinFsp SDK；这里用 8.3 短路径避免空格把 gcc 参数拆开。
$winfsp = 'C:\PROGRA~2\WinFsp'
if (Test-Path "$winfsp\inc\fuse\fuse_common.h") {
    $env:CGO_CFLAGS = "-I$winfsp\inc\fuse -I$winfsp\inc\fuse3"
    $env:CGO_LDFLAGS = "-L$winfsp\lib"
} else {
    Write-Warning '未找到 WinFsp SDK，FUSE 挂载将无法编译。请安装 WinFsp 并勾选 Developer。'
}

go build -o mytgfs.exe ./cmd/mytgfs
if ($LASTEXITCODE -eq 0) {
    Write-Output 'OK build/mytgfs.exe'
}
