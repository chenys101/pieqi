#!/usr/bin/env bash
# 构建并打包 Pieqi 发布物。
# 用法: ./build.sh [版本号]   (默认 v1.0)
# 产物: build/pieqi-<version>.tar.gz 与 .zip
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${1:-v1.0}"
OUT=build
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
# 发布包里直接给一份 config.yaml（内容 = 模板）：解压后双击 start.bat 即可运行。
# 单独留一份 .example.yaml 供对照，避免用户以为里面的值"已经是自己的"。
cp config.example.yaml "$RELEASE/config.yaml"
cp start.bat restart.bat "$RELEASE/"

echo "==> 打包 tar.gz + zip"
tar -C "$OUT" -czf "$OUT/pieqi-$VERSION.tar.gz" "pieqi-$VERSION"
powershell -NoProfile -Command "Compress-Archive -Path '$RELEASE' -DestinationPath '$OUT/pieqi-$VERSION.zip' -Force"

echo "==> 完成:"
ls -la "$OUT"
