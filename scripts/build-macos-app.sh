#!/usr/bin/env bash
#
# 把 sanhu 打包成可双击启动的 macOS .app
#
#   ./scripts/build-macos-app.sh              # 输出到项目根目录 sanhu.app
#   ./scripts/build-macos-app.sh ~/Applications  # 输出到指定目录
#
# 产物结构：
#   sanhu.app/Contents/MacOS/sanhu        真正的 Go 二进制
#   sanhu.app/Contents/MacOS/launcher     启动脚本（设数据目录、拉起服务、开浏览器）
#   sanhu.app/Contents/Info.plist
#
# 双击行为：启动后台服务 → 等端口就绪 → 自动打开浏览器。
# 程序在 Dock 里常驻，退出用 Dock 右键「退出」或 Cmd+Q。
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${1:-$REPO_ROOT}"
APP="$OUT_DIR/sanhu.app"
PORT="${SANHU_PORT:-8000}"

# 数据库位置：默认指向仓库里的 data/，这样双击启动和 `go run ./cmd/sanhu`
# 用的是同一个库，不会出现两份数据各自演化的情况。
# 若把仓库挪了位置，重新跑一次本脚本即可（路径是在打包时写死的）。
DATA_DIR="${SANHU_DATA_DIR:-$REPO_ROOT/data}"

VERSION="$(git -C "$REPO_ROOT" describe --tags --always 2>/dev/null || echo dev)"

echo "▸ 编译二进制（$(uname -m)）…"
mkdir -p "$APP/Contents/MacOS"
CGO_ENABLED=0 go build \
  -ldflags "-s -w -X main.version=$VERSION -X main.gitTag=$VERSION" \
  -o "$APP/Contents/MacOS/sanhu" \
  "$REPO_ROOT/cmd/sanhu"

echo "▸ 写入启动脚本…"
cat > "$APP/Contents/MacOS/launcher" <<LAUNCHER
#!/usr/bin/env bash
# 由 build-macos-app.sh 生成，改动会在下次打包时被覆盖
set -euo pipefail

HERE="\$(cd "\$(dirname "\${BASH_SOURCE[0]}")" && pwd)"
PORT=$PORT
export SANHU_DATA_DIR="$DATA_DIR"
LOG="\$HOME/Library/Logs/sanhu.log"
mkdir -p "\$(dirname "\$LOG")"

open_browser() {
  # 等服务真正起来再开浏览器，避免打开一个连接被拒的页面
  for _ in \$(seq 1 40); do
    if curl -sf --max-time 1 "http://localhost:\$PORT/api/health" >/dev/null 2>&1; then
      open "http://localhost:\$PORT"
      return
    fi
    sleep 0.5
  done
  open "http://localhost:\$PORT"   # 兜底：超时也打开，让用户看到错误页而不是毫无反应
}

# 已经有实例在跑就别再起一个（端口会冲突），直接把浏览器打开
if curl -sf --max-time 1 "http://localhost:\$PORT/api/health" >/dev/null 2>&1; then
  open "http://localhost:\$PORT"
  exit 0
fi

open_browser &
exec "\$HERE/sanhu" >>"\$LOG" 2>&1
LAUNCHER
chmod +x "$APP/Contents/MacOS/launcher"

echo "▸ 写入 Info.plist…"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>sanhu</string>
  <key>CFBundleDisplayName</key><string>散户 sanhu</string>
  <key>CFBundleIdentifier</key><string>com.pakoo.sanhu</string>
  <key>CFBundleExecutable</key><string>launcher</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>${VERSION#v}</string>
  <key>CFBundleVersion</key><string>${VERSION#v}</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

# 本机编译的产物不带 quarantine 属性，不会触发「无法验证开发者」
xattr -cr "$APP" 2>/dev/null || true

echo
echo "✓ 打包完成：$APP"
echo "  数据目录：$DATA_DIR"
echo "  端口：$PORT　日志：~/Library/Logs/sanhu.log"
echo
echo "双击 $APP 即可启动。想放进启动台就把它拖到「应用程序」文件夹——"
echo "但数据目录是打包时写死的绝对路径，挪动 .app 本身不影响，挪动仓库则需重新打包。"
