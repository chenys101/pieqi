// 会话详情页的**状态编排**：详情加载、决策动作、模型切换、反馈面板落点、删除确认。
//
// 为什么在 features/session 而不在 pages：web/README 约定 pages 只做路由与布局拼装。
// 这里每一条都是产品判据（"拉挂了"不等于"任务不存在"、移动端/平板/宽屏三种反馈落点、
// 运行中禁止切模型/回退），页面只负责把它们摆进模板。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import type { ComputedRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useTaskStore } from '@/stores/task'
import { useSessionStore } from '@/stores/session'
import { useAgentStore } from '@/stores/agent'
import * as api from '@/services/api/tasks'
import { adaptTask, ApiError } from '@/services/api/client'
import { useSession } from '@/composables/useSession'
import { useResponsive } from '@/composables/useResponsive'
import { onRealtimeResync } from '@/composables/useWebSocket'
import { useFeedbackPanelStore } from '@/stores/feedbackPanel'
import { useArtifactsStore } from '@/stores/artifacts'
import { useNotificationStore } from '@/stores/notification'
import type { PendingImage } from '@/types/image'

/**
 * @param taskId 当前路由的任务 id（由页面从 `route.params.id` 传入）。
 */
export function useSessionPage(taskId: ComputedRef<string>) {
  const route = useRoute()
  const router = useRouter()
  const taskStore = useTaskStore()
  const sessionStore = useSessionStore()
  const agentStore = useAgentStore()
  const { isMobile, isWide } = useResponsive()
  const fbPanel = useFeedbackPanelStore()
  const artifactsStore = useArtifactsStore()
  const notify = useNotificationStore()

  const viewing = computed(() => artifactsStore.viewing)
  const { task, canCancel, canSendPrompt, submitPrompt, cancel, approve, approveSession, deny, consumeForceScroll } =
    useSession(taskId)

  /**
   * 续问可选的模型：按任务自己的 agent 拉清单（store 内幂等 + 失败可重试）。
   * 拉不到就是空数组 → 组件隐藏选择器，续问照常走"沿用当前模型"。
   * 这里不关心探测失败的原因：换模型是可选能力，不该在会话页弹错误。
   */
  watch(
    () => task.value?.agent,
    (a) => {
      if (a) void agentStore.loadModels(a)
    },
    { immediate: true },
  )
  const turnModels = computed(() => agentStore.modelsOf(task.value?.agent || ''))
  const turnModelsLoading = computed(() => !!agentStore.modelsLoading[task.value?.agent || ''])
  /** 清单已尝试加载过 → 组件据此把"还在读"与"该 agent 没有清单"分开说 */
  const turnModelsLoaded = computed(() => !!agentStore.modelsLoaded[task.value?.agent || ''])

  /**
   * 该任务会话能否收图（加图入口的显隐依据）。
   *
   * **只在任务状态变化时重问**：能力是会话级的，会话建立（第一轮跑起来）之后才有
   * 确定答案。终态/续问时再问一次，是因为会话可能已经被空闲回收掉、续问会重建它。
   * 不作为 computed 每次求值去问 —— 那会把一次 HTTP 变成渲染路径上的副作用。
   */
  const canAttachImages = ref(false)
  watch(
    () => [task.value?.id, task.value?.status] as const,
    ([id]) => {
      if (!id) {
        canAttachImages.value = false
        return
      }
      void api
        .getTaskCapabilities(id)
        .then((c) => {
          canAttachImages.value = c.imagePrompt
        })
        .catch(() => {
          // 探测失败按"不支持"处理：能力是**可选**增强，不该在会话页弹错误打扰用户。
          canAttachImages.value = false
        })
    },
    { immediate: true },
  )

  /** 会话当前模型（不透明值，空 = Agent 默认）—— 触发器上显示的就是它。
   *  来自 Task.Model：切换走 POST /tasks/:id/model 落库，WS task_updated 推回来。 */
  const currentModel = computed(() => task.value?.model || '')

  /**
   * 执行 Agent 展示名 / 悬停说明：输入条上的 agent 胶囊用（方案 C 把 agent 下移到输入条）。
   * 目录未加载/该 agent 被关掉时退回业务名；旧任务由适配层兜底成默认 agent。
   */
  const agentLabel = computed(() => {
    const id = task.value?.agent
    if (!id) return ''
    return agentStore.byId(id)?.name || id
  })
  const agentTitle = computed(() => {
    const id = task.value?.agent
    if (!id) return ''
    const info = agentStore.byId(id)
    return info ? `${info.name}（${id} · ${info.transport}）` : id
  })

  /** 运行中不可切换：这一轮的模型在 prompt 边界就定死了（后端同样返回 409）。
   *  与其摆一个按了没用的开关，不如禁用并说明。 */
  const canSwitchModel = computed(() => !canCancel.value)
  const switchingModel = ref(false)

  /**
   * 切换会话模型：落库 + 在时间线上留一条 model_switch 记录。
   *
   * 为什么是「立刻切换」而不是「下一轮生效」：per-turn 那个（intervene.model）不落库，
   * 用户无从确认上轮跑在哪个模型上，刷新页面更是直接丢失。会话级切换把这件事变成
   * 可回溯的事实 —— 记录就在时间线里（ModelSwitchNote）。
   *
   * 失败必须报错：静默失败会让人以为已经切换，而实际下一轮还跑在旧模型上。
   */
  async function switchModel(value: string) {
    const id = task.value?.id
    if (!id || value === currentModel.value || switchingModel.value) return
    switchingModel.value = true
    try {
      await api.setTaskModel(id, value)
    } catch (err) {
      notify.error(err instanceof Error ? err.message : '切换模型失败')
    } finally {
      switchingModel.value = false
    }
  }

  /**
   * 发送补充指令：把输入框的 (文本, 图片) 接到 submitPrompt。
   *
   * 中间为什么要这一层：submitPrompt 的第二个参数是 **per-turn 模型**，而输入框
   * 现在不带它（模型已改成会话级切换，见 switchModel 的注释）—— 直接绑上去会让
   * 图片数组落到模型参数位。用显式适配器把两边的签名对齐，比调整任一侧的公共签名好。
   */
  function onSendPrompt(text: string, images: PendingImage[]) {
    void submitPrompt(text, undefined, images)
  }

  /** 决策横幅：waiting_input 且带 decision 时展示 */
  const decision = computed(() => (task.value?.status === 'waiting_input' ? task.value.decision : undefined))
  const approvalBusy = ref(false)
  /** 冷启动探测完成（详情已尝试拉取），仍无任务 → 视为不存在 */
  const probed = ref(false)
  /**
   * 详情**拉取失败**的原因（超时/断网/5xx/服务重启中）。
   *
   * 与「任务不存在」分开：前者是"这次没拿到"，后者是"服务端说不给你"。
   * 合并成一句话会让人删掉一个其实还在的任务（2026-10-23 用户反馈的直接观感就是
   * "任务详情看不了了"）。有值时页面给出重试入口，而不是一条死路。
   */
  const loadError = ref('')

  /** 重新拉详情（「重新加载」按钮） */
  function retryLoadDetail() {
    const id = taskId.value
    if (!id) return
    loadError.value = ''
    probed.value = false
    void loadDetail(id)
  }

  /** Agent 执行中禁止回退（静止边界原则）；面板仍可查看 */
  const canRewind = computed(() => !!task.value && task.value.status !== 'running' && task.value.status !== 'pending')

  /** 平板档：反馈走 420px 侧栏（宽屏常驻、移动端整屏，都不用它） */
  const feedbackOpen = ref(false)
  /** 移动端整屏：时间线 / 变更反馈 二选一（头部那一行的反馈图标来回切） */
  const mobilePane = ref<'timeline' | 'feedback'>('timeline')

  /**
   * 反馈入口的落点按档位不同 —— 三档的入口本来就不是同一个东西。
   * 宽屏**没有这个按钮**（贴边条就是它的入口）。
   */
  function openFeedback() {
    if (isWide.value) {
      fbPanel.expand()
      return
    }
    if (isMobile.value) {
      // 移动端图标是**开关**：面板里没别的路回时间线，再点一次就是回去
      mobilePane.value = mobilePane.value === 'timeline' ? 'feedback' : 'timeline'
      return
    }
    feedbackOpen.value = true
  }

  // 详情加载：WS 快照 / 列表只给**轻量视图**（不含 events），所以进入详情页时
  // 必须按 id 拉一次完整详情来填充时间线。
  //
  // ⚠️ 判据是「本地是否已有该会话的**权威**事件流」，**不是**「任务是否已在列表里」，
  // 也**不是**「本地事件数组非空」—— 后者是 2026-10-23「无法查看任务详情」的成因：
  //   快照 → sessionStore 拿到 events=null → 被当成"空事件流" →
  //   这条判据被满足 → 既不渲染时间线、也不再发请求 → 页面只剩一句「任务不存在」。
  // 现在问的是 sessionStore.eventsKnown(id)：只有**真的由带 events 的载荷载入过**
  // 才算已知。没拉到过就必须拉 —— 详情页的职责是保证时间线有数据，而不是猜。
  watch(
    taskId,
    (id) => {
      probed.value = false
      // 换任务回到时间线：停在「变更反馈」里换到一个新任务，看到的是上一任务的语境残留
      mobilePane.value = 'timeline'
      if (!id) return
      if (taskStore.byId(id) && sessionStore.eventsKnown(id)) {
        probed.value = true
        return
      }
      void loadDetail(id)
    },
    { immediate: true },
  )

  /**
   * 拉取任务完整详情（含事件流）并按 id 同步进 store。
   *
   * 统一走这个函数而不是直接调 taskStore.refreshTask：会话事件流由 sessionStore 持有，
   * 只刷新 taskStore 会让任务状态更新、时间线却还是旧的（两处状态各自为政）。
   *
   * 返回是否成功：调用方要靠它区分「拉到了」和「拉挂了」—— 后者必须让页面如实报错，
   * 不能停在"任务不存在"。listLoaded 用来判断"404 到底是不是真的不存在"
   * （本地列表里有它 ⇒ 服务端状态异常，而不是用户点错了链接）。
   */
  async function loadDetail(id: string): Promise<'ok' | 'not-related' | 'error'> {
    try {
      const dto = await api.getTaskDto(id)
      taskStore.upsertTask(adaptTask(dto))
      sessionStore.syncFromTask(dto)
      return 'ok'
    } catch (err) {
      const status = err instanceof ApiError ? err.status : 0
      // 换了任务就别再拿旧请求的结果去改页面状态（用户已经走了）
      if (taskId.value !== id) return 'not-related'
      // 404 / 401：服务端明确说"不给你这个任务"（已删除 / 鉴权没通）。
      // 这两类没有重试价值，照旧落到「任务不存在或已删除」。
      if (status === 404 || status === 401) return 'error'
      // 其余（超时 / 断网 / 5xx / 服务重启中）都**不是**"任务不存在"：
      // 如实说"详情没加载出来"并给一个重试入口，比让人以为任务被删了要好得多。
      loadError.value = err instanceof Error ? err.message : '详情加载失败'
      return 'error'
    } finally {
      if (taskId.value === id) probed.value = true
    }
  }

  /**
   * WS 重连后补拉详情 —— 这是"任务完成了、详情却不刷新"的正解。
   *
   * 断开期间服务端把事件投给了已经 Unsubscribe 的订阅者，既不置 dropped 标记、
   * 也没有补发（与 EventBus 缓冲满丢弃是两回事），那段时间的 task_delta 永久消失；
   * 重连后服务端补发的 snapshot 又是轻量视图（不含 events）。两端都没有 gap 检测，
   * 于是时间线会永久缺一段 —— 从前只有"手动刷新页面"能救。
   *
   * 这里复用 loadDetail：它本就是幂等全量替换 + 去重（syncFromTask 遇 undefined
   * events 会保留本地流，带 events 才覆盖），所以补拉不会重复插入、也不会清空已有内容。
   */
  const unregisterResync = onRealtimeResync(() => {
    const id = taskId.value
    if (id) void loadDetail(id)
  })
  onBeforeUnmount(unregisterResync)

  /** 决策请求期间的忙态：三个动作共用同一段收尾，各写一份 try/finally 才会漏掉一个 */
  async function runDecision(fn: () => Promise<void>) {
    approvalBusy.value = true
    try {
      await fn()
    } finally {
      approvalBusy.value = false
    }
  }

  const onApprove = () => runDecision(approve)
  const onApproveSession = () => runDecision(approveSession)
  const onDeny = () => runDecision(deny)

  // 删除必需确认，但**不用原生 confirm()**（SPEC 已禁）：它是模态的，会盖住任务标题与上下文，
  // 而用户恰恰要看着那些才知道删的是不是这个。改为紧接着头部长出的内联确认条。
  const removeConfirming = ref(false)

  function askRemove() {
    removeConfirming.value = true
  }

  async function doRemove() {
    if (!task.value) return
    removeConfirming.value = false
    await taskStore.deleteTask(task.value.id)
    // 删除后回仪表盘而不是 /tasks：任务浏览器页是移动端专用，桌面由侧栏树承接（SPEC §6.1）
    router.replace('/dashboard')
  }

  return {
    route,
    router,
    task,
    canCancel,
    canSendPrompt,
    cancel,
    consumeForceScroll,
    isMobile,
    isWide,
    fbPanel,
    artifactsStore,
    viewing,
    decision,
    approvalBusy,
    onApprove,
    onApproveSession,
    onDeny,
    loadError,
    probed,
    retryLoadDetail,
    canRewind,
    feedbackOpen,
    mobilePane,
    openFeedback,
    turnModels,
    turnModelsLoading,
    turnModelsLoaded,
    currentModel,
    switchingModel,
    canSwitchModel,
    switchModel,
    agentLabel,
    agentTitle,
    canAttachImages,
    onSendPrompt,
    removeConfirming,
    askRemove,
    doRemove,
  }
}
