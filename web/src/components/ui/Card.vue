<script setup lang="ts">
// 卡片容器
withDefaults(defineProps<{ padded?: boolean }>(), { padded: true })
</script>

<template>
  <div
    class="rounded-lg border border-border bg-surface"
    :class="padded ? 'p-4' : ''"
  >
    <slot />
  </div>
</template>
