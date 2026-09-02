<script setup lang="ts">
// Agents 页（方案 §22）：Agent 目录 + 实时会话统计（从 Task Store 派生）。
import { useAgentStore } from '@/stores/agent'
import { AgentCard } from '@/features/agent'
import EmptyState from '@/components/ui/EmptyState.vue'

const agentStore = useAgentStore()
</script>

<template>
  <div class="h-full overflow-y-auto">
    <div class="mx-auto max-w-4xl px-4 py-5 md:px-6">
      <h1 class="mb-3 text-base font-semibold">Agents</h1>
      <div v-if="agentStore.catalog.length" class="grid gap-3 md:grid-cols-2">
        <AgentCard
          v-for="info in agentStore.catalog"
          :key="info.id"
          :info="info"
          :stats="agentStore.agents.find((s) => s.agentId === info.id)!"
        />
      </div>
      <EmptyState v-else title="暂无可用 Agent" />
    </div>
  </div>
</template>
