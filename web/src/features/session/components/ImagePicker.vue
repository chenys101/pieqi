<script setup lang="ts">
// ImagePicker：输入框内的「+」按钮 + 已选图片的缩略图条。
//
// 为什么自己写而不是复用某个上传组件：这里的图片**不进服务端存储**，而是
// 直接内联进 prompt 的 base64（见 imageAttach.ts 的三条纪律）。所以：
//   - 没有上传进度可言（不发请求，纯本地读取+压缩）；
//   - 没有"上传后的 URL"可引用（预览用本地 object URL / data URL）；
//   - 删除就是纯内存操作。
// 套一个上传组件反而要造假对象来满足它的接口。
import { computed, ref } from 'vue'
import {
  loadImageFile,
  loadClipboardImages,
  formatBytes,
  MAX_IMAGES,
} from '../imageAttach'
import type { PendingImage } from '@/types/image'

const props = withDefaults(
  defineProps<{
    /** 已选图片（v-model） */
    modelValue: PendingImage[]
    /** 不可选（运行中/发送中/该 agent 不收图） */
    disabled?: boolean
  }>(),
  { disabled: false },
)
const emit = defineEmits<{ 'update:modelValue': [value: PendingImage[]] }>()

const inputRef = ref<HTMLInputElement | null>(null)
const error = ref('')
const loading = ref(false)

/**
 * 粘贴失败的提示与选图失败**分开两个 ref**。
 *
 * 合成一个的话就会互相踩：粘贴一句"最多 8 张"，随后选一张格式不对的图，
 * 前一句被顶掉是对的；但反过来——选图报错后成功粘了一张——会把**早已过时**的
 * 选图错误重新显示出来。两个来源各自持有自己的错误，语义才不串。
 */
const pasteError = ref('')

/**
 * 「粘贴截图」这条提示只在桌面端显示。
 *
 * 判据是**输入形态**而不是设备名：这条路径的前提是"手边有一台能截图的 PC"，
 * 而触屏上长按输入框的粘贴菜单只会给文本，提示一句话就是纯噪音 ——
 * 手机屏幕高度尤其紧张（与"入口按钮本身两端都留着"是两回事：按钮点得到，
 * 粘贴提示用不上）。
 *
 * ⚠️ 这也只是**提示**：粘贴处理是绑在输入框 paste 事件上的，与它无关。
 * 触屏设备真带图粘贴（外接键盘、平板生产力模式）照样收得到 ——
 * 别把这条 showPasteHint 反过来当功能开关用。
 */
const showPasteHint = ref(false)
if (typeof window !== 'undefined' && window.matchMedia) {
  const mq = window.matchMedia('(hover: hover) and (pointer: fine)')
  showPasteHint.value = mq.matches
  // 二合一设备（Surface 这类）拔插键鼠会切过来，所以跟着变
  mq.addEventListener?.('change', (e) => (showPasteHint.value = e.matches))
}

/** 还能再选几张 */
const remaining = computed(() => MAX_IMAGES - props.modelValue.length)
const full = computed(() => remaining.value <= 0)

function openFileDialog() {
  if (props.disabled || full.value) return
  error.value = ''
  pasteError.value = ''
  inputRef.value?.click()
}

/**
 * 给父组件用的**命令式入口**：把粘贴交给 ImagePicker 自己处理。
 *
 * 为什么不让父组件（InterveneInput / NewTaskPage）接 paste 事件：
 * 图片是 ImagePicker 的 v-model，只有它知道"已经选了几张、还差几张"。
 * 父组件接 paste 就得自己算剩余额度、自己调 loadImageFile、自己维护错误文案
 * —— 等于把 ImagePicker 的逻辑抄一份，两份必然漂移。
 *
 * 用 `open()` 而不是子组件直接 `window.addEventListener('paste')` 的理由是
 * **作用域**：全局监听会在页面上任何输入框里粘贴都往这个 composer 塞图；
 * 绑在 textarea 的 paste 上（由 PromptInput 转发）才限定在"用户正在这个框里输入"。
 */
function open(data: DataTransfer) {
  if (props.disabled || full.value) return
  error.value = ''
  void addFromClipboard(data)
}

/**
 * 处理粘贴进来的图片。
 *
 * 判据是**文本里有没有图**，有图就本次粘贴只当图处理 —— 理由见下方 preventDefault
 * 的注释（截图工具会同时塞一段误发的路径文本）。
 */
async function addFromClipboard(data: DataTransfer) {
  loading.value = true
  try {
    const r = await loadClipboardImages(data, remaining.value)
    if (r.images.length > 0) emit('update:modelValue', [...props.modelValue, ...r.images])
    // 没取到图时**什么都不做**（也不清错误）：这是一次正常的文本粘贴，
    // 该由 textarea 自己处理，跟图片功能无关。
    if (r.images.length > 0 || r.error) pasteError.value = r.error ?? ''
  } finally {
    loading.value = false
  }
}

/**
 * 处理选中的文件。
 *
 * 逐张处理并**保留已成功的**：一张失败（比如混选了 HEIC）不该把整批都丢掉 ——
 * 用户选十张图，不该因为其中一张格式不对就重选九张。
 */
