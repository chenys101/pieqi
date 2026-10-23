<script setup lang="ts">
// 响应式布局切换（方案 §27）：≤768px 走 Mobile（底栏 3 项），其余走 Desktop（侧栏 + Main）。
import { useResponsive } from '@/composables/useResponsive'
import DesktopLayout from './DesktopLayout.vue'
import MobileLayout from './MobileLayout.vue'

//
// `reset-view`（默认 true）：用 `v-if` 在**换档时重建**内部视图。
//
// ★★ 为什么必须是重建，而不是 `v-if` + `<slot/>` 直接复用（2026-10-24 排查定案）
//
//   旧写法是两个分支各自放一次 `<slot/>`。Vue 在编译时会**按位置给 slot 分配 key**：
//   两处 `<slot/>` 是不同位置 ⇒ 指向同一个 vnode（`key: 0` 与 `key: 1`）。
//   于是从移动档切到桌面档（或反过来）时，同一个 vnode 换了父节点、同时又换了 key，
//   渲染器会走 `switch` 分支里的「patch(newVNode, newVNode)」兜底 ——
//   新旧是同一个对象，patch 认为"没差别"，**旧子树原地留着、新分支却是空的**。
//   表现（实测，375→1440 与 1440→375 都复现）：
//     · 整页空白（`#app` 里有 `<main>`，但里面一个子节点都没有）；
//     · 或者反过来——切到桌面档之后，**底栏还在**、侧栏也出来了（两份壳各留一半）。
//   两条都是"不报错、只白屏/错位"，而平板上旋转屏幕、手机上转横屏、
//   桌面拖窄窗口都会踩到。
//
//   修法就是给内部视图一个**稳定 key**（MOBILE / DESKTOP）：换档时 key 变 ⇒
//   旧子树被卸载、新子树被挂载，不存在"同一个 vnode 换位置"的情况。
//
//   `reset-view=false` 是给 **e2e 夹具**用的（夹具页外面自己套了一层 AppLayout，
//   见 web/e2e/fixtures/page-fixture.ts）。生产代码没有任何地方传它。
defineProps<{ resetView?: boolean }>()

const { isMobile } = useResponsive()
</script>

<template>
  <MobileLayout v-if="isMobile" :key="resetView === false ? 'view' : 'mobile'">
    <slot />
  </MobileLayout>
  <DesktopLayout v-else :key="resetView === false ? 'view' : 'desktop'">
    <slot />
  </DesktopLayout>
</template>
