<script setup lang="ts">
// 创建机器人弹层（SPEC §5.4.2 / §5.4.3）。
//
// **形态不是设计出来的，是流程决定的**：真实实现是飞书 Device Flow ——
// `POST /api/larkreg/start` 拿授权链接 → 渲染成二维码 → 用户用飞书扫 →
// `GET /api/larkreg/poll` 轮询到成功、服务端落盘凭据。
// 所以扫码这一半**没有「创建」按钮**：创建这个动作发生在用户的手机上，
// 桌面上点按钮什么也不会发生。（回归脚本显式守着这条，别"顺手加回来"。）
//
// 手动配置那一半则**必须**有提交按钮 —— 判据只有一条：创建这个动作发生在哪一端。
// 两半共用同一个弹层、同一份预设提示词，因为它们是同一件事的两种达成方式。
//
// 四态由 `stage` 单属性驱动（不为状态额外维护 class）：
//   gen  = 生成中（start 内部最多等 3s）
//   scan = 待扫码（码 + 有效期倒计时 + 人的三步）
//   busy = 已授权，轮转到机器侧动作
//   dead = 码过期（唯一出路是重新生成）
//
// 倒计时秒数来自接口的 `expire_in`（SDK 默认 600s），**不从常量写死** ——
// 它必须与服务端真实有效期一致，否则用户会在一个"看着还能扫"的码前干等。
import { computed, onUnmounted, ref, watch } from 'vue'
import Modal from '@/components/ui/Modal.vue'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Textarea from '@/components/ui/Textarea.vue'
import Select from '@/components/ui/Select.vue'
import { tunnelQrUrl } from '@/services/api/tunnel'
import { startLarkReg, pollLarkReg, getLarkConfig, saveLarkConfig } from '@/services/api/larkreg'

const POLL_INTERVAL_MS = 2000

const props = defineProps<{
  open: boolean
  /** 是否第一台（由当前机器人列表算出，**不由入口决定**） */
  first: boolean
}>()

const emit = defineEmits<{ close: []; created: [] }>()

type Stage = 'gen' | 'scan' | 'busy' | 'dead'

const mode = ref<'scan' | 'manual'>('scan')
const stage = ref<Stage>('gen')
const statusLine = ref('')
const qrUrl = ref('')
const remaining = ref(0)
/** 服务端给出的二维码有效期（秒）；dead 态重新生成会沿用同一个值 */
const ttl = ref(600)

// 手动配置表单
const form = ref({
  appId: '',
  appSecret: '',
  verifyToken: '',
  encryptKey: '',
  eventMode: 'longconn' as 'longconn' | 'webhook',
})
const secretSet = ref(false)
const saving = ref(false)
const fieldError = ref('')
const appIdEl = ref<InstanceType<typeof Input> | null>(null)
const secretEl = ref<InstanceType<typeof Input> | null>(null)

/** 共用的预设提示词：两半都带它（D2：跟随 bot 记录） */
const sysPrompt = ref('')

let pollTimer: ReturnType<typeof setTimeout> | null = null
let tickTimer: ReturnType<typeof setInterval> | null = null

const countdown = computed(() => {
  const s = Math.max(0, remaining.value)
  const m = Math.floor(s / 60)
  return `${m}:${String(s % 60).padStart(2, '0')}`
})

function clearTimers() {
  if (pollTimer) clearTimeout(pollTimer)
  if (tickTimer) clearInterval(tickTimer)
  pollTimer = null
  tickTimer = null
}

/** 生成二维码。失效态的「重新生成」复用**同一个函数**，不长出第二条流程。 */
async function generateQr() {
  clearTimers()
  stage.value = 'gen'
  qrUrl.value = ''
  statusLine.value = '正在生成二维码…'
  try {
    const r = await startLarkReg(sysPrompt.value.trim())
    qrUrl.value = r.qrUrl
    ttl.value = r.expireIn
    remaining.value = r.expireIn
    stage.value = 'scan'
    statusLine.value = '等待你扫码确认…'
    startTicker()
    schedulePoll()
  } catch (e) {
    stage.value = 'dead'
    statusLine.value = `二维码生成失败：${e instanceof Error ? e.message : e}`
  }
}

function startTicker() {
  tickTimer = setInterval(() => {
    remaining.value -= 1
    if (remaining.value <= 0) {
      remaining.value = 0
      // 失效不是"提示一下但仍能用"：到点就是真的不能再扫了，
      // 所以必须同时给出出路（重新生成），否则用户卡在一个没有出口的界面上。
      clearTimers()
      stage.value = 'dead'
      statusLine.value = '二维码已失效'
    }
  }, 1000)
}

