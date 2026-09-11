<script setup lang="ts">
// 移动端任务浏览器页里的一个项目手风琴（SPEC §6.1.4 / §6.1.5）。
//
// 与侧栏树是**同一棵树换了个容器**：数据源都是 `taskStore.groupsByProject`，
// 折叠状态都来自 `stores/taskTree`。差别只在渲染密度 ——
// 侧栏 240px 只放名称（§6.1.2：再插一列会把 80% 的名字截断），
// 这里是整屏，所以补回侧栏放不下的时间与状态徽章。
import { ref } from 'vue'
import type { BrowserGroup } from '../types'
import { STATUS_DOT, STATUS_LABELS } from '@/utils/format'
import { timeAgo } from '@/utils/date'

const props = defineProps<{ group: BrowserGroup }>()
const emit = defineEmits<{ toggle: []; remove: [id: string] }>()

// 删除入口：**必须常驻可见**（不能靠 hover 显形），因为这一页只有移动端可达，
// 而移动端没有 hover —— 悬浮才出现的按钮在那里等于不存在。
// 确认用内联确认条而非原生 confirm()：原生弹窗是模态的，会盖住用户正看着的那一行，
// 而这个操作恰恰需要他确认删的是不是**它**。
const pendingDelete = ref('')

function doDelete(id: string) {
  pendingDelete.value = ''
  emit('remove', id)
}
</script>

<template>
  <div
    class="tb-group"
    :class="{ 'is-open': props.group.open, 'is-dim': props.group.dim }"
  >
    <!-- 项目行。收起态是这一页的默认形态：树高恒为「项目数」行，
         不随任务数膨胀（§6.1.1 同一条理由，只是这里的容器是整屏）。 -->
    <button
      class="tb-head"
      type="button"
      :aria-expanded="props.group.open"
      :disabled="props.group.dim"
      :title="props.group.path"
      @click="emit('toggle')"
    >
      <svg
        viewBox="0 0 24 24"
        class="chev"
        fill="none"
        stroke="currentColor"
        stroke-width="2.5"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M9 6l6 6-6 6" />
      </svg>
      <span class="min-w-0 flex-1 truncate text-[13.5px] font-semibold text-text">{{ props.group.name }}</span>
      <span class="shrink-0 rounded-full border border-border-subtle bg-surface-muted px-1.5 py-px text-[11.5px] font-semibold tabular-nums text-text-secondary">
        {{ props.group.tasks.length }}
      </span>
      <!-- 状态聚合在移动端隐藏（§6.1.5）：它会跟项目名抢宽度。
           这一页本就只有移动端可达，但桌面深链进来也能看，故按断点取舍。 -->
      <span class="agg hidden shrink-0 text-xs tabular-nums text-text-tertiary md:inline">
        {{ props.group.agg || '无匹配任务' }}
      </span>
    </button>

    <div class="tb-body">
      <div class="inner">
        <div v-for="t in props.group.tasks" :key="t.id" class="tb-item">
          <div class="tb-row">
            <RouterLink
              :to="`/sessions/${t.id}`"
              class="tb-main"
              :title="t.prompt"
            >
              <span class="h-2 w-2 shrink-0 rounded-full" :class="STATUS_DOT[t.status]" />
              <span class="min-w-0 flex-1">
                <span class="block truncate text-[13px] font-medium text-text">{{ t.title }}</span>
                <span class="mt-0.5 block truncate text-[11.5px] text-text-tertiary">
                  {{ t.agent || 'Claude Code' }} · 更新于 {{ timeAgo(t.updatedAt) }}前
                </span>
              </span>
              <span class="shrink-0 text-[11.5px] font-medium text-text-tertiary">
                {{ STATUS_LABELS[t.status] }}
              </span>
            </RouterLink>
            <button
              type="button"
              class="tb-del"
              :aria-label="`删除任务 ${t.title}`"
              @click="pendingDelete = t.id"
            >
              ×
            </button>
          </div>
          <!-- 内联确认条：长在这一行下面，用户看着那一行确认 -->
          <div v-if="pendingDelete === t.id" class="tb-confirm">
            <span class="min-w-0 flex-1 truncate text-[11.5px] text-error">删除「{{ t.title }}」？不可恢复。</span>
            <button type="button" class="tb-act is-danger" @click="doDelete(t.id)">删除</button>
            <button type="button" class="tb-act" @click="pendingDelete = ''">取消</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 项目分组卡片：与原型 .tsk-group 同一套观感 */
