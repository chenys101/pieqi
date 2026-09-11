<script setup lang="ts">
// IM 机器人列表（SPEC §5.4.1）。
//
// 绑定的单位是**机器人**：一台机器人 = 一个 IM 身份。只有这一层 ——
// 不再有"智能体"这个中间名（同一件事两个名字，用户只会以为它们有区别）。
//
// 形态是**列表**而不是单行卡，尽管现在常常只有一台：列表形态是为多机器人准备的，
// 单行形态届时必须重写结构，列表形态只需多一行。**为已知的下一步留结构。**
//
// 渠道是机器人的**属性**（写在名字前半截），不是**上级分类** ——
// 所以这里不做渠道分组：一分组就等于把刚删掉的那层换个名字加回来。
//
// 管理员位有**两个状态，同一张卡**（不是两张卡）：
//   未配置 = 虚线框 + 空心点 + 「未配置」徽章 + 「一键创建」
//   已配置 = 实线框 + 绿点 + 能力标记 + 「配置」
// 差别说的是"要不要你动手"，不是好或坏。已配置态**刻意没有「管理员」徽章**：
// 身份已由名字里的「管理员」承载，再挂一枚就是同一句话说两遍；
// 徽章留给**能力**（隧道 / API）—— 那是名字里读不出来的。
import { computed, onMounted, ref } from 'vue'
import Button from '@/components/ui/Button.vue'
import Badge from '@/components/ui/Badge.vue'
import CapTag from '@/components/ui/CapTag.vue'
import { listBots, updateBot, deleteBot, type BotDto } from '@/services/api/bots'
import { getLarkStatus } from '@/services/api/larkreg'
import RobotCreateModal from './RobotCreateModal.vue'

const bots = ref<BotDto[]>([])
const loading = ref(true)
const loadError = ref('')
/** 外网：/api/larkreg 一律 403，所以「创建 / 配置」这些动作在这里注定失败 */
const external = ref(false)

const createOpen = ref(false)

// 就地编辑（属于某台机器人的配置，就地设置 —— 这就是设置页那条判据的落点）
const editingId = ref('')
const editName = ref('')
const editPrompt = ref('')
const savingEdit = ref(false)
const editError = ref('')
/** 二次确认用内联确认条，**不用原生 confirm()**（SPEC 已禁） */
const confirmUnbind = ref(false)

const admin = computed(() => bots.value.find((b) => b.role === 'admin') ?? null)
const members = computed(() => bots.value.filter((b) => b.role !== 'admin'))
const first = computed(() => bots.value.length === 0)

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const r = await listBots()
    bots.value = r.bots ?? []
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
  // 内网判定：/api/larkreg/status 挂在 BindOpGate 后面，外网一律 403
  try {
    const st = await getLarkStatus()
    external.value = st.status === 403
  } catch {
    external.value = false
  }
}

function startEdit(b: BotDto) {
  editingId.value = b.id
  editName.value = b.name
  editPrompt.value = b.sys_prompt ?? ''
  editError.value = ''
  confirmUnbind.value = false
}

function cancelEdit() {
  editingId.value = ''
  editError.value = ''
  confirmUnbind.value = false
}

async function saveEdit() {
  const id = editingId.value
  if (!id) return
  if (!editName.value.trim()) {
    editError.value = '名称不能为空'
    return
  }
  savingEdit.value = true
  editError.value = ''
  try {
    const next = await updateBot(id, {
      name: editName.value.trim(),
      sysPrompt: editPrompt.value,
    })
    const i = bots.value.findIndex((b) => b.id === id)
    if (i >= 0) bots.value[i] = next
    cancelEdit()
  } catch (e) {
    editError.value = e instanceof Error ? e.message : String(e)
  } finally {
    savingEdit.value = false
  }
}

async function doUnbind() {
  const id = editingId.value
  if (!id) return
  savingEdit.value = true
  editError.value = ''
  try {
    await deleteBot(id)
    bots.value = bots.value.filter((b) => b.id !== id)
    cancelEdit()
  } catch (e) {
    editError.value = e instanceof Error ? e.message : String(e)
  } finally {
    savingEdit.value = false
  }
}

async function onCreated() {
  await load()
}

onMounted(load)

defineExpose({ reload: load })
</script>

