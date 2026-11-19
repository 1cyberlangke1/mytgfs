# 构建 mytgfs 的安卓 APK。
#
# 输入：-Abi 目标架构（arm64-v8a 真机 / x86_64 模拟器）；-Task Gradle 任务；
#       -Proxy 走 HTTP 代理下载 Gradle 依赖；-NoGradle 只编 libwails.so；
#       -E2E 编 e2e 包（假数据层，产物 build\mytgfs-e2e-android.apk）
#       环境变量 ANDROID_KEYSTORE_FILE / _PASSWORD / ANDROID_KEY_ALIAS / _KEY_PASSWORD
#       给出时 assembleRelease 用正式密钥签名，否则回退 debug 密钥
# 环境：JDK 21（AGP 8.7.3 的 lint 在 22+ 上会直接失败，CI 已固定 21）
# 输出：build\android\app\build\outputs\apk\<变体>\*.apk，并复制成
#       build\mytgfs-<版本>-android-<abi>.apk（E2E 时是 build\mytgfs-e2e-android.apk）
# 预期：Go 侧用 NDK clang 编成 libwails.so，Gradle 只负责打包；
#       Windows 主机上 wails3 的 Taskfile 不认（uname 判 Darwin/Linux），故自建脚本

param(
    [ValidateSet('arm64-v8a', 'x86_64')]
    [string]$Abi = 'arm64-v8a',
    [ValidateSet('assembleDebug', 'assembleRelease')]
    [string]$Task = 'assembleDebug',
    [string]$Proxy = '',
    [switch]$NoGradle,
    # e2e 包：编译期挂假数据层，供 CI 里的模拟器跑键盘用例
    [switch]$E2E,
    # CI 里前端已由 frontend job 产出：跳过 npm build，直接嵌入 internal/web/dist
    [switch]$SkipWebBuild
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Definition
$androidDir = Join-Path $root 'build\android'

if (-not (Test-Path $androidDir)) {
    throw "找不到安卓宿主工程: $androidDir"
}

# 版本号：与桌面二进制用同一套 git 描述
$version = (& git describe --tags --always 2>$null)
if (-not $version) { $version = 'dev' }
$version = $version.Trim() -replace '^v', ''

# 前端产物：Go 侧 embed internal/web/dist，先重建成最新再编 .so
$embedDist = Join-Path $root 'internal\web\dist'
if ($SkipWebBuild) {
    # CI 已经把产物放进 embed 目录：再构建一次就是重复工作
    if (-not (Test-Path (Join-Path $embedDist 'index.html'))) {
        throw '前端产物不存在：-SkipWebBuild 需要先把 web-dist 放到 internal/web/dist'
    }
} else {
    Push-Location (Join-Path $root 'web')
    npm run build
    if ($LASTEXITCODE -ne 0) {
        Pop-Location
        throw '前端构建失败: npm run build'
    }
    Pop-Location
    if (Test-Path $embedDist) { Remove-Item -Recurse -Force $embedDist }
    Copy-Item -Recurse (Join-Path $root 'web\dist') $embedDist
}

$sdk = $env:ANDROID_HOME
if (-not $sdk) { $sdk = $env:ANDROID_SDK_ROOT }
if (-not $sdk) { throw 'ANDROID_HOME / ANDROID_SDK_ROOT 未设置' }

# NDK：优先 ANDROID_NDK_HOME，否则取 sdk\ndk 下版本号最大的
$ndk = $env:ANDROID_NDK_HOME
if (-not $ndk) {
    $cand = Get-ChildItem (Join-Path $sdk 'ndk') -Directory -ErrorAction SilentlyContinue |
        Sort-Object { [version]($_.Name -replace '[^0-9.].*$', '') } | Select-Object -Last 1
    if ($cand) { $ndk = $cand.FullName }
}
if (-not $ndk) { throw '未找到 Android NDK，请设置 ANDROID_NDK_HOME' }

switch ($Abi) {
    'arm64-v8a' { $target = 'aarch64-linux-android21'; $goarch = 'arm64' }
    'x86_64' { $target = 'x86_64-linux-android21'; $goarch = 'amd64' }
}
# NDK 的 clang 放在宿主平台子目录里：Windows 是 windows-x86_64/*.cmd，
# Linux/macOS 是无后缀可执行文件；CI 用同一份脚本，所以这里按平台拼
$isWin = ($env:OS -eq 'Windows_NT')
$hostTag = if ($isWin) { 'windows-x86_64' } elseif ($IsMacOS) { 'darwin-x86_64' } else { 'linux-x86_64' }
$ccName = if ($isWin) { "$target-clang.cmd" } else { "$target-clang" }
$cc = Join-Path $ndk "toolchains\llvm\prebuilt\$hostTag\bin\$ccName"
if (-not (Test-Path $cc)) { throw "NDK clang 不存在: $cc" }

# 生成 overlay：往 cmd/mytray 里塞一个只在安卓生效的 init()
# c-shared 模式不会自动调 main()，必须由 init 注册
$genDir = Join-Path $androidDir '_gen'
New-Item -ItemType Directory -Force -Path $genDir | Out-Null
$genFile = Join-Path $genDir 'main_android.gen.go'
$genSrc = @'
//go:build android

package main

import "github.com/wailsapp/wails/v3/pkg/application"

func init() {
	application.RegisterAndroidMain(main)
}
'@
[IO.File]::WriteAllText($genFile, $genSrc, (New-Object Text.UTF8Encoding($false)))

$overlay = @{
    Replace = @{
        (Join-Path $root 'cmd\mytray\main_android.gen.go') = $genFile
    }
} | ConvertTo-Json
[IO.File]::WriteAllText((Join-Path $androidDir 'overlay.json'), $overlay, (New-Object Text.UTF8Encoding($false)))

$jniDir = Join-Path $androidDir "app\src\main\jniLibs\$Abi"
# abiFilters 里声明了两种架构，jniLibs 里残留的另一个架构会被一起打进 APK，
# 体积翻倍且混进上一次构建的旧库；每次只保留本次目标架构
$jniRoot = Join-Path $androidDir 'app\src\main\jniLibs'
Get-ChildItem $jniRoot -Directory -ErrorAction SilentlyContinue |
    Where-Object { $_.Name -ne $Abi } |
    ForEach-Object { Remove-Item -Recurse -Force $_.FullName }
New-Item -ItemType Directory -Force -Path $jniDir | Out-Null
$so = Join-Path $jniDir 'libwails.so'

Push-Location $root
$oldCgo = $env:CGO_ENABLED
$oldGoos = $env:GOOS
$oldGoarch = $env:GOARCH
$oldCC = $env:CC
try {
    Write-Host "编译 libwails.so (android/$goarch, $version)"
    $env:CGO_ENABLED = '1'
    $env:GOOS = 'android'
    $env:GOARCH = $goarch
    $env:CC = $cc
    $tags = if ($Task -eq 'assembleRelease') { 'android,production' } else { 'android,debug' }
    if ($E2E) { $tags = "$tags,e2e,e2eandroid" }
    $commit = & git rev-parse --short HEAD 2>$null
    if (-not $commit) { $commit = 'none' }
    $date = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    $ldflags = "-X mytgfs/internal/version.Version=v$version -X mytgfs/internal/version.Commit=$commit -X mytgfs/internal/version.Date=$date"
    & go build -buildmode=c-shared -overlay (Join-Path $androidDir 'overlay.json') `
        -ldflags $ldflags -tags $tags -o $so ./cmd/mytray
    if ($LASTEXITCODE -ne 0) { throw "go build 失败: exit $LASTEXITCODE" }

    if ($NoGradle) {
        Write-Host "已生成 $so"
        return
    }

    # Gradle 首次运行要下载发行包与依赖，走代理时由 -P 传进来
    if ($Proxy) {
        $env:GRADLE_OPTS = "-Dhttp.proxyHost=127.0.0.1 -Dhttp.proxyPort=7890 -Dhttps.proxyHost=127.0.0.1 -Dhttps.proxyPort=7890"
    }

    Write-Host "Gradle $Task"
    if ($isWin) {
        & (Join-Path $androidDir 'gradlew.bat') '-p' $androidDir $Task "--build-cache" "-PappVersion=$version"
    } else {
        # 仓库里 gradlew 的可执行位在 Windows 检出时可能丢失，直接交给 sh 更稳
        & sh (Join-Path $androidDir 'gradlew') '-p' $androidDir $Task "--build-cache" "-PappVersion=$version"
    }
    if ($LASTEXITCODE -ne 0) { throw "Gradle 失败: exit $LASTEXITCODE" }

    $variant = if ($Task -eq 'assembleRelease') { 'release' } else { 'debug' }
    $apk = Join-Path $androidDir "app\build\outputs\apk\$variant\app-$variant.apk"
    if (-not (Test-Path $apk)) { throw "未找到 APK: $apk" }
    $out = if ($E2E) {
        Join-Path $root 'build\mytgfs-e2e-android.apk'
    } else {
        Join-Path $root "build\mytgfs-$version-android-$Abi.apk"
    }
    Copy-Item $apk $out -Force
    Write-Host "APK: $out"
}
finally {
    Pop-Location
    $env:CGO_ENABLED = $oldCgo
    $env:GOOS = $oldGoos
    $env:GOARCH = $oldGoarch
    $env:CC = $oldCC
}
