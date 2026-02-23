#!/bin/sh
set -e
export PORT=${PORT:-8080}

# 持久化目录：/data/config 与 /data/db，用于 Docker 重启后保留配置与数据库
mkdir -p /data/config /data/db
# 软链接：/app/data -> /data/db，使数据库写入持久卷
ln -sfn /data/db /app/data 2>/dev/null || true
# 使用持久化 DB 路径（挂载 /data 卷时生效）
export DB_PATH=${DB_PATH:-/data/db/data.db}

# 加密密钥必须由环境变量提供，禁止随机生成，避免重启后 "Decryption Failed"
if [ -z "$DATA_ENCRYPTION_KEY" ]; then
  echo "ERROR: DATA_ENCRYPTION_KEY must be set in Railway Variables."
  echo "Set a persistent base64 key (e.g. openssl rand -base64 32) to avoid decryption errors on restart."
  exit 1
fi
# 自检：确保 DB 目录可写（Railway 需将卷挂载到 /data）
if ! touch /data/db/.writable 2>/dev/null; then
  echo "WARN: /data/db not writable - add volume mount to /data in Railway for persistence"
elif rm -f /data/db/.writable 2>/dev/null; then
  : # OK
fi

# 关键修复：确保配置目录存在
mkdir -p /etc/nginx/conf.d

# 生成 nginx 配置（注意路径改为 Debian 默认的 conf.d）
cat > /etc/nginx/conf.d/default.conf << NGINX_EOF
server {
    listen $PORT;
    server_name _;
    root /usr/share/nginx/html;
    index index.html;
    location / { try_files \$uri \$uri/ /index.html; }
    location /api/ {
        proxy_pass http://127.0.0.1:8081/api/;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
    }
}
NGINX_EOF

# 修正 Nginx 主配置以包含 conf.d
sed -i 's|include /etc/nginx/http.d/\*.conf;|include /etc/nginx/conf.d/*.conf;|' /etc/nginx/nginx.conf || true

# 启动
API_SERVER_PORT=8081 /app/nofx &
sleep 2
nginx -g "daemon off;"
