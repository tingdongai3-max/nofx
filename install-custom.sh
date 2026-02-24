#!/bin/bash
#
# NOFX 自托管一键部署脚本（拉取你自己的分支）
# 适用于 Ubuntu Server 22.04 LTS，幂等可重复执行。
#
# 用法:
#   curl -fsSL https://raw.githubusercontent.com/tingdongai3-max/nofx/my-custom-version/install-custom.sh | bash
# 或指定安装目录:
#   curl -fsSL .../install-custom.sh | bash -s -- /opt/nofx
# 迁移本地配置与数据到服务器（先上传迁移包到服务器 /tmp/）:
#   curl -fsSL .../install-custom.sh | bash -s -- \$HOME/nofx /tmp/nofx-migration-YYYYMMDD-HHMM.tar.gz
#
# 可选环境变量（在运行前 export）:
#   NOFX_GIT_REPO    仓库 URL，默认 https://github.com/tingdongai3-max/nofx.git
#   NOFX_GIT_BRANCH  分支名，默认 my-custom-version
#   NOFX_INSTALL_DIR 安装目录，默认 $HOME/nofx
#
# 更新（任选其一）:
#   方式一（推荐，一条命令完成 pull + 重建 + 重启）:
#     curl -fsSL https://raw.githubusercontent.com/tingdongai3-max/nofx/my-custom-version/install-custom.sh | bash
#   方式二（已手动 git pull 过，在安装目录下只重建并重启）:
#     cd ~/nofx && docker compose -f docker-compose.custom.yml build --no-cache && docker compose -f docker-compose.custom.yml up -d --force-recreate
#

set -e

# ---------- 配置（你的仓库与分支） ----------
GIT_REPO="${NOFX_GIT_REPO:-https://github.com/tingdongai3-max/nofx.git}"
GIT_BRANCH="${NOFX_GIT_BRANCH:-my-custom-version}"
COMPOSE_FILE="docker-compose.custom.yml"
# 安装目录：第一个参数或环境变量或默认；第二个参数可选：迁移包路径（恢复本地配置与数据）
INSTALL_DIR="${1:-${NOFX_INSTALL_DIR:-$HOME/nofx}}"
MIGRATION_TARBALL="${2:-}"

# ---------- 颜色 ----------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
log_ok()    { echo -e "${GREEN}[OK]${NC} $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_err()   { echo -e "${RED}[ERR]${NC} $*"; }
die()       { log_err "$*"; exit 1; }

# ---------- 依赖检测与安装（Ubuntu 22.04） ----------
check_and_install_deps() {
    log_info "Checking dependencies..."
    local need_install=""
    command -v curl  &>/dev/null || need_install="$need_install curl"
    command -v git   &>/dev/null || need_install="$need_install git"
    command -v docker &>/dev/null || need_install="$need_install docker.io"
    if [ -n "$need_install" ]; then
        if [ -f /etc/debian_version ]; then
            log_info "Installing:$need_install (may ask for sudo)"
            sudo apt-get update -qq
            sudo apt-get install -y curl git docker.io || die "Failed to install packages"
            sudo usermod -aG docker "$USER" 2>/dev/null || true
            log_ok "Dependencies installed. If docker was just installed, you may need to log out and back in (or run: newgrp docker)."
        else
            die "Missing dependencies:$need_install. Please install manually (e.g. curl, git, docker)."
        fi
    fi
    if ! docker info &>/dev/null; then
        die "Docker daemon not running or permission denied. Start Docker or run with a user in group 'docker'."
    fi
    if docker compose version &>/dev/null; then
        COMPOSE_CMD="docker compose"
    elif command -v docker-compose &>/dev/null; then
        COMPOSE_CMD="docker-compose"
    else
        log_info "Docker Compose not found, installing standalone docker-compose (v1)..."
        install_docker_compose_standalone || die "Failed to install Docker Compose"
        COMPOSE_CMD="docker-compose"
    fi
    export COMPOSE_CMD
    log_ok "Dependencies OK (curl, git, docker, $COMPOSE_CMD)"
}

# 在无 docker-compose-plugin 的 Ubuntu 上安装独立版 docker-compose
install_docker_compose_standalone() {
    local arch
    case "$(uname -m)" in
        x86_64|amd64) arch="x86_64" ;;
        aarch64|arm64) arch="aarch64" ;;
        *) log_err "Unsupported arch $(uname -m)"; return 1 ;;
    esac
    local os="linux"
    local ver="v2.24.0"
    local url="https://github.com/docker/compose/releases/download/${ver}/docker-compose-${os}-${arch}"
    local dest="/usr/local/bin/docker-compose"
    curl -fsSL "$url" -o /tmp/docker-compose-bin || return 1
    sudo mv /tmp/docker-compose-bin "$dest" || mv /tmp/docker-compose-bin "$dest" || return 1
    sudo chmod +x "$dest" 2>/dev/null || chmod +x "$dest" || return 1
    log_ok "Installed docker-compose to $dest"
    return 0
}

