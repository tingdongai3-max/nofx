#!/bin/bash
#
# dev_guard.sh - NOFX Development Environment Guardian
#
# This script ensures consistent backend compilation, restart, and frontend health.
# Usage: ./scripts/dev_guard.sh
#

set -e

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$PROJECT_ROOT"
source "$PROJECT_ROOT/scripts/go_env.sh"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

inspect_log_tail() {
    local name="$1"
    local logfile="$2"

    if [ ! -f "$logfile" ]; then
        return
    fi

    log_info "Inspecting recent ${name} log tail: ${logfile}"
    tail -n 20 "$logfile" || true

    if rg -n -i "address already in use|panic:|fatal:|bind:|listen tcp" "$logfile" >/dev/null 2>&1; then
        log_warn "Recent ${name} log shows a possible crash or port conflict"
    fi
}

is_port_listening() {
    local port="$1"
    ss -tlnp 2>/dev/null | grep -q ":${port}\\b"
}

wait_for_port_release() {
    local port="$1"
    local timeout="${2:-10}"

    for ((i=1; i<=timeout; i++)); do
        if ! is_port_listening "$port"; then
            return 0
        fi
        log_warn "Port ${port} still busy, waiting (${i}/${timeout})..."
        sleep 1
    done

    return 1
}

force_kill_pids() {
    local pids="$1"
    if [ -z "$pids" ]; then
        return
    fi

    log_warn "Port 8080 still busy after graceful stop, force killing old nofx process(es)"
    while read -r pid; do
        [ -n "$pid" ] || continue
        kill -9 "$pid" 2>/dev/null || true
    done <<< "$pids"
}

start_detached() {
    local logfile="$1"
    shift

    nohup setsid "$@" </dev/null >"$logfile" 2>&1 &
    local pid=$!
    disown "$pid" 2>/dev/null || true
    echo "$pid"
}

print_failure_tail() {
    local name="$1"
    local logfile="$2"
    log_error "${name} failed to start. Recent log output:"
    tail -n 20 "$logfile" || true
}

# 1. Atomic compilation
log_info "Compiling nofx binary..."
log_info "Using Go toolchain: $("$GO_BIN" version)"
if ! "$GO_BIN" build -o nofx . 2>&1; then
    log_error "Compilation failed!"
    exit 1
fi
log_info "Compilation successful"

# 2. Backend restart - graceful kill and start
log_info "Restarting nofx backend..."
inspect_log_tail "backend" "/tmp/nofx.log"
inspect_log_tail "frontend" "/tmp/nofx-web.log"

# Find and kill old nofx process
OLD_PIDS=$(pgrep -x "nofx" 2>/dev/null || true)
if [ -n "$OLD_PIDS" ]; then
    log_info "Killing old nofx process(es): $(echo "$OLD_PIDS" | tr '\n' ' ')"
    while read -r pid; do
        [ -n "$pid" ] || continue
        kill "$pid" 2>/dev/null || true
    done <<< "$OLD_PIDS"
fi

if ! wait_for_port_release 8080 10; then
    force_kill_pids "$OLD_PIDS"
    if ! wait_for_port_release 8080 5; then
        log_error "Port 8080 did not become free within 15 seconds"
        print_failure_tail "Backend" "/tmp/nofx.log"
        exit 1
    fi
fi

# Double check before launch so the new instance gets an exclusive bind.
if is_port_listening 8080; then
    log_error "Port 8080 is still occupied before backend restart"
    print_failure_tail "Backend" "/tmp/nofx.log"
    exit 1
fi

# Start new nofx in background
NEW_PID=$(start_detached /tmp/nofx.log ./nofx)
log_info "nofx started (PID: $NEW_PID)"

# Wait for backend to either stay alive or fail fast (bind/panic).
sleep 2

if ! kill -0 "$NEW_PID" 2>/dev/null; then
    log_error "Backend process exited during startup (PID: $NEW_PID)"
    print_failure_tail "Backend" "/tmp/nofx.log"
    exit 1
fi

# Wait for backend to be ready
sleep 1

# Verify backend is listening
if ! is_port_listening 8080; then
    print_failure_tail "Backend" "/tmp/nofx.log"
    exit 1
fi
log_info "Backend OK (8080 LISTEN)"

# 3. Frontend inspection
log_info "Inspecting frontend..."

if ! is_port_listening 3000; then
    log_warn "Frontend not running on 3000, starting..."
    cd "$PROJECT_ROOT/web"
    FRONTEND_PID=$(start_detached /tmp/nofx-web.log npm run dev)
    sleep 3

    if ! kill -0 "$FRONTEND_PID" 2>/dev/null; then
        print_failure_tail "Frontend" "/tmp/nofx-web.log"
        exit 1
    fi

    if ! is_port_listening 3000; then
        print_failure_tail "Frontend" "/tmp/nofx-web.log"
        exit 1
    fi
    log_info "Frontend started and OK (3000 LISTEN, PID: $FRONTEND_PID)"
else
    log_info "Frontend OK (3000 already LISTEN)"
fi

# 4. Smoke test - HTTP verification
log_info "Running smoke tests..."

# Test backend
if curl -sf http://localhost:8080/api/health > /dev/null 2>&1; then
    log_info "Backend HTTP OK (8080)"
else
    log_error "Backend HTTP failed!"
    exit 1
fi

# Test frontend
if curl -sf http://localhost:3000 > /dev/null 2>&1; then
    log_info "Frontend HTTP OK (3000)"
else
    log_error "Frontend HTTP failed!"
    exit 1
fi

# 5. Full-stack health guard
log_info "Running Go full-stack health guard..."
if ./nofx healthcheck; then
    log_info "Full-stack health guard OK"
else
    if [ "${NOFX_DEV_GUARD_RETRY:-0}" != "1" ]; then
        log_warn "Full-stack health guard failed, attempting one automatic make dev restart..."
        NOFX_DEV_GUARD_RETRY=1 make dev
        exit $?
    fi
    log_error "Full-stack health guard failed after retry"
    exit 1
fi

echo ""
log_info "=========================================="
log_info "  ALL SYSTEMS OPERATIONAL"
log_info "  Backend:  http://localhost:8080"
log_info "  Frontend: http://localhost:3000"
log_info "  Backend PID: $NEW_PID"
log_info "=========================================="
