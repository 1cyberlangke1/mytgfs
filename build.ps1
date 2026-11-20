# 构建 mytgfs。
# 输入：-Package 额外产出可分发的 zip（含安装/卸载脚本与配置模板）
# 输出：build\mytgfs.exe、build\mytgfs-tray.exe、build\mytgfs-cli.exe、build\mytgfs.ico；失败时退出码 1
# 预期：纯 Go 构建，不需要 CGO；cli 版用 -tags noweb 去掉 web/tray；
#       exe 文件图标由 cmd\*/rsrc_windows_amd64.syso 提供（internal/brand/gen 生成）
param([switch]$Package)

New-Item -ItemType Directory -Force -Path 'build' | Out-Null

# 前端产物不入库：每次构建现做，拷进 embed 目录
Push-Location web
npm run build
if ($LASTEXITCODE -ne 0) {
    Pop-Location
    Write-Output 'FAIL web build'
    exit 1
}
Pop-Location
if (Test-Path 'internal\web\dist') { Remove-Item -Recurse -Force 'internal\web\dist' }
Copy-Item -Recurse 'web\dist' 'internal\web\dist'

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

# 校验 tray 的 PE 子系统必须是 GUI：裸 go build 会产出控制台子系统，双击弹黑框
# 输入：build\mytgfs-tray.exe
# 输出：子系统不是 2（WINDOWS_GUI）时打印 FAIL 并以 1 退出
# 预期：构建阶段就拦住错误产物，不把问题留到双击时
$trayExe = 'build\mytgfs-tray.exe'
$fs = [IO.File]::OpenRead($trayExe)
try {
    $br = New-Object IO.BinaryReader($fs)
    $fs.Position = 0x3C
    $peOffset = $br.ReadInt32()
    # PE32/PE32+ 的 OptionalHeader.Subsystem 都在 OptionalHeader 起始 + 68
    $fs.Position = $peOffset + 24 + 68
    $subsystem = $br.ReadUInt16()
} finally {
    $fs.Dispose()
}
if ($subsystem -ne 2) {
    Write-Output "FAIL $trayExe subsystem=$subsystem (expected 2 / WINDOWS_GUI)"
    exit 1
}

# 纯命令行版：不带 web/tray 依赖，体积约为完整版三分之一
go build -trimpath -tags noweb -ldflags $base -o 'build\mytgfs-cli.exe' ./cmd/mytgfs
if ($LASTEXITCODE -ne 0) {
    Write-Output 'FAIL build\mytgfs-cli.exe'
    exit 1
}
Write-Output 'OK build\mytgfs-cli.exe'

# 桌面快捷方式把图标指向 build\mytgfs.ico：每次构建都补一份，避免快捷方式显示空白文档图标
Copy-Item 'internal\brand\icons\icon-windows.ico' 'build\mytgfs.ico' -Force
Write-Output 'OK build\mytgfs.ico'

# 可分发 zip：解压后运行 install.ps1，用户不需要装任何运行库
if ($Package) {
    $out = 'build\package'
    if (Test-Path $out) { Remove-Item -Recurse -Force $out }
    New-Item -ItemType Directory -Force -Path $out | Out-Null
    Copy-Item 'build\mytgfs.exe', 'build\mytgfs-tray.exe', 'build\mytgfs-cli.exe', 'config.example.toml', 'LICENSE' $out -Force
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
