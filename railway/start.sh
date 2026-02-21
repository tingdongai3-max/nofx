#!/bin/sh
set -e

# Railway 会设置 PORT、DATABASE_URL 等环境变量
export PORT=${PORT:-8080}
echo "🚀 Starting NOFX on port $PORT..."

# 后端内部端口（nginx 代理目标）；可通过 BACKEND_PORT 覆盖
BACKEND_PORT=${BACKEND_PORT:-8081}
export API_SERVER_PORT=$BACKEND_PORT

# DATABASE_URL: Railway PostgreSQL 自动提供，应用会自动连接
# 加密密钥：生产环境务必在 Railway 控制台设置 RSA_PRIVATE_KEY 和 DATA_ENCRYPTION_KEY，
# 否则每次部署会生成新密钥，无法解密数据库中已有的交易所 API 配置
if [ -z "$RSA_PRIVATE_KEY" ]; then
    export RSA_PRIVATE_KEY=$(openssl genrsa 2048 2>/dev/null)
fi
if [ -z "$DATA_ENCRYPTION_KEY" ]; then
    export DATA_ENCRYPTION_KEY=$(openssl rand -base64 32)
fi

# 生成 nginx 配置（proxy 到后端 API_SERVER_PORT）
cat > /etc/nginx/http.d/default.conf << NGINX_EOF
server {
    listen $PORT;
    server_name _;
    root /usr/share/nginx/html;
    index index.html;
    gzip on;
    gzip_types text/plain text/css application/json application/javascript;

    location / {
        try_files \$uri \$uri/ /index.html;
    }

    location /api/ {
        proxy_pass http://127.0.0.1:${BACKEND_PORT}/api/;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_connect_timeout 300s;
        proxy_send_timeout 300s;
        proxy_read_timeout 300s;
    }

    location /health {
        return 200 'OK';
        add_header Content-Type text/plain;
    }
}
NGINX_EOF

# 启动后端
/app/nofx &
sleep 2

# 启动 nginx（后台）
nginx

echo "✅ NOFX started successfully"

# 保持容器运行
tail -f /dev/null
