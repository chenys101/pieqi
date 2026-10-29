<script setup lang="ts">
// 新建任务页：只做布局与拼装（web/README「pages 只做路由与布局拼装」）。
//
// 项目 / Agent / 模型的**策略**在 features/task/useNewTaskForm.ts：
// 预选项目、默认工作目录、最近项目兜底、"没有目录就发不出去"的判据都在那里。
// 这里只保留模板需要的绑定，不再持有业务逻辑。
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import Select from '@/components/ui/Select.vue'
import PromptInput from '@/features/session/components/PromptInput.vue'
import ContentColumn from '@/components/layout/ContentColumn.vue'
import ModelPicker from '@/features/session/components/ModelPicker.vue'
import ModePicker from '@/features/session/components/ModePicker.vue'
import ImagePicker from '@/features/session/components/ImagePicker.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useRouter } from 'vue-router'
import { groupKey } from '@/utils/format'
import { useNewTaskForm } from '@/features/task/useNewTaskForm'

const props = withDefaults(defineProps<{ initialProjectPath?: string }>(), { initialProjectPath: '' })
const router = useRouter()

const {
  prompt,
  images,
  imagePicker,
  mode,
  selectedPath,
  customPath,
  presetPath,
  presetActive,
  presetUsesCustom,
  creating,
  defaultProjectPath,
  showProjectPicker,
  usingDefaultPath,
  projectSummary,
  agent,
  model,
  sessionMode,
  sessionModes,
  projects,
  catalog,
  selectedAgent,
  models,
  modelsLoaded,
  selectedModelHint,
  canSubmit,
  missingWorkdir,
  canAttachImages,
  onPaste,
  markPathTouched,
  submit,
} = useNewTaskForm(() => props.initialProjectPath)
</script>

