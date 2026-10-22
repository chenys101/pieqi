#!/usr/bin/env bash
# 日常开发编译（第 1 步）：前端 build → 后端编译到 build/pieqi.new.exe。
#
# 与 build/build.sh 的区别（别再混淆）：
#   build.sh      发布打包：交叉编译 linux+windows → zip/tar.gz → build/release/。
#                 本机从未跑过；它**不产出** pieqi.new.exe，也不是日常迭代用的。
#   build-dev.sh  日常迭代：只编本机 Windows 二进制，落到交付位 build/pieqi.new.exe。
#
# 为什么前端必须先编：web/dist 是 //go:embed 进二进制的，反了就"改了没生效"，
# 而且不报错 —— 这是本仓库踩过的坑，顺序不能换。
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO"

echo "==> [1/2] 前端构建 (web/dist)"
(cd web && npm run build)

echo "==> [2/2] 后端编译 -> build/pieqi.new.exe"
# 落点必须是 .new：若直接写 pieqi.exe 会覆盖交付位，
# Restart() 里那份"回滚备份"保存的就是刚编出来的，回滚即失去意义。
#
# GOTMPDIR 兜底（2026-10-22 实测）：go 的 work dir 落在 os.TempDir()，而在受限
# 沙箱/服务下跑时，C:\...\Temp 本身可能不可写，报
#   go: creating work dir: mkdir C:\Users\...\Temp\go-buildNNN: Access is denied.
# 属偶发（重试常能过），但失败时不打印成功标记、产物时间戳不推进 —— 容易把
# "没编出来"当成"已编出"。这里只在**未显式设置**时指向 build/.gotmp（工作区内、
# 天然可写），且用完即清；不覆盖调用方已有的 GOTMPDIR。
if [ -z "${GOTMPDIR:-}" ]; then
  export GOTMPDIR="$REPO/build/.gotmp"
  mkdir -p "$GOTMPDIR"
  echo "    (GOTMPDIR 未设置，暂用 $GOTMPDIR)"
fi
go build -o build/pieqi.new.exe ./cmd/pieqi

# 零字节产物是构建中断的残骸；restart.bat 与自重启都把"零字节"当"没有产物"，
# 这里提前报出来，免得 deploy 把一次失败当成"没东西可投"静默跳过。
if [ ! -s build/pieqi.new.exe ]; then
  echo "ERROR: build/pieqi.new.exe 为零字节，构建未成功" >&2
  exit 1
fi

ls -l build/pieqi.new.exe
echo "OK: 交付位就绪 -> build/pieqi.new.exe（下一步：bash build/deploy-dev.sh）"