function schedulePoll() {
  pollTimer = setTimeout(async () => {
    if (stage.value !== 'scan') return
    let r: Awaited<ReturnType<typeof pollLarkReg>>
    try {
      r = await pollLarkReg()
    } catch {
      schedulePoll()
      return
    }
    if (r.state === 'pending') {
      schedulePoll()
      return
    }
    if (r.state === 'error') {
      clearTimers()
      stage.value = 'dead'
      statusLine.value = `❌ ${r.message}`
      return
    }
    // 成功：切到机器侧动作。不立刻关窗 —— 用户刚在手机上点了"确认"，
    // 至少要让他看到这一步被接住了。
    clearTimers()
    stage.value = 'busy'
    statusLine.value = r.hint && r.hint !== '已生效' ? `已授权 · ${r.hint}` : '已授权 · 凭据已保存并应用'
    emit('created')
    setTimeout(() => emit('close'), 1200)
  }, POLL_INTERVAL_MS)
}

async function loadConfigForManual() {
  try {
    const cfg = await getLarkConfig()
    form.value.appId = cfg.app_id ?? ''
    form.value.eventMode = (cfg.event_mode as 'longconn' | 'webhook') ?? 'longconn'
    secretSet.value = !!cfg.secret_set
  } catch {
    // 外网 / 未配置：表单留空，提交时会就地报错
  }
}

/** 缺字段不静默：就地报错 + 把焦点送到那个字段。 */
function focusField() {
  if (!form.value.appId.trim()) appIdEl.value?.focus()
  else if (!form.value.appSecret.trim() && !secretSet.value) secretEl.value?.focus()
}

async function submitManual() {
  fieldError.value = ''
  if (!form.value.appId.trim()) {
    fieldError.value = 'App ID 必填'
    focusField()
    return
  }
  if (!form.value.appSecret.trim() && !secretSet.value) {
    fieldError.value = 'App Secret 必填'
    focusField()
    return
  }
  saving.value = true
  try {
    await saveLarkConfig({
      appId: form.value.appId.trim(),
      appSecret: form.value.appSecret.trim(),
      verifyToken: form.value.verifyToken.trim(),
      encryptKey: form.value.encryptKey.trim(),
      eventMode: form.value.eventMode,
      sysPrompt: sysPrompt.value.trim(),
    })
    form.value.appSecret = ''
    emit('created')
    emit('close')
  } catch (e) {
    fieldError.value = e instanceof Error ? e.message : String(e)
  } finally {
    saving.value = false
  }
}

function close() {
  clearTimers()
  emit('close')
}

/** 打开即生成；关闭即清理（否则轮询会在关窗后继续跑）。 */
watch(
  () => props.open,
  (open) => {
    if (!open) {
      clearTimers()
      return
    }
    mode.value = 'scan'
    sysPrompt.value = ''
    stage.value = 'gen'
    void generateQr()
  },
)

watch(mode, (m) => {
  if (m === 'manual') {
    clearTimers()
    void loadConfigForManual()
  }
})

onUnmounted(clearTimers)

/** 描述跟着模式走：这段话是决策依据（"点下去会得到什么"），
 *  留一句与实际不符的就是在骗人点头。 */
const headline = computed(() => {
  if (mode.value === 'manual') {
    return props.first
      ? '填写凭据接入一个已有的飞书自建应用，并绑定为 飞书 · 管理员 —— 首个绑定自动确立，可发起隧道与调用 API。'
      : '填写凭据接入一个已有的飞书自建应用，并绑定为一台新机器人（成员身份，不含隧道与 API 权限）。'
  }
  return props.first
    ? '扫码后在你的飞书组织内新建一个自建应用，并绑定为 飞书 · 管理员 —— 首个绑定自动确立，可发起隧道与调用 API。'
    : '扫码后在你的飞书组织内新建一个自建应用，并绑定为一台新机器人（成员身份，不含隧道与 API 权限）。'
})
</script>

