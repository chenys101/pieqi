<script setup lang="ts">
// PWA 情境化安装提示（D5 定案 c）。
//
// 为什么不做成设置页里的一项（a / b 两个被否的选项）：
// **一个"时有时无"的按钮不该放进应当稳定的设置页** ——
// `canInstall` 由浏览器的 `beforeinstallprompt` 决定，iOS Safari 从不派发，
// 桌面端触发了也不适合放在这里；换位置不改变不稳定性，只是换了个组放。
// 而且安装是**一次性动作**，不是偏好。
//
// 所以：只在浏览器真的给了安装能力、且还没装、且用户没说"暂不"时，
// 在底栏上方浮出一条。其余任何时候它都不存在（`v-if`，不是 `v-show`）——
// 不存在的东西不会让人疑惑"为什么按钮是灰的"。
//
// iOS 的引导是设置页「数据与关于」里的一行**静态文字**（说明不是按钮 → 不会时有时无）。
import { usePwa } from '@/composables/usePwa'

const { shouldPrompt, promptInstall, dismiss } = usePwa()

async function onInstall() {
  const outcome = await promptInstall()
  // 「暂不」是浏览器的弹窗给的，用户在那里拒绝过就别再问了
  if (outcome === 'dismissed') dismiss()
}
</script>

<template>
  <!-- 只在移动端外壳里挂载：桌面端 `beforeinstallprompt` 同样会触发，
       但场景弱且会侵入桌面布局（D5 建议）。 -->
  <div
    v-if="shouldPrompt"
    class="flex shrink-0 items-center gap-2.5 border-t border-border bg-surface-subtle px-3 py-2.5"
  >
    <svg
      viewBox="0 0 24 24"
      class="h-4 w-4 shrink-0 text-accent"
      fill="none"
      stroke="currentColor"
      stroke-width="1.8"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <path d="M12 3v12M7 10l5 5 5-5M4 19h16" />
    </svg>
    <span class="min-w-0 flex-1 text-xs text-text-secondary">
      添加到主屏幕，像 App 一样打开
    </span>
    <button
      class="shrink-0 rounded-md bg-accent px-2.5 py-1 text-xs font-medium text-white transition-opacity hover:opacity-90"
      @click="onInstall"
    >
      安装
    </button>
    <button
      class="shrink-0 rounded-md px-1.5 py-1 text-xs text-muted transition-colors hover:bg-elevated hover:text-text"
      @click="dismiss"
    >
      暂不
    </button>
  </div>
</template>
