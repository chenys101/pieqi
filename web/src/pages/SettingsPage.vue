<script setup lang="ts">
// Settings 页（SPEC §5.4）：只放「跨任务 / 跨项目 / 跨 agent 的全局偏好」。
// 属于某个对象的配置**就地**设置 —— 这是防止设置变垃圾桶的唯一有效手段。
//
// 四组按「能减少多少负担」排序，不按「有多无害」：
//   ① 审批与自动化   ② 连接与账号   ③ 外观与偏好   ④ 数据与关于
//
// 组① 排最上，是因为它直接决定你要被通知多少次 —— 这是本页最值钱的一组，
// 不是"通用设置"。惯例把外观放最上，那是在给不痛不痒的偏好排序。
//
// D5 定案：原先那个 PWA 安装段已移除 —— 一个"时有时无"的按钮不该占着设置项的位置
// （canInstall 由浏览器的 beforeinstallprompt 决定，iOS Safari 更是从不触发）。
// 安装引导改由情境化提示承担（切片 6）；iOS 的说明是**一行纯文字**留在这里
// （说明不是按钮 → 不会时有时无）。
//
// 组③ 只落**主题**与**减弱动效**两项。原型同组还有「语言」「时区」，本页不放：
// 产品没有 i18n、时区也是服务端本地时区 —— 放上去就是两个点不动的控件。
import { onMounted, ref } from 'vue'
import { useWebSocket } from '@/composables/useWebSocket'
import { useSessionStore } from '@/stores/session'
import { useAppStore } from '@/stores/app'
import { useAgentStore } from '@/stores/agent'
import { useNotificationStore } from '@/stores/notification'
import { useTheme, type ThemePref } from '@/composables/useTheme'
import { useReduceMotion } from '@/composables/useReduceMotion'
import { TunnelPanel, RobotList } from '@/features/settings'
import {
  getSettings,
  patchSettings,
  downloadDiagnostics,
  type AppSettings,
} from '@/services/api/settings'
import Badge from '@/components/ui/Badge.vue'
import Button from '@/components/ui/Button.vue'
import SetGroup from '@/components/ui/SetGroup.vue'
import SettingRow from '@/components/ui/SettingRow.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import Switch from '@/components/ui/Switch.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'

const { connection, reconnect } = useWebSocket()
const sessionStore = useSessionStore()
const appStore = useAppStore()
const agentStore = useAgentStore()
const notify = useNotificationStore()

// 主题与动效的状态都在 composable 里（module-level 单例 + localStorage），
// 所以这里不用 v-model 直接绑 —— 赋值必须走 setter，否则不会持久化、也不会 apply。
const { pref: themePref, setPref: setThemePref } = useTheme()
const { reduce: reduceMotion, set: setReduceMotion } = useReduceMotion()

const themeOptions = [
  { value: 'system', label: '跟随系统' },
  { value: 'light', label: '浅色' },
  { value: 'dark', label: '深色' },
]

function onThemeChange(v: string) {
  setThemePref(v as ThemePref)
}

// ---------- ① 审批与自动化 + ④ 数据 ----------

const settings = ref<AppSettings | null>(null)

onMounted(async () => {
  try {
    settings.value = await getSettings()
  } catch {
    // 后端未接线 / 网络失败：开关保持 disabled，不给"点了没反应"的控件
  }
})

/**
 * 乐观更新：先改本地再发请求，失败则回滚。
 * 开关是"立刻应该看到结果"的控件，等一个往返回去再动会显得卡；
 * 而失败的代价只是回滚一次位置，比每次点击都等 200ms 划算。
 */
async function apply(patch: Partial<AppSettings>) {
  if (!settings.value) return
  const prev = { ...settings.value }
  settings.value = { ...prev, ...patch }
  try {
    settings.value = await patchSettings(patch)
  } catch (e) {
    settings.value = prev
    notify.error(e instanceof Error ? e.message : '保存失败')
  }
}

const retentionOptions = [
  { value: 1000, label: '最近 1000 条' },
  { value: 5000, label: '最近 5000 条' },
  { value: 0, label: '全部保留' },
]

function onRetentionChange(e: Event) {
  const v = Number((e.target as HTMLSelectElement).value)
  void apply({ event_retention: v })
}

const exporting = ref(false)
async function onExport() {
  exporting.value = true
  try {
    const name = await downloadDiagnostics()
    notify.success(`已导出 ${name}`)
  } catch (e) {
    notify.error(e instanceof Error ? e.message : '导出失败')
  } finally {
    exporting.value = false
  }
}

/** package.json version 由 Vite define 注入（构建期常量） */
const appVersion = __APP_VERSION__
</script>

