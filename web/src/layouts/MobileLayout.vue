<script setup lang="ts">
// 移动布局（SPEC §6 断点表 / D5 定案）。
//
// 结构：内容区 + 情境化安装提示 + 底栏 3 项。
//
// ⚠️ **底栏是替代抽屉，不是补充。**
// 原型在移动端直接把侧栏 `display:none`，改用底栏导航（`index.html:211`）——
// 所以这里不再渲染 AppSidebar、不再有汉堡按钮、不再有抽屉遮罩。
// 原来"抽屉里装 PC 侧栏"的做法把一整套桌面导航树塞进 80vw，
// 既看不见当前在哪一项（侧栏是常驻物，它的高亮依赖"一直在那儿"），
// 又要先付出一次点击才能导航。
//
// 任务浏览能力没有丢：侧栏树换容器渲染成整屏的任务浏览器页，
// 由底栏「任务」进入（数据源与折叠状态都与侧栏共用，见 stores/taskTree）。
//
// 页面标题由各页自己的头部承担 —— 底栏不承担"当前页叫什么"，
// 它只回答"我还能去哪"，这两件事分开才不会互相挤占。
import BottomNav from '@/components/ui/BottomNav.vue'
import InstallPrompt from '@/components/ui/InstallPrompt.vue'
</script>

<template>
  <div class="flex h-dvh flex-col overflow-hidden bg-background text-text">
    <main class="min-h-0 flex-1 overflow-hidden">
      <slot />
    </main>

    <!-- 安装提示在底栏**上方**同一层：它是一次性的邀约，不该盖住内容，
         也不该出现在内容里被滚动带走。 -->
    <InstallPrompt />
    <BottomNav />
  </div>
</template>
