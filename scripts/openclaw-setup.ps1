# OpenClaw 安装后配置脚本（Windows）
# 使用：在 openclaw 已全局安装后，于 PowerShell 中执行 .\scripts\openclaw-setup.ps1

$ErrorActionPreference = "Stop"

Write-Host "检查 openclaw 是否可用..." -ForegroundColor Cyan
$version = openclaw --version 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "未找到 openclaw。请先执行: npm install -g openclaw@latest" -ForegroundColor Yellow
    Write-Host "若已安装仍报错，请将 npm 全局 bin 目录加入 PATH（如 %AppData%\npm）" -ForegroundColor Yellow
    exit 1
}
Write-Host "openclaw 版本: $version" -ForegroundColor Green

Write-Host "`n运行配置向导并安装 Daemon（按提示操作）..." -ForegroundColor Cyan
openclaw onboard --install-daemon
if ($LASTEXITCODE -ne 0) {
    Write-Host "onboard 未成功完成，请检查上方输出。" -ForegroundColor Yellow
    exit 1
}

Write-Host "`n启动 Gateway（端口 18789）..." -ForegroundColor Cyan
Write-Host "如需后台运行，可改用: openclaw daemon start" -ForegroundColor Gray
openclaw gateway --port 18789 --verbose
