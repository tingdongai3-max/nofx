#!/bin/sh
set -e
export PORT=${PORT:-8080}
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
