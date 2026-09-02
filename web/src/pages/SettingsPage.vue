<script setup lang="ts">
// Settings 页（方案 §25）：Connection / PWA / Tunnel / 飞书渠道 / About。
// 不把后端所有配置暴露到 UI。
import { ref } from 'vue'
import { useWebSocket } from '@/composables/useWebSocket'
import { useSessionStore } from '@/stores/session'
import { usePwa } from '@/composables/usePwa'
import { useAppStore } from '@/stores/app'
import { useAgentStore } from '@/stores/agent'
import { TunnelPanel, LarkConfigModal } from '@/features/settings'
import Button from '@/components/ui/Button.vue'
import Badge from '@/components/ui/Badge.vue'

const { connection, reconnect } = useWebSocket()
const sessionStore = useSessionStore()
const { canInstall, isStandalone, promptInstall } = usePwa()
const appStore = useAppStore()
const agentStore = useAgentStore()
const larkOpen = ref(false)

/** package.json version 由 Vite define 注入（构建期常量） */
const appVersion = __APP_VERSION__
</script>

<template>
  <div class="h-full overflow-y-auto">
    <div class="mx-auto max-w-2xl space-y-4 px-4 py-5 md:px-6">
      <h1 class="text-base font-semibold">设置</h1>

      <!-- 连接状态 -->
      <section class="rounded-lg border border-border bg-surface p-4">
        <h2 class="mb-2 text-sm font-medium">连接</h2>
        <div class="flex items-center justify-between">
          <span class="flex items-center gap-2 text-xs" :class="connection === 'connected' ? 'text-success' : 'text-warning'">
            <span class="status-breathe h-1.5 w-1.5 rounded-full bg-current" />
            {{ sessionStore.connectionLabel }}
          </span>
          <Button size="sm" @click="reconnect()">重连</Button>
        </div>
        <div v-if="appStore.auth" class="mt-2 text-xs text-muted">
          {{ appStore.auth.bound ? `已绑定：${appStore.auth.nickname || appStore.auth.openid || '飞书账号'}` : '未绑定飞书账号' }}
        </div>
      </section>

      <!-- Agents 展示：目录 + 实时会话统计（从 Task Store 派生） -->
      <section class="rounded-lg border border-border bg-surface p-4">
        <h2 class="mb-2 text-sm font-medium">Agents</h2>
        <div class="flex flex-col gap-2">
          <div
            v-for="info in agentStore.catalog"
            :key="info.id"
            class="flex items-center justify-between gap-2 rounded-lg border border-border/60 bg-background px-3 py-2"
          >
            <div class="min-w-0">
              <div class="text-sm font-medium">{{ info.name }}</div>
              <div class="truncate font-mono text-xs text-muted" :title="info.transport">{{ info.transport }}</div>
            </div>
            <div class="flex shrink-0 flex-col items-end gap-1">
              <Badge :tone="agentStore.agents.find((s) => s.agentId === info.id)?.online ? 'success' : 'neutral'">
                {{ agentStore.agents.find((s) => s.agentId === info.id)?.online ? '🟢 Online' : '离线' }}
              </Badge>
              <span class="text-xs text-muted">
                活跃 {{ agentStore.agents.find((s) => s.agentId === info.id)?.activeSessions ?? 0 }} ·
                总计 {{ agentStore.agents.find((s) => s.agentId === info.id)?.totalSessions ?? 0 }}
              </span>
            </div>
          </div>
        </div>
      </section>

      <!-- PWA 安装 -->
      <section class="rounded-lg border border-border bg-surface p-4">
        <h2 class="mb-2 text-sm font-medium">PWA</h2>
        <p class="text-xs text-muted">
          {{ isStandalone ? '已在独立窗口运行 ✓' : '安装到主屏幕，获得接近原生 App 的体验' }}
        </p>
        <Button v-if="canInstall" size="sm" variant="primary" class="mt-2" @click="promptInstall()">安装应用</Button>
      </section>

      <!-- 外网隧道（状态全员可读；控制仅飞书移动端） -->
      <section class="rounded-lg border border-border bg-surface p-4">
        <h2 class="mb-3 text-sm font-medium">外网隧道</h2>
        <TunnelPanel />
      </section>

      <!-- 飞书渠道配置（内网） -->
      <section class="rounded-lg border border-border bg-surface p-4">
        <h2 class="mb-2 text-sm font-medium">飞书渠道</h2>
        <p class="text-xs text-muted">扫码一键创建自建应用，或手动配置 App 凭据（仅内网可操作）</p>
        <Button size="sm" class="mt-2" @click="larkOpen = true">配置飞书渠道</Button>
      </section>

      <!-- 关于 -->
      <section class="rounded-lg border border-border bg-surface p-4 text-xs text-muted">
        <div class="mb-1 text-sm font-medium text-text">关于</div>
        <div>Pieqi · Agent 指挥台</div>
        <div>前端 v{{ appVersion }} · Go 单二进制部署</div>
      </section>
    </div>

    <LarkConfigModal :open="larkOpen" @close="larkOpen = false" />
  </div>
</template>