# ---------- 安装目录：存在则 pull，否则 clone（幂等） ----------
setup_repo() {
    log_info "Setup repository at: $INSTALL_DIR"
    if [ -d "$INSTALL_DIR" ]; then
        if [ -d "$INSTALL_DIR/.git" ]; then
            local origin_url
            origin_url=$(git -C "$INSTALL_DIR" config --get remote.origin.url 2>/dev/null || true)
            if [ -n "$origin_url" ]; then
                log_info "Existing git repo found, pulling latest..."
                git -C "$INSTALL_DIR" fetch origin
                git -C "$INSTALL_DIR" checkout "$GIT_BRANCH" 2>/dev/null || git -C "$INSTALL_DIR" checkout -b "$GIT_BRANCH" origin/"$GIT_BRANCH" 2>/dev/null || true
                git -C "$INSTALL_DIR" pull origin "$GIT_BRANCH" || die "git pull failed"
                log_ok "Pulled latest from $GIT_BRANCH"
            else
                die "Directory $INSTALL_DIR exists and is a git repo but has no remote.origin. Remove it or use another directory."
            fi
        else
            die "Directory $INSTALL_DIR exists but is not a git repo. Remove it or use another directory."
        fi
    else
        mkdir -p "$(dirname "$INSTALL_DIR")"
        git clone --branch "$GIT_BRANCH" --depth 1 "$GIT_REPO" "$INSTALL_DIR" || die "git clone failed"
        log_ok "Cloned $GIT_REPO ($GIT_BRANCH) into $INSTALL_DIR"
    fi
    cd "$INSTALL_DIR" || die "Cannot cd to $INSTALL_DIR"
}

# ---------- 恢复迁移包（可选：覆盖 .env 和 data/） ----------
restore_migration() {
    if [ -z "$MIGRATION_TARBALL" ] || [ ! -f "$MIGRATION_TARBALL" ]; then
        return 0
    fi
    log_info "Restoring migration from: $MIGRATION_TARBALL"
    tar -xzf "$MIGRATION_TARBALL" -C "$INSTALL_DIR" || die "Failed to extract migration tarball"
    # 兼容本地为 data/data.db 的布局：Docker 需要 data/db/data.db
    if [ -f "$INSTALL_DIR/data/data.db" ] && [ ! -f "$INSTALL_DIR/data/db/data.db" ]; then
        mkdir -p "$INSTALL_DIR/data/db"
        cp "$INSTALL_DIR/data/data.db" "$INSTALL_DIR/data/db/data.db"
        log_info "Copied data/data.db -> data/db/data.db for Docker layout"
    fi
    # 恢复后保证自托管可登录（避免本地 .env 覆盖导致 ALLOW_BYPASS_LOGIN 丢失）
    grep -q "ALLOW_BYPASS_LOGIN=" "$INSTALL_DIR/.env" 2>/dev/null && sed -i 's/^ALLOW_BYPASS_LOGIN=.*/ALLOW_BYPASS_LOGIN=1/' "$INSTALL_DIR/.env" || echo "ALLOW_BYPASS_LOGIN=1" >> "$INSTALL_DIR/.env"
    log_ok "Migration restored (.env and data/ applied, ALLOW_BYPASS_LOGIN=1 ensured)"
}

