#!/bin/bash
# 在服务器上开启自托管放行并重启（拉最新代码、写入 ALLOW_BYPASS_LOGIN=1、重建/重启）
# 用法（在服务器上执行）:
#   curl -fsSL https://raw.githubusercontent.com/tingdongai3-max/nofx/my-custom-version/scripts/server-allow-bypass-login.sh | bash
# 或: cd /root/nofx && bash scripts/server-allow-bypass-login.sh
set -e
cd "${1:-/root/nofx}"
[ -d .git ] && git pull origin my-custom-version || true
if [ ! -f .env ]; then
  echo "No .env in $(pwd), creating with ALLOW_BYPASS_LOGIN=1"
  echo "ALLOW_BYPASS_LOGIN=1" > .env
else
  grep -q "ALLOW_BYPASS_LOGIN=" .env && sed -i 's/^ALLOW_BYPASS_LOGIN=.*/ALLOW_BYPASS_LOGIN=1/' .env || echo "ALLOW_BYPASS_LOGIN=1" >> .env
fi
echo "ALLOW_BYPASS_LOGIN=1 set in .env"
COMPOSE_CMD="docker-compose"
command -v docker-compose &>/dev/null || COMPOSE_CMD="docker compose"
$COMPOSE_CMD -f docker-compose.custom.yml build --no-cache nofx
$COMPOSE_CMD -f docker-compose.custom.yml up -d
echo "Done. Log in at http://$(curl -s --max-time 2 ifconfig.me 2>/dev/null || echo 'YOUR_SERVER_IP'):8080 with 2326840417@qq.com / 2326840417a.A"