async function onFiles(e: Event) {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = '' // 复位，否则连选同一个文件不触发 change
  if (files.length === 0) return

  loading.value = true
  const accepted: PendingImage[] = []
  const errors: string[] = []
  let budget = remaining.value

  for (const f of files) {
    if (budget <= 0) {
      errors.push(`最多 ${MAX_IMAGES} 张，其余已忽略`)
      break
    }
    const r = await loadImageFile(f)
    if (r.ok) {
      accepted.push(r.image)
      budget--
    } else {
      errors.push(r.error)
    }
  }

  if (accepted.length > 0) emit('update:modelValue', [...props.modelValue, ...accepted])
  error.value = errors.join('；')
  loading.value = false
}

function remove(id: string) {
  emit(
    'update:modelValue',
    props.modelValue.filter((i) => i.id !== id),
  )
}

defineExpose({ open })
</script>

<template>
  <div class="flex min-w-0 flex-col gap-1.5">
    <!-- 缩略图条：只在有图时出现，避免空占位 -->
    <div v-if="modelValue.length > 0" class="flex flex-wrap items-center gap-1.5" data-testid="composer-images">
      <div
        v-for="img in modelValue"
        :key="img.id"
        class="group relative h-12 w-12 shrink-0 overflow-hidden rounded-[var(--radius-sm)] border border-border"
        :title="`${img.name}（${formatBytes(img.bytes)}）`"
      >
        <img :src="img.previewUrl" :alt="img.name" class="h-full w-full object-cover" />
        <!-- 删除：hover 才显形，但**触屏没有 hover** —— 故 focus-visible 也要显形，
             且按钮本身始终可点（只是透明），否则触屏用户根本删不掉。 -->
        <button
          type="button"
          class="absolute right-0 top-0 flex h-4 w-4 items-center justify-center rounded-bl bg-black/60 text-[10px] leading-none text-white opacity-0 transition-opacity hover:bg-black/80 focus-visible:opacity-100 group-hover:opacity-100"
          :title="`移除 ${img.name}`"
          :aria-label="`移除 ${img.name}`"
          :data-testid="`composer-image-remove-${img.id}`"
          @click="remove(img.id)"
          @mousedown.prevent
        >
          ×
        </button>
      </div>
      <span class="text-[11px] text-muted">{{ modelValue.length }}/{{ MAX_IMAGES }}</span>
    </div>

    <!-- 「+」按钮：与模型选择器同在 #lead 行。
         `@mousedown.prevent` 与 ModelPicker 同一个理由：选完图回来接着写，
         焦点不该被这个按钮抢走（抢走之后敲的字不会进输入框）。

         粘贴提示与按钮**同一行**（不再竖排到按钮下面）：
         竖排会把整个动作行撑高 16px 以上，并把右侧的模型选择器顶得与 + 不对齐
         （PC 详情页实测是一行高矮错位、提示还被输入条下沿裁掉）。 -->
    <div class="flex min-w-0 items-center gap-2">
      <button
        type="button"
        class="pointer-events-auto flex h-7 w-7 shrink-0 items-center justify-center rounded-[var(--radius-sm)] text-muted transition-colors hover:bg-elevated hover:text-text focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="disabled || full || loading"
        :title="full ? `最多 ${MAX_IMAGES} 张` : '添加图片'"
        aria-label="添加图片"
        data-testid="composer-add-image"
        @click="openFileDialog"
        @mousedown.prevent
      >
        <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
          <path d="M12 5v14M5 12h14" />
        </svg>
      </button>

      <!-- 粘贴截图的可发现性提示。
           功能做在输入框的 paste 上（零点击、直接可用），但**零点击等于零发现**：
           没有这行字，用户不会知道这里能贴图，只会去找"上传"。
           只在桌面端出现（判据与理由见 showPasteHint），且不可点、不占位。
           宽度不够时截断而不是换行 —— 它是一句辅助说明，不该改变动作行高度。 -->
      <p
        v-if="showPasteHint && modelValue.length === 0 && !error && !pasteError"
        class="hidden min-w-0 truncate text-[11px] leading-none text-text-tertiary md:block"
        title="截图后按 Ctrl+V 直接粘贴到输入框"
        data-testid="composer-paste-hint"
      >
        Ctrl+V 可粘贴截图
      </p>
    </div>

    <!-- 隐藏的 file input：accept 与后端白名单一致，让系统选择器先过滤一道 -->
    <input
      ref="inputRef"
      type="file"
      accept="image/png,image/jpeg,image/webp,image/gif"
      multiple
      class="hidden"
      data-testid="composer-image-input"
      @change="onFiles"
    />

    <p v-if="error" class="text-[11px] text-error" data-testid="composer-image-error">{{ error }}</p>

    <!-- 粘贴失败（类型不支持 / 超限）与选图失败共用一套文案样式，但**各自独立**：
         混成一个 ref 时，先报错的来源会被后到的成功操作顶掉或错误地复活（见 pasteError 注释）。 -->
    <p v-if="pasteError" class="text-[11px] text-error" data-testid="composer-paste-error">
      {{ pasteError }}
    </p>
  </div>
</template>
