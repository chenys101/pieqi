#!/usr/bin/env bash
# 日常开发部署（第 2+3 步）：投放到运行位 → curl 触发自重启。
#
#   2. deploy : build/pieqi.new.exe --copy--> ~/.pieqi/bin/pieqi.new.exe
#   3. restart: POST /api/admin/restart（服务端自己按"有无产物"二选一）
#
# 为什么 deploy 必须写 ~/.pieqi/bin/pieqi.new.exe（而不是直接覆盖 pieqi.exe）：
#   * 运行中的 pieqi.exe 被进程锁定，Windows 不允许覆盖 —— 直接 copy 必然
#     "being used by another process"。服务自己走换版时靠 os.Rename 把运行中的
#     exe 改名（Windows 允许改名），再落新文件，所以"投放"只是把产物放到它
#     认得的交付位，真正的交换由服务完成（带 .old 回滚）。
#   * 位里落 pieqi.new.exe 而不是 pieqi.exe：换版与否由"有没有 pieqi.new.exe"
#     决定，这也正是 POST /api/admin/restart 的判据。
#
# 为什么必须用 cp（不是 mv）：仓库目录带 Low 完整性标签，build/ 里的每个文件都继承
# 它；新进程的完整性 = min(父令牌, 镜像文件标签)，所以跑 Low 镜像的进程写不了
# ~/.pieqi（创建任务 500、日志零增长）。cp 会让新文件继承目标目录的 Medium。
# xcopy/robocopy 也不要用 —— 它们可能连 SACL 一起搬。
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
ARTIFACT="$REPO/build/pieqi.new.exe"
RUNDIR="${PIEQI_RUNDIR:-$HOME/.pieqi/bin}"
PORT="${PIEQI_PORT:-3000}"
BASE="http://127.0.0.1:$PORT"
# 发起者会话 id：agent 在 pieqi 里跑时拿得到 PIEQI_TASK_ID，人类手动跑时为空的正常路径。
INITIATOR="${PIEQI_TASK_ID:-}"

# ---- 步骤 2：投放 ---------------------------------------------------------
if [ ! -s "$ARTIFACT" ]; then
  echo "ERROR: 没有可投放的产物 $ARTIFACT" >&2
  echo "       先跑：bash build/build-dev.sh" >&2
  exit 1
fi
mkdir -p "$RUNDIR"
cp -f "$ARTIFACT" "$RUNDIR/pieqi.new.exe"
echo "==> [2] 已投放 -> $RUNDIR/pieqi.new.exe"
ls -l "$RUNDIR/pieqi.new.exe"

# ---- 步骤 3：触发自重启 ------------------------------------------------
# 服务端据此决定走 swap-and-restart（有产物）还是 restart-only（无产物）。
echo "==> [3] POST $BASE/api/admin/restart"
# -m 上限 60s：正常情况下 202 立即返回（重启编排在后台）；
# 若连接卡住说明服务没在听，早点失败比干等好。
resp="$(curl -sS --noproxy '*' -m 60 -X POST \
  ${INITIATOR:+-H "X-Pieqi-Task-Id: $INITIATOR"} \
  "$BASE/api/admin/restart")" || {
  echo "ERROR: POST 失败（服务没在 $BASE 上监听？）" >&2
  exit 1
}
echo "    $resp"

case "$resp" in
  *'"swapped":true'*)
    echo "==> 已受理：swap-and-restart（换版并重启）"
    ;;
  *'"swapped":false'*)
    echo "==> 已受理：restart-only（只重启，未换版）"
    echo "    ⚠️ 这表示服务没看到 pieqi.new.exe —— 检查 $RUNDIR 是否是运行位"
    ;;
  *)
    echo "==> 已受理（响应未带 swapped 字段，请人工核对）"
    ;;
esac

# ---- 等待新实例并就绪校验 ------------------------------------------------
# 判据不能用"进程活着"（旧实例还在等新实例报到），要用端口 + 接口真的答话。
echo "==> 等待新实例在 :$PORT 就绪 ..."
for i in $(seq 1 30); do
  sleep 1
  code="$(curl -s -o /dev/null --noproxy '*' -m 3 -w '%{http_code}' "$BASE/api/admin/restart" 2>/dev/null || true)"
  if [ "$code" = "200" ]; then
    echo "==> 新实例就绪（第 ${i}s）"
    curl -s --noproxy '*' -m 5 "$BASE/api/admin/restart"; echo
    exit 0
  fi
done

echo "WARN: ${i}s 内未就绪。检查 $RUNDIR/../logs/ 与 $RUNDIR/pieqi.exe.failed" >&2
exit 1