<template>
  <Modal
    :open="props.open"
    :title="first ? '创建飞书机器人' : '再创建一台飞书机器人'"
    :dismissable="stage !== 'busy'"
    @close="close"
  >
    <p class="mb-4 text-[12px] leading-[1.65] text-text-tertiary">{{ headline }}</p>

    <!-- ============ 扫码（默认） ============ -->
    <template v-if="mode === 'scan'">
      <div class="mb-3 flex flex-col items-center gap-3">
        <!-- 扫码区：四态都渲染同一块，只换遮罩。整块抽走会让弹层高度猛跳一下。 -->
        <div class="relative flex h-[196px] w-[196px] items-center justify-center rounded-lg border border-border bg-white">
          <img
            v-if="qrUrl"
            :src="tunnelQrUrl(qrUrl)"
            alt="飞书授权二维码"
            class="h-[196px] w-[196px] rounded-lg transition-opacity"
            :class="stage === 'dead' ? 'opacity-25' : ''"
          />
          <div v-else class="h-8 w-8 animate-spin rounded-full border-2 border-border border-t-accent" />

          <!-- gen / busy / dead 都用遮罩表达，且**只出图标、不出文字** ——
               状态文字一律归下面那条状态行（它是四态里都在的元素）。 -->
          <div
            v-if="stage === 'gen'"
            class="absolute inset-0 flex items-center justify-center rounded-lg bg-white/85"
          >
            <div class="h-8 w-8 animate-spin rounded-full border-2 border-border border-t-accent" />
          </div>
          <div
            v-else-if="stage === 'busy'"
            class="absolute inset-0 flex items-center justify-center rounded-lg bg-white/85 text-3xl text-success"
          >
            ✓
          </div>
          <div
            v-else-if="stage === 'dead'"
            class="absolute inset-0 flex items-center justify-center rounded-lg bg-white/70 text-3xl text-error"
          >
            ⌛
          </div>
        </div>

        <!-- 状态行：机器现在在干什么 -->
        <div
          class="text-center text-xs"
          :class="stage === 'dead' ? 'text-error' : stage === 'busy' ? 'text-success' : 'text-text-secondary'"
        >
          {{ statusLine }}
        </div>

        <!-- 指引链：你该干什么。只在可扫/已扫时出现，避免与状态行说同一件事。 -->
        <p v-if="stage === 'scan'" class="text-center text-[11px] text-text-tertiary">
          打开飞书 App → 扫一扫 → 确认授权
          <span class="ml-1 tabular-nums">（{{ countdown }} 后失效）</span>
        </p>
        <p v-else-if="stage === 'busy'" class="text-center text-[11px] text-text-tertiary">
          确认授权 → 保存凭据 → 应用配置
        </p>

        <Button v-if="stage === 'dead'" variant="primary" size="sm" @click="generateQr">重新生成</Button>
      </div>

      <!-- 预设提示词：位置在扫码区**之后** —— 它正好填在等待扫码的空档里，但不该挡住码 -->
      <label class="flex flex-col gap-1 border-t border-border-subtle pt-3 text-[11px] text-text-tertiary">
        预设提示词（可选）
        <Textarea
          v-model="sysPrompt"
          :rows="2"
          placeholder="这台机器人在应答时始终遵守的指令"
        />
      </label>

      <!-- 次要入口放在**页脚左侧、低强调**：它是"这条路走不通时"的旁门，不该跟主路径抢注意力。
           放页脚而不是扫码区里，是因为四态恒在 —— gen 与 dead 时最需要它。 -->
      <div class="mt-4 flex justify-start border-t border-border-subtle pt-3">
        <button
          class="text-[11px] text-text-tertiary underline-offset-2 transition-colors hover:text-text hover:underline"
          @click="mode = 'manual'"
        >
          手动配置已有应用
        </button>
      </div>
    </template>

    <!-- ============ 手动配置 ============ -->
    <form v-else class="flex flex-col gap-3" @submit.prevent="submitManual">
      <label class="flex flex-col gap-1 text-[11px] text-text-tertiary">
        接入方式
        <Select
          :model-value="form.eventMode"
          @update:model-value="(v) => (form.eventMode = v as 'longconn' | 'webhook')"
        >
          <option value="longconn">长连接 longconn（推荐，无需公网回调）</option>
          <option value="webhook">Webhook（需公网回调地址）</option>
        </Select>
      </label>
      <label class="flex flex-col gap-1 text-[11px] text-text-tertiary">
        App ID
        <Input
          ref="appIdEl"
          v-model="form.appId"
          placeholder="cli_xxxxxxxx"
          autocomplete="off"
        />
      </label>
      <label class="flex flex-col gap-1 text-[11px] text-text-tertiary">
        App Secret
        <Input
          ref="secretEl"
          v-model="form.appSecret"
          type="password"
          :placeholder="secretSet ? '留空则保持原值' : '必填'"
          autocomplete="new-password"
        />
      </label>
      <!-- webhook 的两个字段是**条件存在**，不是灰着占位 ——
           灰着的字段会让人怀疑自己是不是漏填了。 -->
      <template v-if="form.eventMode === 'webhook'">
        <label class="flex flex-col gap-1 text-[11px] text-text-tertiary">
          Verify Token
          <Input v-model="form.verifyToken" placeholder="留空则保持原值" autocomplete="off" />
        </label>
        <label class="flex flex-col gap-1 text-[11px] text-text-tertiary">
          Encrypt Key
          <Input v-model="form.encryptKey" placeholder="留空则保持原值" autocomplete="off" />
        </label>
      </template>

      <label class="flex flex-col gap-1 text-[11px] text-text-tertiary">
        预设提示词（可选）
        <Textarea
          v-model="sysPrompt"
          :rows="2"
          placeholder="这台机器人在应答时始终遵守的指令"
        />
      </label>

      <div class="mt-1 flex items-center justify-between gap-3 border-t border-border-subtle pt-3">
        <!-- 次要入口在页脚左侧：回到扫码这条主路径 -->
        <button
          type="button"
          class="text-[11px] text-text-tertiary underline-offset-2 transition-colors hover:text-text hover:underline"
          @click="mode = 'scan'"
        >
          ← 回到扫码
        </button>
        <div class="flex items-center gap-2">
          <span v-if="fieldError" class="text-[11px] text-error">{{ fieldError }}</span>
          <Button variant="primary" size="sm" type="submit" :loading="saving">保存并接入</Button>
        </div>
      </div>
    </form>
  </Modal>
</template>
