# 一键配置 Windows 的 WebDAV 客户端。
# 输入：-Url 服务地址（默认 http://127.0.0.1:8080/）；-Drive 盘符（默认 Y）
# 输出：成功打印 OK；失败退出码 1
# 预期：需要管理员权限；把 WebClient 设为自动启动、放开传输大小限制、
#       允许 Basic 认证，并把服务地址映射成指定盘符
param(
    [string]$Url = 'http://127.0.0.1:8080/',
    [string]$Drive = 'Y'
)

$ErrorActionPreference = 'Stop'

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator
)
if (-not $isAdmin) {
    Write-Host '请以管理员身份运行本脚本'
    exit 1
}

# WebClient 默认是手动启动，映射 WebDAV 前必须是运行状态
sc.exe config webclient start= auto | Out-Null
Start-Service webclient -ErrorAction SilentlyContinue

# 默认 50MB 限制；这里放宽到 4GB - 1，并允许 Basic 认证
$params = 'HKLM:\SYSTEM\CurrentControlSet\Services\WebClient\Parameters'
Set-ItemProperty -Path $params -Name FileSizeLimitInBytes -Value 4294967295 -Type DWord
Set-ItemProperty -Path $params -Name BasicAuthLevel -Value 2 -Type DWord

Restart-Service webclient -ErrorAction SilentlyContinue

# 先清掉同名盘符，避免“设备已在使用”
net use "$($Drive):" /delete /y 2>$null | Out-Null
net use "$($Drive):" $Url /persistent:no
if ($LASTEXITCODE -ne 0) {
    Write-Host "映射失败，检查服务是否在 $Url 运行"
    exit 1
}

Write-Host "OK $($Drive): -> $Url"
