<script setup lang="ts">
// Agent 卡片（方案 §22）：名称 / 在线状态 / Transport / 会话统计 / 能力
import Badge from '@/components/ui/Badge.vue'
import type { AgentInfo, AgentStats } from '@/types/agent'

defineProps<{ info: AgentInfo; stats: AgentStats }>()
</script>

<template>
  <div class="rounded-lg border border-border bg-surface p-4">
    <div class="flex items-center justify-between">
      <div class="flex items-center gap-2">
        <span class="text-sm font-semibold">{{ info.name }}</span>
        <Badge :tone="stats.online ? 'success' : 'neutral'">
          {{ stats.online ? '🟢 Online' : '离线' }}
        </Badge>
      </div>
      <span class="font-mono text-xs text-muted">{{ info.transport }}</span>
    </div>
    <div class="mt-3 grid grid-cols-2 gap-2 text-xs">
      <div class="rounded border border-border/60 bg-background px-2.5 py-2">
        <div class="text-muted">活跃会话</div>
        <div class="mt-0.5 text-base font-semibold">{{ stats.activeSessions }}</div>
      </div>
      <div class="rounded border border-border/60 bg-background px-2.5 py-2">
        <div class="text-muted">总会话</div>
        <div class="mt-0.5 text-base font-semibold">{{ stats.totalSessions }}</div>
      </div>
    </div>
    <div class="mt-3 flex flex-wrap gap-1.5">
      <span
        v-for="cap in info.capabilities"
        :key="cap"
        class="rounded-full border border-border px-2 py-0.5 text-xs text-muted"
      >
        ✓ {{ cap }}
      </span>
    </div>
  </div>
</template>
