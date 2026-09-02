<script setup lang="ts">
// 飞书渠道配置模态框（迁移自 V1 larkreg.js，方案 §25）：
// 扫码一键创建 + 手动配置；接口仅内网（外网 403 → 提示）。
import { onMounted, ref } from 'vue'
import Modal from '@/components/ui/Modal.vue'
import Button from '@/components/ui/Button.vue'
import { tunnelQrUrl } from '@/services/api/tunnel'
import {
  getLarkStatus,
  startLarkReg,
  pollLarkReg,
  getLarkConfig,
  saveLarkConfig,
} from '@/services/api/larkreg'

const POLL_INTERVAL_MS = 2000
const POLL_TIMEOUT_MS = 10 * 60 * 1000

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ close: [] }>()

const tab = ref<'scan' | 'manual'>('scan')
const registered = ref(false)
const appId = ref('')
const intranetOnly = ref(false)

// 扫码流程状态
const scanning = ref(false)
const scanStatus = ref('')
const qrUrl = ref('')
let pollTimer: ReturnType<typeof setTimeout> | null = null

// 手动配置表单
const form = ref({ appId: '', appSecret: '', verifyToken: '', encryptKey: '', eventMode: 'longconn' as 'longconn' | 'webhook' })
const secretSet = ref(false)
const saving = ref(false)
const saveMessage = ref<{ ok: boolean; text: string } | null>(null)

async function refreshStatus() {
  const st = await getLarkStatus()
  intranetOnly.value = st.status === 403
  registered.value = st.registered
  appId.value = st.appId
}

async function startScan() {
  scanning.value = true
  scanStatus.value = '正在生成二维码...'
  qrUrl.value = ''
  try {
    const url = await startLarkReg()
    qrUrl.value = url
    scanStatus.value = '请在飞书里扫码确认'
    const deadline = Date.now() + POLL_TIMEOUT_MS
    const poll = async () => {
      if (Date.now() > deadline) {
        scanStatus.value = '⏰ 超时，请重试'
        scanning.value = false
        return
      }
      const r = await pollLarkReg()
      if (r.state === 'pending') {
        scanStatus.value = '等待扫码确认...'
        pollTimer = setTimeout(poll, POLL_INTERVAL_MS)
        return
      }
      if (r.state === 'done') {
        scanStatus.value = `✅ 接入成功 (App ID: ${r.appId})${r.hint ? ' · ' + r.hint : ''}`
        qrUrl.value = ''
        await refreshStatus()
      } else {
        scanStatus.value = `❌ ${r.message}`
      }
      scanning.value = false
    }
    pollTimer = setTimeout(poll, POLL_INTERVAL_MS)
  } catch (e) {
    scanStatus.value = `❌ 启动失败: ${e instanceof Error ? e.message : e}`
    scanning.value = false
  }
}

async function loadConfig() {
  const cfg = await getLarkConfig()
  form.value.appId = cfg.app_id ?? ''
  form.value.eventMode = cfg.event_mode ?? 'longconn'
  secretSet.value = !!cfg.secret_set
}

async function save() {
  saving.value = true
  saveMessage.value = null
  try {
    const msg = await saveLarkConfig({
      appId: form.value.appId.trim(),
      appSecret: form.value.appSecret.trim(),
      verifyToken: form.value.verifyToken.trim(),
      encryptKey: form.value.encryptKey.trim(),
      eventMode: form.value.eventMode,
    })
    saveMessage.value = { ok: true, text: `✅ ${msg}` }
    form.value.appSecret = '' // 清空，避免误提交已配置 secret
    await refreshStatus()
  } catch (e) {
    saveMessage.value = { ok: false, text: `❌ ${e instanceof Error ? e.message : e}` }
  } finally {
    saving.value = false
  }
}

function onClose() {
  if (pollTimer) clearTimeout(pollTimer)
  emit('close')
}

onMounted(async () => {
  await refreshStatus()
  if (!intranetOnly.value) await loadConfig().catch(() => {})
})
</script>

