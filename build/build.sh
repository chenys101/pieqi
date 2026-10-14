#!/usr/bin/env bash
# 构建并打包 Pieqi 发布物。
# 用法: build/build.sh [版本号]   (默认 v1.0)
# 产物: build/release/pieqi-<version>.tar.gz 与 .zip
#
# 位置约定（2026-10-14 迁移后）：本脚本住在 <repo>/build/ 下，所以先回到仓库根再干活。
# 产物**不能**直接落在 build/ 根目录 —— 那里是编译落点（pieqi.new.exe / pieqi.exe）
# 与自测沙盒，release 产物混在里面既难分辨、又会被下一次构建的清理动作误删。
# 所以统一放到 build/release/ 子目录，与交付物彻底分开。
set -euo pipefail

# 本脚本在 <repo>/build/ 下：上跳一级即仓库根。
REPO="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO"

VERSION="${1:-v1.0}"
OUT=build/release
rm -rf "$OUT"
mkdir -p "$OUT"

echo "==> 构建前端 (web/dist)"
(cd web && npm run build)

echo "==> 构建 linux/amd64"
GOOS=linux GOARCH=amd64 go build -o "$OUT/pieqi-linux-amd64" ./cmd/pieqi

echo "==> 构建 windows/amd64"
GOOS=windows GOARCH=amd64 go build -o "$OUT/pieqi-windows-amd64.exe" ./cmd/pieqi

# 发布目录：pieqi 二进制的两个平台 + 示例配置 + 文档 + 启动脚本
RELEASE="$OUT/pieqi-$VERSION"
mkdir -p "$RELEASE"
cp "$OUT/pieqi-linux-amd64" "$OUT/pieqi-windows-amd64.exe" "$RELEASE/"
cp config.example.yaml README.md "$RELEASE/"
# 发布包里直接给一份 config.yaml（内容 = 模板）：解压后双击 restart.bat 即可运行。
# 单独留一份 .example.yaml 供对照，避免用户以为里面的值"已经是自己的"。
cp config.example.yaml "$RELEASE/config.yaml"
# ⚠️ 脚本在仓库里住在 build/ 下（那里同时是"编译落点"），但**发布包内的布局不同**：
# 包内没有 build/ 这层，可执行文件就叫 pieqi.exe。restart.bat 已带跨机器兼容的
# 候选探测（若不存在 %REPO%build\pieqi.exe 就用 %REPO%pieqi.exe 装成"暂存物"
# 再拷到用户目录运行），所以这里只需把脚本与二进制平铺进包。
cp build/restart.bat build/start.bat build/start-detached.bat "$RELEASE/"
# 关键：把 windows 产物也以 pieqi.exe 供一份 —— 否则 restart.bat 找不到任何可运行的
# 二进制，包内"解压双击即可运行"就是空话（这正是迁移前 build.sh 的缺口：
# 它只带了 pieqi-windows-amd64.exe，而脚本只认 pieqi.exe / build\pieqi.exe）。
cp "$OUT/pieqi-windows-amd64.exe" "$RELEASE/pieqi.exe"
# 安装说明：说清"包里的 pieqi.exe 与脚本同级"这一布局，免得用户按仓库布局去套。
cat > "$RELEASE/HOW-TO-RUN.txt" <<'EOF'
Pieqi - how to run this release
===============================

1. Copy config.example.yaml to config.yaml (a copy is already named config.yaml
   here) and edit it: server.port, channels, agents, auth...

2. Double-click  restart.bat  (NOT pieqi.exe).

   Why you must not run pieqi.exe directly: restart.bat copies the binary out of
   the current directory into %USERPROFILE%\.pieqi\bin\ and launches it from
   there. That copy step is what gives the process a normal integrity level; an
   executable sitting next to a Low-integrity directory would run sandboxed and
   could not write its data root (symptom: HTTP 500 on task creation, and the
   log file stops growing).

3. restart.bat needs a binary named either  .\build\pieqi.exe  (repo layout) or
   .\pieqi.exe  (this release layout). Both are handled; no edit required.
EOF

echo "==> 打包 tar.gz + zip"
tar -C "$OUT" -czf "$OUT/pieqi-$VERSION.tar.gz" "pieqi-$VERSION"
powershell -NoProfile -Command "Compress-Archive -Path '$RELEASE' -DestinationPath '$OUT/pieqi-$VERSION.zip' -Force"

echo "==> 完成:"
ls -la "$OUT"
