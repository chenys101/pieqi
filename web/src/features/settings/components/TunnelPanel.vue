<script setup lang="ts">
// 外网隧道控制面板（迁移自 V1 tunnel.js，方案 §25）：
// 状态全员可读；控制操作仅飞书移动端（后端 TunnelOpGate 二次校验）。
import { onMounted, ref } from 'vue'
import Button from '@/components/ui/Button.vue'
import { isLarkMobile } from '@/utils/lark'
import {
  getTunnelStatus,
  startTunnel,
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
const canControl = isLarkMobile()

async function refresh() {
  try {
    const st = await getTunnelStatus()
    if (!st.active) {
      statusText.value = '未运行'
      statusActive.value = false
      return
    }
    const exp = st.expires_at ? new Date(st.expires_at).toLocaleString() : '?'
    statusText.value = `运行中 · 到期 ${exp}`
    statusActive.value = true
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

    <template v-if="canControl">
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
        <Button size="sm" variant="primary" :loading="busy" @click="op(() => startTunnel(ttl), '⚠ 隧道刚启动，DNS 生效约需 30~60 秒')">
          启动隧道
        </Button>
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
    <div v-else class="text-xs text-muted">隧道控制仅飞书移动端可用；外网链接请通过飞书打开。</div>
  </div>
</template>
