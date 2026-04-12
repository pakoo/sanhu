#!/bin/bash
# 散户 sanhu — Mac 日常启动脚本（安装完成后每次用这个）
# 双击运行

cd "$(dirname "$0")"

PYTHON=""
for cmd in python3 /usr/bin/python3 python; do
    if command -v "$cmd" &>/dev/null 2>&1; then
        VER=$("$cmd" -c 'import sys; print(sys.version_info.major)' 2>/dev/null)
        if [ "$VER" = "3" ]; then
            PYTHON="$cmd"
            break
        fi
    fi
done

if [ -z "$PYTHON" ]; then
    echo "❌ 未找到 Python 3，请先运行 install.command"
    read -p "按回车键退出..."
    exit 1
fi

echo "================================================"
echo "  散户 sanhu 启动中..."
echo "  http://localhost:8000"
echo "  关闭此窗口即停止服务"
echo "================================================"

(sleep 1.5 && open "http://localhost:8000") &
$PYTHON app.py