<template>
  <Modal :open="props.open" title="⚡ 飞书渠道配置" @close="onClose">
    <div v-if="intranetOnly" class="py-6 text-center text-sm text-warning">
      ⚠️ 仅内网可配置渠道
    </div>
    <template v-else>
      <div class="mb-3 text-xs" :class="registered ? 'text-success' : 'text-muted'">
        {{ registered ? `已接入 · ${appId}` : '未接入 — 扫码一键创建或手动配置' }}
      </div>

      <!-- Tab 切换 -->
      <div class="mb-3 flex gap-1 rounded-lg border border-border bg-background p-1">
        <button
          class="flex-1 rounded-md px-3 py-1 text-xs font-medium transition-colors"
          :class="tab === 'scan' ? 'bg-accent text-white' : 'text-muted hover:text-text'"
          @click="tab = 'scan'"
        >
          扫码一键创建
        </button>
        <button
          class="flex-1 rounded-md px-3 py-1 text-xs font-medium transition-colors"
          :class="tab === 'manual' ? 'bg-accent text-white' : 'text-muted hover:text-text'"
          @click="tab = 'manual'"
        >
          手动配置
        </button>
      </div>

      <!-- 扫码 -->
      <div v-if="tab === 'scan'" class="flex flex-col gap-3">
        <p class="text-xs text-muted">扫码一键创建飞书自建应用，无需手动配置权限。</p>
        <Button variant="primary" size="sm" :disabled="scanning" @click="startScan">扫码接入</Button>
        <div v-if="scanStatus" class="text-xs text-muted">{{ scanStatus }}</div>
        <img
          v-if="qrUrl"
          :src="tunnelQrUrl(qrUrl)"
          alt="扫码接入二维码"
          class="h-44 w-44 self-center rounded border border-border"
        />
      </div>

      <!-- 手动配置 -->
      <form v-else class="flex flex-col gap-3" @submit.prevent="save">
        <label class="flex flex-col gap-1 text-xs text-muted">
          接入方式
          <select
            v-model="form.eventMode"
            class="rounded-md border border-border bg-background px-2.5 py-1.5 text-sm text-text outline-none focus:border-accent/60"
          >
            <option value="longconn">长连接 longconn（推荐，无需公网回调）</option>
            <option value="webhook">Webhook（需公网回调地址）</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-xs text-muted">
          App ID
          <input
            v-model="form.appId"
            placeholder="cli_xxxxxxxx"
            required
            autocomplete="off"
            class="rounded-md border border-border bg-background px-2.5 py-1.5 text-sm text-text outline-none focus:border-accent/60"
          />
        </label>
        <label class="flex flex-col gap-1 text-xs text-muted">
          App Secret
          <input
            v-model="form.appSecret"
            type="password"
            :placeholder="secretSet ? '留空则保持原值' : '必填'"
            :required="!secretSet"
            autocomplete="new-password"
            class="rounded-md border border-border bg-background px-2.5 py-1.5 text-sm text-text outline-none focus:border-accent/60"
          />
        </label>
        <template v-if="form.eventMode === 'webhook'">
          <label class="flex flex-col gap-1 text-xs text-muted">
            Verify Token
            <input
              v-model="form.verifyToken"
              placeholder="留空则保持原值"
              autocomplete="off"
              class="rounded-md border border-border bg-background px-2.5 py-1.5 text-sm text-text outline-none focus:border-accent/60"
            />
          </label>
          <label class="flex flex-col gap-1 text-xs text-muted">
            Encrypt Key
            <input
              v-model="form.encryptKey"
              placeholder="留空则保持原值"
              autocomplete="off"
              class="rounded-md border border-border bg-background px-2.5 py-1.5 text-sm text-text outline-none focus:border-accent/60"
            />
          </label>
        </template>
        <div class="flex items-center justify-between gap-2">
          <span v-if="saveMessage" class="text-xs" :class="saveMessage.ok ? 'text-success' : 'text-error'">
            {{ saveMessage.text }}
          </span>
          <span v-else></span>
          <Button variant="primary" size="sm" type="submit" :loading="saving">保存并应用</Button>
        </div>
      </form>
    </template>
  </Modal>
</template>
