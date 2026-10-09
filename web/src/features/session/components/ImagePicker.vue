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
import { loadImageFile, formatBytes, MAX_IMAGES, type PendingImage } from '../imageAttach'

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

/** 还能再选几张 */
const remaining = computed(() => MAX_IMAGES - props.modelValue.length)
const full = computed(() => remaining.value <= 0)

function openFileDialog() {
  if (props.disabled || full.value) return
  error.value = ''
  inputRef.value?.click()
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
</script>

<template>
  <div class="flex flex-col gap-1.5">
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
        >
          ×
        </button>
      </div>
      <span class="text-[11px] text-muted">{{ modelValue.length }}/{{ MAX_IMAGES }}</span>
    </div>

    <!-- 「+」按钮：与模型选择器同在 #lead 行 -->
    <button
      type="button"
      class="pointer-events-auto flex h-7 w-7 shrink-0 items-center justify-center rounded-[var(--radius-sm)] text-muted transition-colors hover:bg-elevated hover:text-text focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-40"
      :disabled="disabled || full || loading"
      :title="full ? `最多 ${MAX_IMAGES} 张` : '添加图片'"
      aria-label="添加图片"
      data-testid="composer-add-image"
      @click="openFileDialog"
    >
      <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
        <path d="M12 5v14M5 12h14" />
      </svg>
    </button>

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
  </div>
</template>
