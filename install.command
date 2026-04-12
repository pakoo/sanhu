#!/bin/bash
# 散户 sanhu — Mac 一键安装脚本
# 双击运行，自动完成：检测 Python → 安装依赖 → 初始化数据库 → 启动服务

set -e
cd "$(dirname "$0")"

echo "================================================"
echo "  散户 sanhu — 安装向导"
echo "================================================"
echo ""

# ── Step 1: 检测 Python ────────────────────────────────────────────────────
PYTHON=""
for cmd in python3 /usr/bin/python3 python; do
    if command -v "$cmd" &>/dev/null; then
        VER=$("$cmd" -c 'import sys; print(sys.version_info.major, sys.version_info.minor)')
        MAJOR=$(echo $VER | cut -d' ' -f1)
        MINOR=$(echo $VER | cut -d' ' -f2)
        if [ "$MAJOR" -eq 3 ] && [ "$MINOR" -ge 9 ]; then
            PYTHON="$cmd"
            break
        fi
    fi
done

if [ -z "$PYTHON" ]; then
    echo "❌ 未检测到 Python 3.9+"
    echo ""
    echo "请先安装 Python："
    echo "  https://www.python.org/downloads/"
    echo ""
    read -p "按回车键退出..."
    exit 1
fi

echo "✓ Python: $($PYTHON --version)"
echo ""

# ── Step 2: 安装依赖 ───────────────────────────────────────────────────────
echo "[1/3] 安装依赖包（首次约需 2-5 分钟）..."
$PYTHON -m pip install -r requirements.txt -q --disable-pip-version-check
echo "✓ 依赖安装完成"
echo ""

# ── Step 3: 初始化数据库 ───────────────────────────────────────────────────
echo "[2/3] 初始化数据库..."
echo "y" | $PYTHON scripts/init_db.py
echo ""

# ── Step 4: 启动服务 ───────────────────────────────────────────────────────
echo "[3/3] 启动服务..."
echo ""
echo "================================================"
echo "  ✅ 安装完成！"
echo ""
echo "  浏览器将自动打开："
echo "  http://localhost:8000"
echo ""
echo "  关闭此窗口即停止服务"
echo "================================================"
echo ""

# 延迟 1.5 秒后打开浏览器（等服务启动）
(sleep 1.5 && open "http://localhost:8000") &

$PYTHON app.py
