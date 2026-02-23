#!/bin/bash
#
# 在本地打包配置和数据，用于迁移到服务器。
# 在项目根目录执行（或通过参数指定 nofx 根目录）。
#
# 用法:
#   cd nofx && bash scripts/export-for-server.sh
#   # 或
#   bash scripts/export-for-server.sh /path/to/nofx
#
# 生成: nofx-migration-YYYYMMDD-HHMM.tar.gz（当前目录）
# 然后上传到服务器: scp nofx-migration-*.tar.gz user@服务器:/tmp/
# 在服务器恢复: curl -fsSL .../install-custom.sh | bash -s -- \$HOME/nofx /tmp/nofx-migration-xxx.tar.gz
#

set -e

ROOT="${1:-.}"
if [ ! -d "$ROOT" ]; then
    echo "Usage: $0 [nofx_project_root]"
    echo "  Example: cd nofx && bash scripts/export-for-server.sh"
    exit 1
fi
cd "$ROOT"

OUT_NAME="nofx-migration-$(date +%Y%m%d-%H%M).tar.gz"
OUT_DIR="$(pwd)"

# 打包 .env 和 data/（数据库、配置）
TAR_LIST=""
[ -f ".env" ] && TAR_LIST=".env"
[ -d "data" ] && TAR_LIST="$TAR_LIST data"
if [ -z "$TAR_LIST" ]; then
    echo "No .env or data/ found in $(pwd). Create them first."
    exit 1
fi

echo "[INFO] Packing .env and data/ into $OUT_NAME ..."
tar czf "$OUT_DIR/$OUT_NAME" $TAR_LIST
echo "[OK] Created: $OUT_DIR/$OUT_NAME"
echo ""
echo "Next steps:"
echo "  1. Upload to server:"
echo "     scp $OUT_NAME root@YOUR_SERVER_IP:/tmp/"
echo "  2. On server, restore and deploy (one-liner):"
echo "     curl -fsSL https://raw.githubusercontent.com/tingdongai3-max/nofx/my-custom-version/install-custom.sh | bash -s -- \$HOME/nofx /tmp/$OUT_NAME"
echo ""
