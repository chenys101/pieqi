<script setup lang="ts">
// 外网隧道控制面板（迁移自 V1 tunnel.js，方案 §25）。
//
// D3-c 定案后（IMPLEMENTATION-PLAN §4.2.1），**首次启动不再是这里的一个按钮**：
// `/api/tunnel/start` 已从「仅 UA 门」并入需 token 的组，因为 token 是**管理员的
// 代理凭据**，它的可信度完全依赖「只经 IM 交付给绑定的管理员」这条通道。
// 于是唯一的 bootstrap 是：在飞书里对**管理员机器人**发「隧道」——
// 那条路有 adminBinding.Match 把关，不经过 HTTP。
//
// 判据（与「禁用即就地说明」同源）：**不给一个点下去注定失败/不安全的控件。**
// 隧道没运行时，这里给的是引导语，不是按钮。
//
// 隧道已在跑、且本页持有凭据（从飞书深链带进来的 ?token=）时，才显示
// 变更类动作：关闭 / 续期 / 重置 Token —— 它们本来就要求 token。
import { onMounted, ref } from 'vue'
import Button from '@/components/ui/Button.vue'
import { isLarkMobile } from '@/utils/lark'
import { tunnelToken } from '@/services/api/client'
import {
  getTunnelStatus,
  stopTunnel,
  resetTunnelToken,
  renewTunnel,
  tunnelQrUrl,
  type TunnelTTL,
  type TunnelOpResultDto,
} from '@/services/api/tunnel'

const statusText = ref('查询中…')
const statusActive = ref(false)
const busy = ref(false)
const result = ref<{ op: TunnelOpResultDto; note?: string } | null>(null)
const newToken = ref('')
const ttl = ref<TunnelTTL>('15m')

/** 飞书移动端 UA：后端 TunnelOpGate 的硬性要求（不满足则变更类调用一律 403） */
const isMobile = isLarkMobile()
/** 是否持有隧道凭据（深链带来的 ?token=）。没有它，变更类调用一律 401。 */
const hasToken = ref(!!tunnelToken())

/** 控制区只在「隧道在跑 + 有凭据 + 飞书移动端」时出现，三者缺一都注定失败 */
const canControl = ref(false)

async function refresh() {
  try {
    const st = await getTunnelStatus()
    statusActive.value = !!st.active
    if (!st.active) {
      statusText.value = '未运行'
      canControl.value = false
      return
    }
    const exp = st.expires_at ? new Date(st.expires_at).toLocaleString() : '?'
    statusText.value = `运行中 · 到期 ${exp}`
    hasToken.value = !!tunnelToken()
    canControl.value = isMobile && hasToken.value
  } catch (e) {
    statusText.value = `状态获取失败: ${e instanceof Error ? e.message : e}`
  }
}

async function op(fn: () => Promise<TunnelOpResultDto | void>, note?: string) {
  busy.value = true
  try {
    const r = await fn()
    if (r && (r as TunnelOpResultDto).tunnel_url) {
      result.value = { op: r as TunnelOpResultDto, note }
    }
    await refresh()
  } catch (e) {
    statusText.value = `操作失败: ${e instanceof Error ? e.message : e}`
  } finally {
    busy.value = false
  }
}

/** 重置 Token：返回结构不同（仅 token），单独处理 */
async function onReset() {
  busy.value = true
  try {
    const r = await resetTunnelToken()
    newToken.value = r.token
    await refresh()
  } catch (e) {
    statusText.value = `重置失败: ${e instanceof Error ? e.message : e}`
  } finally {
    busy.value = false
  }
}

onMounted(refresh)
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="text-sm">
      <span :class="statusActive ? 'text-success' : 'text-muted'">●</span>
      {{ statusText }}
    </div>

    <!-- 未运行：引导语，不是按钮。首次启动只能经 IM 命令。 -->
    <div
      v-if="!statusActive"
      class="rounded-lg border border-border-subtle bg-background px-3 py-2.5 text-xs leading-[1.7] text-text-secondary"
    >
      在飞书里对<b>管理员机器人</b>发送「<b>隧道</b>」即可开启 ——
      这是唯一的首次启动路径。隧道凭据是管理员代理凭据，只经 IM 交付给绑定的飞书账号，
      因此这里不提供会失败或不该由网页发起的启动按钮。
    </div>

    <!-- 在跑但没有凭据：说明差什么，不显示注定 401 的按钮 -->
    <div
      v-else-if="!canControl"
      class="rounded-lg border border-border-subtle bg-background px-3 py-2.5 text-xs leading-[1.7] text-text-tertiary"
    >
      变更隧道需要隧道凭据与飞书移动端环境。请用飞书里收到的那条隧道链接打开本页。
    </div>

    <!-- 在跑 + 有凭据 + 飞书移动端：变更类动作 -->
    <template v-else>
      <div class="flex flex-wrap items-center gap-2">
        <select
          v-model="ttl"
          class="rounded-md border border-border bg-background px-2 py-1 text-xs outline-none"
          aria-label="TTL"
        >
          <option value="15m">15 分钟</option>
          <option value="1h">1 小时</option>
          <option value="4h">4 小时</option>
        </select>
        <Button size="sm" variant="danger" :disabled="busy" @click="op(() => stopTunnel())">关闭隧道</Button>
        <Button size="sm" :disabled="busy" @click="op(() => renewTunnel(ttl), `已续期 +${ttl}，以下方最新链接为准`)">续期</Button>
        <Button size="sm" :disabled="busy" @click="onReset">重置 Token</Button>
      </div>

      <div v-if="newToken" class="rounded-lg border border-border bg-background p-3 text-xs">
        新 Token: <code>{{ newToken }}</code>
      </div>

      <div v-if="result" class="flex flex-col gap-2 rounded-lg border border-border bg-background p-3 text-xs">
        <div v-if="result.note" class="text-warning">{{ result.note }}</div>
        <div>
          <div class="mb-1 text-muted">隧道链接（点击在飞书中打开）</div>
          <a :href="result.op.lark_deep_link" target="_blank" class="break-all text-info hover:underline">
            {{ result.op.lark_deep_link }}
          </a>
        </div>
        <!-- QR：后端 /api/tunnel/qrcode 渲染 PNG -->
        <img :src="tunnelQrUrl(result.op.lark_deep_link)" alt="隧道二维码" class="h-44 w-44 rounded border border-border" />
        <div class="text-muted">
          Token: <code class="text-text">{{ result.op.token }}</code> ·
          到期 {{ new Date(result.op.expires_at).toLocaleString() }}
        </div>
      </div>
    </template>
  </div>
</template>