.tb-group {
  border: 1px solid rgb(var(--color-border));
  border-radius: 12px;
  background: rgb(var(--color-surface));
  overflow: hidden;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.tb-group:hover { border-color: rgb(var(--color-border-strong)); }
.tb-group.is-open { box-shadow: 0 1px 3px rgba(16, 18, 27, 0.06); }

/* 筛选后无匹配任务：置灰但**不从列表消失** ——
   保持"列表永远展示所有项目"的稳定性，如实告知无匹配（§6.1.4） */
.tb-group.is-dim { opacity: 0.52; }

.tb-head {
  display: flex;
  align-items: center;
  gap: 9px;
  width: 100%;
  padding: 12px 14px;
  background: transparent;
  border: 0;
  font-family: inherit;
  text-align: left;
  cursor: pointer;
}
.tb-head:hover { background: rgb(var(--color-surface-subtle)); }
.tb-head:disabled { cursor: default; }
.tb-head:disabled:hover { background: transparent; }
.tb-head .chev {
  width: 13px;
  height: 13px;
  flex: none;
  color: rgb(var(--color-text-tertiary));
  transition: transform 0.15s ease-out;
}
.tb-group.is-open .tb-head .chev { transform: rotate(90deg); }

/* 折叠动画（§6.1.3）：grid-template-rows 0fr → 1fr，
   **不读 scrollHeight、不写内联 max-height** —— 列表是动态渲染的，
   按像素测量会在筛选后失准；0fr→1fr 交给浏览器排版，天然自适应。
   收起态延迟到动画结束才 visibility:hidden，否则被裁掉的任务行
   仍在可聚焦序列里，键盘 Tab 会跳进看不见的元素。 */
.tb-body {
  display: grid;
  grid-template-rows: 0fr;
  visibility: hidden;
  border-top: 1px solid transparent;
  transition: grid-template-rows 0.18s ease-out, visibility 0s linear 0.18s, border-color 0.18s;
}
.tb-group.is-open .tb-body {
  grid-template-rows: 1fr;
  visibility: visible;
  border-top-color: rgb(var(--color-border-subtle));
  transition: grid-template-rows 0.18s ease-out, visibility 0s, border-color 0.18s;
}
.tb-body > .inner { overflow: hidden; min-height: 0; }

.tb-item { border-bottom: 1px solid rgb(var(--color-border-subtle)); }
.tb-item:last-child { border-bottom: 0; }

.tb-row { display: flex; align-items: stretch; }
.tb-row:hover { background: rgb(var(--color-surface-subtle)); }

.tb-main {
  display: flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
  flex: 1;
  padding: 10px 6px 10px 14px;
  text-decoration: none;
}

/* 删除：常驻可见（无 hover 就永远不出现的按钮等于不存在），
   但用最弱的颜色 —— 它是次要操作，不该抢任务名的注意力。
   单独一句 padding 是为了把点击区拉到 44px 高的拇指友好尺寸。 */
.tb-del {
  flex: none;
  width: 42px;
  border: 0;
  background: transparent;
  color: rgb(var(--color-text-tertiary));
  font-size: 16px;
  line-height: 1;
  cursor: pointer;
  transition: color 0.15s, background 0.15s;
}
.tb-del:hover { color: rgb(var(--color-error)); background: rgb(var(--color-error) / 0.08); }

.tb-confirm {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 12px 10px;
  padding: 7px 9px;
  border: 1px solid rgb(var(--color-error) / 0.4);
  border-radius: 8px;
  background: rgb(var(--color-error) / 0.05);
}
.tb-act {
  flex: none;
  padding: 3px 8px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  font-size: 11.5px;
  color: rgb(var(--color-text-secondary));
  cursor: pointer;
}
.tb-act:hover { background: rgb(var(--color-elevated)); }
.tb-act.is-danger {
  font-weight: 600;
  color: rgb(var(--color-error));
}
.tb-act.is-danger:hover { background: rgb(var(--color-error) / 0.12); }
</style>