<template>
  <div class="flex flex-col gap-2 px-[14px] pb-3">
    <div v-if="loading" class="text-xs text-text-tertiary">加载机器人列表…</div>
    <div v-else-if="loadError" class="text-xs text-error">列表加载失败：{{ loadError }}</div>

    <template v-else>
      <!-- ============ 管理员位：同一张卡的两个状态 ============ -->
      <div
        class="agent-item flex items-start gap-2.5 rounded-lg border px-3 py-2.5"
        :class="admin ? 'border-border bg-surface' : 'border-dashed border-border-strong bg-background'"
        :data-state="admin ? 'ready' : 'unset'"
      >
        <span
          class="mt-[5px] h-2 w-2 shrink-0 rounded-full"
          :class="admin ? 'bg-success' : 'border border-border-strong bg-transparent'"
        />
        <div class="min-w-0 flex-1">
          <div class="text-[13px] text-text">{{ admin ? admin.name : '飞书 · 管理员' }}</div>
          <div class="mt-[3px] text-[11.5px] leading-[1.55] text-text-tertiary">
            <template v-if="admin">
              已绑定{{ admin.app_id ? ` · ${admin.app_id}` : '' }}
              <template v-if="admin.sys_prompt"> · 已设预设提示词</template>
            </template>
            <template v-else>尚未绑定 · 创建后自动成为管理员，可发起隧道与调用 API</template>
          </div>
          <!-- 门锁上了，不等于要把这扇门是干什么的藏起来：原因**追加**，不替换上面的描述。 -->
          <div v-if="external" class="mt-[5px] flex items-center gap-1.5 text-[11.5px] leading-[1.5] text-warning">
            <svg viewBox="0 0 24 24" class="h-3.5 w-3.5 shrink-0" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
              <path d="M12 9v4M12 17h.01M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z" />
            </svg>
            需在内网访问 · 飞书接入接口不对外网开放
          </div>
        </div>

        <!-- 能力标记只出现在**已配置的管理员**这里，且全站只有这一处 -->
        <CapTag v-if="admin" />
        <Badge v-else tone="neutral">未配置</Badge>

        <Button
          v-if="!admin"
          size="sm"
          variant="primary"
          :disabled="external"
          @click="createOpen = true"
        >
          一键创建
        </Button>
        <Button v-else size="sm" :disabled="external" @click="startEdit(admin)">配置</Button>
      </div>

      <!-- ============ 其余机器人：平级条目，不按渠道分组 ============ -->
      <template v-for="b in members" :key="b.id">
        <div class="agent-item flex items-start gap-2.5 rounded-lg border border-border bg-surface px-3 py-2.5">
          <span class="mt-[5px] h-2 w-2 shrink-0 rounded-full bg-success" />
          <div class="min-w-0 flex-1">
            <div class="text-[13px] text-text">{{ b.name }}</div>
            <div class="mt-[3px] text-[11.5px] leading-[1.55] text-text-tertiary">
              成员身份{{ b.app_id ? ` · ${b.app_id}` : '' }}
              <template v-if="b.sys_prompt"> · 已设预设提示词</template>
            </div>
            <div v-if="external" class="mt-[5px] text-[11.5px] leading-[1.5] text-warning">需在内网访问</div>
          </div>
          <Button size="sm" :disabled="external" @click="startEdit(b)">配置</Button>
        </div>

        <!-- 就地编辑：属于这台机器人的设置，就在这台机器人上改 -->
        <div
          v-if="editingId === b.id"
          class="flex flex-col gap-2 rounded-lg border border-border bg-surface-inset px-3 py-3"
        >
          <label class="flex flex-col gap-1 text-[11px] text-text-tertiary">
            名称
            <input
              v-model="editName"
              class="rounded-md border border-border bg-background px-2.5 py-1.5 text-sm text-text outline-none focus:border-accent/60"
            />
          </label>
          <label class="flex flex-col gap-1 text-[11px] text-text-tertiary">
            预设提示词
            <textarea
              v-model="editPrompt"
              rows="2"
              placeholder="这台机器人在应答时始终遵守的指令"
              class="resize-y rounded-md border border-border bg-background px-2.5 py-1.5 text-sm text-text outline-none focus:border-accent/60"
            />
          </label>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span v-if="editError" class="text-[11px] text-error">{{ editError }}</span>
            <span v-else />
            <div class="flex items-center gap-2">
              <Button size="sm" :disabled="savingEdit" @click="cancelEdit">取消</Button>
              <Button size="sm" variant="primary" :loading="savingEdit" @click="saveEdit">保存</Button>
            </div>
          </div>
          <!-- 解绑：内联二次确认（不用原生 confirm） -->
          <div class="flex flex-wrap items-center justify-between gap-2 border-t border-border-subtle pt-2">
            <span class="text-[11px] text-text-tertiary">解绑会让这台机器人立刻停止收发消息。</span>
            <div v-if="!confirmUnbind" class="flex items-center gap-2">
              <Button size="sm" variant="danger" @click="confirmUnbind = true">解绑</Button>
            </div>
            <div v-else class="flex items-center gap-2">
              <span class="text-[11px] text-error">确定解绑「{{ b.name }}」？</span>
              <Button size="sm" :disabled="savingEdit" @click="confirmUnbind = false">取消</Button>
              <Button size="sm" variant="danger" :loading="savingEdit" @click="doUnbind">确定解绑</Button>
            </div>
          </div>
        </div>
      </template>

      <!-- 管理员的就地编辑（与成员同一份表单逻辑） -->
      <div
        v-if="admin && editingId === admin.id"
        class="flex flex-col gap-2 rounded-lg border border-border bg-surface-inset px-3 py-3"
      >
        <label class="flex flex-col gap-1 text-[11px] text-text-tertiary">
          名称
          <input
            v-model="editName"
            class="rounded-md border border-border bg-background px-2.5 py-1.5 text-sm text-text outline-none focus:border-accent/60"
          />
        </label>
        <label class="flex flex-col gap-1 text-[11px] text-text-tertiary">
          预设提示词
          <textarea
            v-model="editPrompt"
            rows="2"
            placeholder="这台机器人在应答时始终遵守的指令"
            class="resize-y rounded-md border border-border bg-background px-2.5 py-1.5 text-sm text-text outline-none focus:border-accent/60"
          />
        </label>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span v-if="editError" class="text-[11px] text-error">{{ editError }}</span>
          <span v-else />
          <div class="flex items-center gap-2">
            <Button size="sm" :disabled="savingEdit" @click="cancelEdit">取消</Button>
            <Button size="sm" variant="primary" :loading="savingEdit" @click="saveEdit">保存</Button>
          </div>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2 border-t border-border-subtle pt-2">
          <span class="text-[11px] text-text-tertiary">解绑会让这台机器人立刻停止收发消息。</span>
          <div v-if="!confirmUnbind" class="flex items-center gap-2">
            <Button size="sm" variant="danger" @click="confirmUnbind = true">解绑</Button>
          </div>
          <div v-else class="flex items-center gap-2">
            <span class="text-[11px] text-error">确定解绑「{{ admin.name }}」？</span>
            <Button size="sm" :disabled="savingEdit" @click="confirmUnbind = false">取消</Button>
            <Button size="sm" variant="danger" :loading="savingEdit" @click="doUnbind">确定解绑</Button>
          </div>
        </div>
      </div>

      <!-- ============ 继续创建（从第二台起用；与上面那张卡同一套流程） ============
           禁用**只压边框与标签，不整块降透明度** —— 整块 0.55 会把橙色原因
           淡到读不清，而原因恰恰是唯一要让人看懂的东西。 -->
      <button
        class="flex w-full flex-col items-start gap-1 rounded-lg border border-dashed px-3 py-2.5 text-left text-[12.5px] transition-colors disabled:cursor-not-allowed"
        :class="
          external
            ? 'border-border text-text-tertiary'
            : 'border-border-strong text-text-secondary hover:border-accent/50 hover:text-text'
        "
        :disabled="external"
        @click="createOpen = true"
      >
        <span class="flex items-center gap-1.5">
          <svg viewBox="0 0 24 24" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <path d="M12 5v14M5 12h14" />
          </svg>
          一键创建飞书机器人
        </span>
        <!-- 只写"需内网访问"这半句：完整原因在上面的卡片上，同一屏里再抄一遍就是同一句话说两遍。 -->
        <span class="text-[11px] text-text-tertiary">
          {{ external ? '需内网访问' : '支持预设提示词 · 仅管理员含隧道与 API 权限' }}
        </span>
      </button>
    </template>

    <RobotCreateModal :open="createOpen" :first="first" @close="createOpen = false" @created="onCreated" />
  </div>
</template>
