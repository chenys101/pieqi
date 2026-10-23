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
# GOTMPDIR **必须**指向工作区内（2026-10-23 复核，判据见 docs/adr/0016）。
#
# go build 需要在 os.TempDir() 下创建 work dir（go-buildNNN）。在 agent 会话里，
# 「进程能否在工作区外建目录」是被限制的，于是：
#   - 不设置            → mkdir C:\...\Temp\go-buildNNN: Access is denied
#   - 指向 %TEMP% 下任意目录（哪怕目录已存在、DACL 全开）→ 同样被拒
#   - 指向 **工作区内** 的 build/.gotmp → 成功
# 实测对照（产物是否真的生成，不看退出码）：
#   GOTMPDIR=build/.gotmp → built=True
#   GOTMPDIR=%TEMP%/...   → built=False
#   未设置                 → built=False
# 所以这里**无条件**用 build/.gotmp，不再沿用调用方已有的 GOTMPDIR ——
# 一个"外部显然正确"的 GOTMPDIR（比如会话 Temp）恰恰是失败的原因。
# 同时注意 `-o` 的输出也必须在工作区内：写到 %TEMP% 会以
# `copying ...: open C:\...\Temp\xxx.exe: Access is denied` 失败。
export GOTMPDIR="$REPO/build/.gotmp"
mkdir -p "$GOTMPDIR"
echo "    (GOTMPDIR=$GOTMPDIR)"
go build -o build/pieqi.new.exe ./cmd/pieqi

# 零字节产物是构建中断的残骸；restart.bat 与自重启都把"零字节"当"没有产物"，
# 这里提前报出来，免得 deploy 把一次失败当成"没东西可投"静默跳过。
if [ ! -s build/pieqi.new.exe ]; then
  echo "ERROR: build/pieqi.new.exe 为零字节，构建未成功" >&2
  exit 1
fi

ls -l build/pieqi.new.exe
echo "OK: 交付位就绪 -> build/pieqi.new.exe（下一步：bash build/deploy-dev.sh）"