# ---------- .env：不存在则生成，不硬编码敏感信息 ----------
setup_env() {
    log_info "Checking .env..."
    if [ -f ".env" ]; then
        log_ok ".env already exists, skipping generation (edit it if you need to change secrets)"
        return 0
    fi
    command -v openssl &>/dev/null || die "openssl not found, cannot generate keys"
    log_info "Generating .env with JWT_SECRET and DATA_ENCRYPTION_KEY (openssl)..."
    JWT_SECRET=$(openssl rand -base64 32)
    DATA_ENCRYPTION_KEY=$(openssl rand -base64 32)
    if [ -f ".env.example" ]; then
        grep -v "^JWT_SECRET=" .env.example 2>/dev/null | grep -v "^DATA_ENCRYPTION_KEY=" > .env || true
    else
        echo "# NOFX env (generated $(date -u +%Y-%m-%dT%H:%M:%SZ))" > .env
    fi
    echo "JWT_SECRET=$JWT_SECRET" >> .env
    echo "DATA_ENCRYPTION_KEY=$DATA_ENCRYPTION_KEY" >> .env
    grep -q "NOFX_BACKEND_PORT=" .env 2>/dev/null || echo "NOFX_BACKEND_PORT=8080" >> .env
    grep -q "TZ=" .env 2>/dev/null || echo "TZ=Asia/Shanghai" >> .env
    log_ok ".env ready (secrets generated, not hardcoded in script)"
}

# ---------- 检查服务是否已在运行（幂等） ----------
is_service_running() {
    [ -f "$COMPOSE_FILE" ] || return 1
    $COMPOSE_CMD -f "$COMPOSE_FILE" ps --status running -q 2>/dev/null | head -1 | grep -q .
}

# ---------- 构建并启动 ----------
build_and_start() {
    log_info "Building image (this may take several minutes)..."
    $COMPOSE_CMD -f "$COMPOSE_FILE" build --no-cache || die "Docker build failed"
    log_ok "Build finished"
    if is_service_running; then
        log_info "Service already running, restarting to use new image..."
        $COMPOSE_CMD -f "$COMPOSE_FILE" up -d --force-recreate || die "Restart failed"
    else
        log_info "Starting services..."
        $COMPOSE_CMD -f "$COMPOSE_FILE" up -d || die "Start failed"
    fi
    log_ok "Services started"
}

# ---------- 等待健康 ----------
wait_healthy() {
    log_info "Waiting for backend (max 90s)..."
    local url="http://127.0.0.1:8080/api/health"
    local i=1
    while [ $i -le 45 ]; do
        if curl -sf --max-time 3 "$url" >/dev/null 2>&1; then
            log_ok "Backend is ready"
            return 0
        fi
        printf "  attempt %d/45\n" "$i"
        sleep 2
        i=$((i + 1))
    done
    log_warn "Backend not ready in time; check: $COMPOSE_CMD -f $COMPOSE_FILE logs -f"
}

# ---------- 显示访问信息 ----------
print_success() {
    local ip
    ip=$(curl -s --max-time 3 ifconfig.me 2>/dev/null || curl -s --max-time 3 icanhazip.com 2>/dev/null || echo "127.0.0.1")
    [ -z "$ip" ] && ip=$(hostname -I 2>/dev/null | awk '{print $1}')
    [ -z "$ip" ] && ip="127.0.0.1"
    echo ""
    echo -e "${GREEN}╔════════════════════════════════════════════════════════════╗"
    echo -e "║              🎉 部署完成（自托管分支） 🎉                  ║"
    echo -e "╚════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "  ${BLUE}Web:${NC}  http://${ip}:8080"
    echo -e "  ${BLUE}本地:${NC} http://127.0.0.1:8080"
    echo -e "  ${BLUE}目录:${NC} $INSTALL_DIR"
    echo ""
    echo -e "${CYAN}保持更新（拉取你的分支并重建）:${NC}"
    echo -e "  ${GREEN}curl -fsSL https://raw.githubusercontent.com/tingdongai3-max/nofx/my-custom-version/install-custom.sh | bash${NC}"
    echo ""
    echo -e "${YELLOW}常用命令:${NC}"
    echo "  cd $INSTALL_DIR"
    echo "  $COMPOSE_CMD -f $COMPOSE_FILE logs -f    # 日志"
    echo "  $COMPOSE_CMD -f $COMPOSE_FILE restart    # 重启"
    echo "  $COMPOSE_CMD -f $COMPOSE_FILE down      # 停止"
    echo ""
}

# ---------- Main ----------
main() {
    echo -e "${BLUE}"
    echo "╔════════════════════════════════════════════════════════════╗"
    echo "║           NOFX 自托管一键部署（自定义分支）                  ║"
    echo "╚════════════════════════════════════════════════════════════╝"
    echo -e "${NC}"
    check_and_install_deps
    setup_repo
    restore_migration
    setup_env
    build_and_start
    wait_healthy
    print_success
}

main "$@"