<template>
  <div class="flex h-full">
    <div class="flex min-h-0 min-w-0 flex-1 flex-col">
      <!-- Header（与 SessionHeader 同骨架）：返回 + 标题 -->
      <header class="border-b border-border bg-surface/60 px-3 py-2.5 md:px-4">
        <ContentColumn :resizable="false">
          <div class="flex items-center gap-2">
            <button
              class="rounded-md px-1.5 py-1 text-muted transition-colors hover:bg-elevated hover:text-text"
              title="返回仪表盘"
              aria-label="返回"
              @click="router.push('/dashboard')"
            >
              <svg viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
                <path d="M15 18l-6-6 6-6" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" />
              </svg>
            </button>
            <h1 class="min-w-0 flex-1 truncate text-sm font-semibold">新建任务</h1>
          </div>
        </ContentColumn>
      </header>

      <!-- 主区（对应详情页 Timeline 位置）：项目选择 -->
      <div class="min-h-0 flex-1 overflow-y-auto px-3 py-4 md:px-4">
        <ContentColumn pad="" data-testid="page-content-wrap">
          <div class="flex flex-col gap-2" data-testid="page-content">
            <!-- 项目（可选）：默认用设置里的「默认工作目录」，一动不碰也能直接发任务。
                 折叠态只显示一行"将在哪跑"，展开才出现选择器 —— 因为**大多数时候
                 用户要的就是那个固定目录**，把选择器一直摊在首屏是在为少数情况付费。 -->
            <div class="rounded-lg border border-border bg-surface p-3" data-testid="project-card">
              <div class="flex items-center gap-2">
                <div class="min-w-0 flex-1">
                  <div class="mb-1.5 flex items-baseline gap-1.5 text-xs font-medium text-muted">
                    <span>项目</span>
                    <span class="font-normal text-text-tertiary">可选</span>
                  </div>
                  <!-- 一行摘要：本次任务的工作目录。长路径截断，全文在 title 里 -->
                  <div
                    class="flex min-w-0 items-center gap-1.5 text-xs"
                    :title="projectSummary"
                    data-testid="project-summary"
                  >
                    <span class="min-w-0 truncate font-mono" :class="usingDefaultPath ? 'text-text-tertiary' : 'text-text'">
                      {{ projectSummary }}
                    </span>
                    <!-- 「来自当前任务」：从任务详情页点进来的预选值。**只说事实**（这个项目是
                         从哪儿带过来的），不构成一条规则 —— 用户改成别的项目后徽标随之消失
                         （见 clearPresetBadge）。两种形态都要有：历史项目里有的走下拉框、
                         没有的（别的机器/别的工作区）走自定义路径输入。 -->
                    <span
                      v-if="presetActive"
                      class="shrink-0 rounded bg-elevated px-1.5 py-0.5 text-[11px] text-text-tertiary"
                      data-testid="project-from-task"
                    >
                      来自当前任务
                    </span>
                    <span v-else-if="usingDefaultPath" class="shrink-0 rounded bg-elevated px-1.5 py-0.5 text-[11px] text-text-tertiary">
                      默认目录
                    </span>
                  </div>
                </div>
                <Button size="sm" variant="ghost" data-testid="project-toggle" @click="showProjectPicker = !showProjectPicker">
                  {{ showProjectPicker ? '收起' : '更改' }}
                </Button>
              </div>

              <div v-if="showProjectPicker" class="mt-2 flex flex-col gap-2 border-t border-border-subtle pt-2">
                <div class="flex gap-2">
                  <template v-if="mode === 'select'">
                    <Select v-model="selectedPath" class="min-w-0 flex-1" aria-label="历史项目" @change="markPathTouched">
                      <!-- 空值项 = 用默认目录。显式列出它，用户才能在"选了历史项目"之后**退回**默认 -->
                      <option v-if="defaultProjectPath" value="">使用默认目录（{{ defaultProjectPath }}）</option>
                      <option v-for="p in projects" :key="groupKey(p.projectPath)" :value="p.projectPath">
                        {{ p.projectId || p.projectPath }}
                      </option>
                      <option v-if="!projects.length" value="" disabled>暂无历史项目 — 请自定义路径</option>
                      <!-- 预选的项目在**清单到达前**就已决定用自定义路径（见预选块）；
                           用户随时可以切回下拉，那一项要能找得到 —— 否则一按「选项目」
                           预选值就从界面上消失，只剩一句"选一个"的空下拉。 -->
                      <option v-if="presetUsesCustom && presetPath" :value="presetPath">
                        {{ presetPath }}
                      </option>
                    </Select>
                    <Button size="sm" @click="mode = 'path'">自定义路径</Button>
                  </template>
                  <template v-else>
                    <Input
                      v-model="customPath"
                      class="min-w-0 flex-1"
                      placeholder="输入绝对路径，如 G:\workspace\erp"
                      @input="markPathTouched"
                    />
                    <Button
                      v-if="projects.length || defaultProjectPath || (presetUsesCustom && presetPath)"
                      size="sm"
                      @click="mode = 'select'"
                    >
                      选项目
                    </Button>
                  </template>
                </div>
                <!-- 兜底提示：既没默认目录也没选任何东西时，必须把"为什么发不出去"说出来。
                     判据用 missingWorkdir 而不是 canSubmit —— 后者含 creating，会在提交中
                     误报（见 missingWorkdir 的注释）。 -->
                <p v-if="missingWorkdir" class="text-[11px] text-warning" data-testid="project-missing">
                  还没有工作目录：请选择一个项目或填写路径（也可以到设置里配置默认目录）。
                </p>
              </div>
            </div>
            <div class="rounded-lg border border-border bg-surface p-3">
              <div class="mb-1.5 text-xs font-medium text-muted">执行 Agent</div>
              <Select v-model="agent" aria-label="执行 Agent" data-testid="agent-select">
                <option v-for="a in catalog" :key="a.id" :value="a.id">{{ a.name }}</option>
              </Select>
              <!-- 选中项的说明随选择变化：换 agent 是"换执行者"，用户需要知道换了什么 -->
              <p class="mt-1.5 text-xs text-muted">
                {{ selectedAgent?.description || '选择由哪个 coding agent 执行本任务' }}
              </p>
            </div>
            <!-- 模型选择器嵌在底部输入框内（与详情页同一套 ModelPicker），
                 不再在主区单开一块「模型」卡片 ——
                 「项目 / Agent」是**这个任务的属性**，而模型是**发这条消息时的选择**，
                 两者不在同一个决策时刻，摆在一起反而让人以为改模型等于改任务。
                 该 Agent 不下发清单时，弹层里有一句说明（ModelPicker 的空清单分支）。 -->
            <p class="px-1 text-xs text-muted">
              {{ defaultProjectPath ? '默认在「默认工作目录」里执行（可在上面更改）。' : '选择项目并指定 Agent 后，' }}在下方描述要做什么，创建后进入会话。
            </p>
          </div>
        </ContentColumn>
      </div>

      <!-- 底部输入条（对应详情页 InterveneInput 位置）：prompt + 嵌在输入框右下角的创建按钮
           内层交给 ContentColumn，与上方 header / 主区共用同一条正文左边界（AC-R8-01）；
           PC 与移动端共用这一套 DOM，按钮位置两端一致。
           `resizable=false` —— 调宽手柄只在会话时间线上出现一次。 -->
      <div class="border-t border-border bg-surface/80 px-3 py-2.5 backdrop-blur md:px-4" data-testid="composer-bar">
        <ContentColumn :resizable="false">
          <div class="w-full" data-testid="composer-inner">
            <PromptInput
              v-model="prompt"
              :rows="3"
              aria-label="任务描述"
              placeholder="描述要做什么… 输入 / 触发命令/Skill，Ctrl+Enter 创建"
              @submit="submit"
              @paste="onPaste"
            >
              <template #lead>
                <!-- 加图入口：与详情页同一套 ImagePicker。位置与理由同 InterveneInput ——
                     必须放 #lead 而不是 #actions（后者整行 justify-end，会被推到最右与创建按钮挤在一起）。
                     显隐依据见 canAttachImages 的注释（只有 ACP 系 agent 才可能收图）。 -->
                <ImagePicker v-if="canAttachImages" ref="imagePicker" v-model="images" :disabled="creating" />
                <!-- 模式与模型同属「发这条提示词时的选择」：Cursor agent/plan/ask 等。
                     清单来自目录 modes；空则不显示（claude 等无预置模式）。 -->
                <ModePicker
                  v-if="sessionModes.length"
                  :mode="sessionMode"
                  :modes="sessionModes"
                  data-testid="new-task-mode-picker"
                  @update:mode="sessionMode = $event"
                />
                <ModelPicker
                  v-if="agent"
                  :model="model"
                  :models="models"
                  :models-loaded="modelsLoaded"
                  data-testid="new-task-model-picker"
                  @update:model="model = $event"
                />
              </template>

            <template #actions>
              <!-- 视觉 36×36，命中区用伪元素外扩到 ≥44px 高（AC-R8-04）。
                   `@mousedown.prevent`：与 #lead 里的模型选择器同一判据 ——
                   不该因为"点了一下框内按钮"就把输入框的焦点弄丢。 -->
              <button
                class="pointer-events-auto relative shrink-0 rounded-[var(--radius-sm)] bg-accent p-2.5 text-white transition-opacity after:absolute after:-inset-1 after:content-[''] hover:opacity-90 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-40"
                :disabled="!canSubmit"
                title="创建任务 (Ctrl+Enter)"
                aria-label="创建任务"
                data-testid="composer-submit"
                @click="submit"
                @mousedown.prevent
              >
                <Spinner v-if="creating" class="h-4 w-4" />
                <svg v-else viewBox="0 0 24 24" class="h-4 w-4" aria-hidden="true">
                  <path d="M22 2 11 13M22 2l-7 20-4-9-9-4 20-7z" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round" />
                </svg>
              </button>
            </template>
          </PromptInput>
          <!-- 选中模型的说明 / 无清单时的说明：
               弹层关着时这些信息没有出处，所以必须留在外面。
               「没有下拉框」和「不支持选模型」在界面上无法区分 —— 触发器一直在，
               点开就知道有没有可选项。 -->
          <p v-if="models.length" class="mt-1 px-1 text-xs text-muted" data-testid="model-hint">
            {{ selectedModelHint || '不选则用该 Agent 的默认模型。' }}
          </p>
          <p
            v-else-if="modelsLoaded"
            class="mt-1 px-1 text-xs text-muted"
            data-testid="model-unsupported"
          >
            该 Agent 不提供可选模型，将使用它自身的默认模型。
          </p>
          </div>
        </ContentColumn>
      </div>
    </div>
  </div>
</template>