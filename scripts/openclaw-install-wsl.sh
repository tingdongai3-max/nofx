#!/usr/bin/env bash
# OpenClaw 在 WSL2 (Ubuntu) 内安装脚本
# 建议在 WSL 终端中执行（可输入 sudo 密码）：bash /mnt/c/Users/23268/nofx/nofx/scripts/openclaw-install-wsl.sh
set -e

# 国内加速：apt 换阿里云、npm 换 npmmirror（不需要可设 SKIP_MIRROR=1）
echo "==> 配置镜像加速（apt + npm）..."
if [ "${SKIP_MIRROR}" != "1" ] && [ -f /etc/apt/sources.list ]; then
  if ! grep -q "aliyun" /etc/apt/sources.list 2>/dev/null; then
    sudo cp /etc/apt/sources.list /etc/apt/sources.list.bak.$(date +%s) 2>/dev/null || true
    sudo sed -i 's|http://archive.ubuntu.com|https://mirrors.aliyun.com|g; s|http://security.ubuntu.com|https://mirrors.aliyun.com|g' /etc/apt/sources.list
  fi
fi
npm config set registry https://registry.npmmirror.com 2>/dev/null || true
export npm_config_registry=https://registry.npmmirror.com
npm config set fetch-timeout 120000 2>/dev/null || true

echo "==> 检查/安装 Node.js 22+ ..."
NODE_VER=0
if command -v node &>/dev/null; then
  NODE_VER=$(node -v | sed 's/v//' | cut -d. -f1)
  if [ "$NODE_VER" -ge 22 ]; then
    echo "Node $(node -v) 已满足要求"
  fi
fi

if ! command -v node &>/dev/null || [ "${NODE_VER}" -lt 22 ]; then
  sudo apt-get update -qq
  if [ "${SKIP_MIRROR}" = "1" ]; then
    echo "通过 NodeSource 安装 Node.js 22 ..."
    sudo apt-get install -y -qq ca-certificates curl gnupg
    curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
    sudo apt-get install -y nodejs
  else
    echo "通过 nvm + npmmirror 安装 Node.js 22（Node 二进制走国内源，避免 NodeSource 几十 MB 慢）..."
    sudo apt-get install -y -qq curl
    if [ ! -d "$HOME/.nvm" ]; then
      curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash
    fi
    [ -s "$HOME/.nvm/nvm.sh" ] && . "$HOME/.nvm/nvm.sh"
    export NVM_NODEJS_ORG_MIRROR=https://npmmirror.com/mirrors/node
    nvm install 22
    nvm use 22
  fi
fi
# 若上面用了 nvm，确保当前 shell 有 node/npm
[ -s "$HOME/.nvm/nvm.sh" ] && . "$HOME/.nvm/nvm.sh"

echo "==> Node: $(node -v), npm: $(npm -v)"

echo "==> 全局安装 openclaw@latest ..."
npm install -g openclaw@latest

echo "==> 安装完成。请在本机 WSL 终端中执行："
echo "    openclaw onboard --install-daemon"
echo "    openclaw gateway --port 18789 --verbose"
echo ""
echo "或启动 daemon 后： openclaw daemon start"