<template>
  <div class="h-full overflow-y-auto">
    <div class="mx-auto flex w-full max-w-[760px] flex-col gap-4 px-5 pt-[18px] pb-10">
      <h1 class="text-base font-semibold">设置</h1>

      <!-- ============ ① 审批与自动化 ============
           排最上，因为它最能减少你要点的次数。排序依据是「能减多少负担」，
           不是「有多无害」—— 把外观放最上是在给不痛不痒的偏好排序。 -->
      <SetGroup title="审批与自动化" why="按风险分级决定要不要打断你">
        <SettingRow
          label="L0 只读探测"
          desc="读文件、检索代码。不产生任何改动，自动放行没有风险。"
        >
          <Switch
            :model-value="settings?.auto_approve_l0 ?? false"
            :disabled="!settings"
            label="L0 只读探测自动放行"
            @update:model-value="(v) => apply({ auto_approve_l0: v })"
          />
        </SettingRow>

        <SettingRow
          label="L1 写入"
          desc="改代码、写文件。每轮都有 Checkpoint 可回退，因此也允许自动放行。"
        >
          <Switch
            :model-value="settings?.auto_approve_l1 ?? false"
            :disabled="!settings"
            label="L1 写入自动放行"
            @update:model-value="(v) => apply({ auto_approve_l1: v })"
          />
        </SettingRow>

        <!-- L2 / L3 是**锁定行**，不是"默认关着的开关" ——
             能把自己配进坑里的选项，不要做成选项（SPEC §5.4 控件约定）。
             locked 让背景下沉、标题降级，但不压透明度：压透明度会把
             "为什么不能改"这行字一起淡化，而它恰恰是最该读清的。 -->
        <SettingRow
          desc="装依赖、跑脚本 —— 影响会溢出到工作区之外。必须你确认。"
          locked
        >
          <template #label>L2 执行命令 <Badge tone="neutral">不可配置</Badge></template>
          <Switch :model-value="false" disabled label="L2 执行命令，不可配置" />
        </SettingRow>

        <SettingRow
          desc="删除、覆盖、强制推送。除内联二次确认外没有别的路径。"
          locked
        >
          <template #label>L3 破坏性操作 <Badge tone="neutral">不可配置</Badge></template>
          <Switch :model-value="false" disabled label="L3 破坏性操作，不可配置" />
        </SettingRow>

        <SettingRow
          label="免打扰时段"
          desc="此时段内审批只排队、不推送，打开界面时集中呈现。"
          stack
        >
          <Switch
            :model-value="settings?.dnd_enabled ?? false"
            :disabled="!settings"
            label="免打扰时段"
            @update:model-value="(v) => apply({ dnd_enabled: v })"
          />
          <!-- 起止**始终可见**：关着的时候也要能看出"打开后会是什么时段"，
               否则用户是在盲开一个开关。 -->
          <div class="flex items-center gap-1.5 text-[11.5px] text-text-tertiary">
            <Input
              type="time"
              size="sm"
              class="font-mono"
              :model-value="settings?.dnd_start ?? '22:00'"
              :disabled="!settings?.dnd_enabled"
              aria-label="免打扰开始时间"
              @change="(e: Event) => apply({ dnd_start: (e.target as HTMLInputElement).value })"
            />
            <span>–</span>
            <Input
              type="time"
              size="sm"
              class="font-mono"
              :model-value="settings?.dnd_end ?? '08:00'"
              :disabled="!settings?.dnd_enabled"
              aria-label="免打扰结束时间"
              @change="(e: Event) => apply({ dnd_end: (e.target as HTMLInputElement).value })"
            />
          </div>
        </SettingRow>
      </SetGroup>

      <!-- ============ ② 连接与账号 ============ -->
      <SetGroup title="连接与账号" why="状态 · 机器人 · 工作区">
        <SettingRow>
          <template #label>实时连接</template>
          <template #desc>
            {{ sessionStore.connectionLabel }}
            <template v-if="appStore.auth">
              ·
              {{
                appStore.auth.bound
                  ? `已绑定 ${appStore.auth.nickname || appStore.auth.openid || '飞书账号'}`
                  : '未绑定飞书账号'
              }}
            </template>
          </template>
          <span
            class="flex items-center gap-1.5 text-xs"
            :class="connection === 'connected' ? 'text-success' : 'text-warning'"
          >
            <span class="status-breathe h-1.5 w-1.5 rounded-full bg-current" />
            {{ connection === 'connected' ? '正常' : '重连中' }}
          </span>
          <Button size="sm" @click="reconnect()">重连</Button>
        </SettingRow>

        <!-- IM 机器人：绑定的单位是**机器人**，不是渠道 —— 一台机器人 = 一个 IM 身份。
             只有这一层，不再套"智能体"这个中间名（同一件事两个名字，用户只会以为它们有区别）。
             渠道是机器人的**属性**（写在名字前半截），不是**上级分类** —— 列表不按渠道分组。
             管理员不是要你先想清楚的配置项，而是**首个绑定自动确立**的角色。 -->
        <SettingRow
          label="IM 机器人"
          desc="审批与回执由机器人对外收发。一台机器人对应一个 IM 身份；首个绑定的自动成为管理员 —— 隧道与 API 特权属于该机器人绑定的飞书账号，其余机器人不受理隧道命令。"
        />
        <RobotList />

        <SettingRow label="Agent 引擎" desc="已接入的 coding agent，以及各自的实时会话数。">
          <span class="text-xs text-text-tertiary">{{ agentStore.catalog.length }} 个</span>
        </SettingRow>
        <div class="flex flex-col gap-2 px-[14px] pb-3">
          <div
            v-for="info in agentStore.catalog"
            :key="info.id"
            class="flex items-center justify-between gap-2 rounded-md border border-border-subtle bg-background px-3 py-2"
          >
            <div class="min-w-0">
              <div class="text-[13px] text-text">{{ info.name }}</div>
              <div class="truncate font-mono text-[11px] text-text-tertiary" :title="info.transport">
                {{ info.transport }}
              </div>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <Badge
                :tone="agentStore.agents.find((s) => s.agentId === info.id)?.online ? 'success' : 'neutral'"
              >
                {{ agentStore.agents.find((s) => s.agentId === info.id)?.online ? '在线' : '离线' }}
              </Badge>
              <span class="text-[11px] text-text-tertiary">
                活跃 {{ agentStore.agents.find((s) => s.agentId === info.id)?.activeSessions ?? 0 }} ·
                总计 {{ agentStore.agents.find((s) => s.agentId === info.id)?.totalSessions ?? 0 }}
              </span>
            </div>
          </div>
        </div>

        <SettingRow label="外网隧道" desc="让手机在飞书里访问本机服务；状态全员可读，控制仅限飞书移动端。">
        </SettingRow>
        <div class="px-[14px] pb-3">
          <TunnelPanel />
        </div>

        <SettingRow label="工作区" desc="任务与 worktree 的落盘位置。">
          <span class="font-mono text-xs text-text-tertiary">~/.pieqi</span>
        </SettingRow>
      </SetGroup>

      <!-- ============ ③ 外观与偏好 ============ -->
      <SetGroup title="外观与偏好" why="主题 · 动效">
        <SettingRow
          label="主题"
          desc="浅色与深色是两套独立设计；跟随系统会随环境自动切换。"
          stack
        >
          <!-- 不直接用 v-model：赋值必须走 setter 才会持久化并应用到 <html> -->
          <SegmentedControl
            :model-value="themePref"
            :options="themeOptions"
            aria-label="主题"
            @update:model-value="onThemeChange"
          />
        </SettingRow>

        <SettingRow
          label="减弱动效"
          desc="关掉折叠与切换动画。系统开启「减少动态效果」时自动生效。"
        >
          <Switch
            :model-value="reduceMotion"
            label="减弱动效"
            @update:model-value="setReduceMotion"
          />
        </SettingRow>
      </SetGroup>

      <!-- ============ ④ 数据与关于 ============ -->
      <SetGroup title="数据与关于" why="保留 · 导出 · 版本">
        <SettingRow
          label="事件保留上限"
          desc="超出后从最旧的开始丢弃，避免长任务把内存吃掉。"
        >
          <!-- 固定宽壳：Select 根元素是 w-full，在 SettingRow 右侧会吞掉整行 -->
          <div class="w-32">
            <Select
              size="sm"
              :model-value="settings?.event_retention ?? 5000"
              :disabled="!settings"
              aria-label="事件保留上限"
              @change="onRetentionChange"
            >
              <option v-for="o in retentionOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
            </Select>
          </div>
        </SettingRow>

        <SettingRow label="诊断日志" desc="导出最近 7 天日志用于排查问题。">
          <Button size="sm" :loading="exporting" @click="onExport">导出</Button>
        </SettingRow>

        <SettingRow label="版本">
          <span class="font-mono text-xs text-text-tertiary">v{{ appVersion }}</span>
        </SettingRow>
        <SettingRow label="部署形态" desc="Go 单二进制，前端资源已内嵌，无独立前端服务。"> </SettingRow>
        <!-- iOS 安装说明：**一行纯文字**，不是按钮。说明不会时有时无。 -->
        <SettingRow
          label="添加到主屏幕"
          desc="iPhone / iPad：Safari 里点分享 → 添加到主屏幕，之后从主屏图标进入，没有地址栏。Android 会在移动端底部按需提示一次；桌面端不提示（场景弱，且会侵入桌面布局）。"
        />
      </SetGroup>

      <p class="text-xs leading-[1.65] text-text-tertiary">
        这里只放跨任务、跨项目的全局偏好。属于某个任务或项目的设置，就在那个任务 / 项目上改 ——
        agent 的权限模式在会话详情，项目默认 agent 在项目行。
      </p>
    </div>
  </div>
</template>
