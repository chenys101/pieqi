// 新建任务页的项目 / Agent / 模型策略。
//
// 为什么整块逻辑在 features 而不在 pages（web/README「pages 只做路由与布局拼装」）：
// 这里决定的是**任务会落到哪个目录、由谁执行**——是产品规则，不是页面布局。
// 页面只负责把它们摆进模板。
//
// **项目不是必选项**（用户 2026-10-23 要求）：默认落在设置里的「默认工作目录」
// （出厂缺省是服务端的 `<数据根>/workspace`），用户不碰项目那一步也能直接发任务。
// 想换目录时才展开选择器 —— 于是"每次都要选一遍那个永远一样的目录"这条负担消失了。
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useTaskStore } from '@/stores/task'
import { useSessionStore } from '@/stores/session'
import { useAgentStore } from '@/stores/agent'
import { useNotificationStore } from '@/stores/notification'
import { groupKey } from '@/utils/format'
import { getSettings } from '@/services/api/settings'
import type { PendingImage } from '@/types/image'

/**
 * @param initialProjectPath 上游给的**预选项目路径**（目前只有一处来源：任务详情页的
 *   「新建任务」入口，带 `?project=<当前任务的绝对路径>` 进来 —— 用户 2026-10-24 要求：
 *   从某个任务点创建时，默认就该落在那个任务的项目里）。
 */
export function useNewTaskForm(initialProjectPath: () => string) {
  const router = useRouter()
  const taskStore = useTaskStore()
  const sessionStore = useSessionStore()
  const agentStore = useAgentStore()
  const notify = useNotificationStore()

  /**
   * 预选值按"用户已经决定"对待（直接置 pathTouched）而不是当成另一个默认值：
   * 这是**有来处**的选择（"我在看这个项目的任务，接着在这个项目里再开一个"），
   * 比设置里的默认工作目录更具体。若只把它填进 selectedPath，下面那段默认值逻辑
   * （默认目录 / 最近项目）会把它当成"系统猜的"继续覆盖，表现就是"点了没用"。
   * 它不影响其余流程：用户仍可在选择器里换成任何别的项目，选了就照常生效。
   */

  /** 预选值本身无效（空 / 非绝对路径 / 是根目录）时不预选：那只会让用户在一个怪目录里干活 */
  const presetPath = computed(() => {
    const p = (initialProjectPath() || '').trim()
    if (!p) return ''
    // 绝对路径判据只看形态（`C:\...` / `\\server\...` / `/...`），不猜盘符白名单：
    // 前端拿不到服务端的路径是否为目录，真正的校验在后端 resolveTaskPath 里。
    if (!/^([a-zA-Z]:[\\/]|\\\\|\/)/.test(p)) return ''
    if (/^[a-zA-Z]:[\\/]?$/.test(p) || p === '/') return ''
    return p
  })
  /**
   * 是否真的带着预选值进来的（决定那枚「来自当前任务」徽标露不露）。
   * 用 ref 而不是直接看 presetPath：**一旦用户改成别的项目，徽标就该消失** ——
   * 这个徽标说的是"这个项目是从那来的"，不是"这页是从那进来的"。
   */
  const presetActive = ref(!!presetPath.value)

  const prompt = ref('')
  /** 已选图片（v-model 给 ImagePicker）。发送后清空 —— 图片是**按轮**载荷。 */
  const images = ref<PendingImage[]>([])
  const imagePicker = ref<{ open: (data: DataTransfer) => void } | null>(null)
  /** 'select' = 从历史项目里挑；'path' = 手填绝对路径 */
  const mode = ref<'select' | 'path'>('select')
  const selectedPath = ref('')
  const customPath = ref('')
  /**
   * 预选项目落到哪一边：在历史项目清单里就用下拉框（`select`），不在就用自定义路径输入
   * （`path`，例如是别的机器/别的工作区里的目录）。判据见下面 `presetUsesCustom` 的注释。
   */
  const presetUsesCustom = ref(false)
  const creating = ref(false)

  /**
   * 服务端设置里的默认工作目录（空串 = 服务端也没配）。
   *
   * 为什么前端必须读它而不是硬编码一个默认值：路径必须是**服务端上真实存在**的目录，
   * 前端凭空造一个（'~'、'./workspace'）只会在提交时换来一句 400。真正的兜底判断
   * 也在后端（见 api.resolveTaskPath），这里读到的值只用来**显示与预选**。
   */
  const defaultProjectPath = ref('')
  /** 设置是否已读回（用来区分"还没读到"与"确实没配"） */
  const settingsLoaded = ref(false)
  /** 用户是否主动改过目录：改过之后就不再被默认值覆盖 */
  const pathTouched = ref(false)
  /**
   * 是否展开路径选择器。
   *
   * 默认**收起**：默认目录就是绝大多数时候的答案，选择器摊在首屏等于每次都要
   * 从一堆无关的东西里穿过它才能打字。没有默认目录（且没有历史项目）时才展开，
   * 因为那时它是必填项。
   *
   * 从详情页带着项目进来时是**例外**（见 presetPath）：那一行的摘要不显示"默认目录"
   * 徽标（它不是默认目录），用户需要能一眼看出"是哪个项目"，所以直接摊开选择器。
   */
  const showProjectPicker = ref(!!presetPath.value)

  /** 当前生效的项目路径（空 = 交给后端的默认目录） */
  const effectiveProjectPath = computed(() => {
    if (mode.value === 'path') return customPath.value.trim()
    return selectedPath.value || ''
  })

  /** 是否会落到默认目录：只有用户完全没指定路径时才是 */
  const usingDefaultPath = computed(() => !effectiveProjectPath.value && !!defaultProjectPath.value)

  /** 项目卡片头部的摘要文案：一眼看出这次任务会在哪跑 */
  const projectSummary = computed(() => {
    if (effectiveProjectPath.value) return effectiveProjectPath.value
    if (defaultProjectPath.value) return defaultProjectPath.value
    return '未配置默认目录'
  })

  // 执行 Agent：取值是 agent 业务名（claude / qoder）。**默认 Claude Code**——
  // 初始值来自服务端目录的 default（后端保证是 claude），目录拉不到时由 store 兜底。
  const agent = ref('')

  // 模型：取值是 agent 下发的**不透明串**（如 dsh 的 ["magpie","workbuddy/glm-5.3-flash"]）。
  // 空串 = 用 agent 自己的默认（不指定）。前端只做"原样搬运"，不解析、不拼接 ——
  // 自己拼的取值会被 agent 判为未知模型，任务直接失败。
  const model = ref('')

  /**
   * ACP 运行模式（SessionModeId）。空 = agent 协商默认。
   * 清单来自目录项 modes（manifest.DefaultModes），与「项目路径 mode（select|path）」无关。
   */
  const sessionMode = ref('')

  const projects = computed(() => taskStore.recentProjects)
  /** Agent 目录（选择器的选项来源） */
  const catalog = computed(() => agentStore.catalog)

  /** 当前选中的 agent 目录项（展示 transport / 说明用） */
  const selectedAgent = computed(() => agentStore.byId(agent.value))

  /** 当前 agent 的可选模型（未加载/不支持时为空数组） */
  const models = computed(() => agentStore.modelsOf(agent.value))
  /**
   * 清单已尝试加载过（含"支持但为空"）。
   * 用来区分「还在读」与「确实没有」—— 否则会先闪一句"不支持选择模型"再冒出下拉框。
   */
  const modelsLoaded = computed(() => !!agentStore.modelsLoaded[agent.value])

  /**
   * 建任务前可选 ACP 模式。
   * 优先用拉 models 时捎带回来的清单（真协商）；否则用目录里的 DefaultModes（Cursor）。
   */
  const sessionModes = computed(() => {
    const probed = agentStore.modesOf(agent.value)
    if (probed.length) return probed
    return selectedAgent.value?.modes ?? []
  })

  // 选中模型的说明（仅直接选中的那个有；"Agent 默认"没有）
  const selectedModelHint = computed(() => models.value.find((m) => m.value === model.value)?.description || '')

  // 换 agent 就是换一整套模型清单 ⇒ 已选模型必然失效，必须清空再按需拉新清单。
  // 用 immediate 覆盖"进页面时目录后到"的情况（agent 由 0 → 默认值 也会触发）。
  //
  // 图片同理清空，而且理由更硬：图片能力**取决于 agent**（claude 恒不收图）。若留着
  // 在 qoder 下选的图再切到 claude，提交时整条任务会被后端以 ErrImageNotSupported
  // 拒绝 —— 用户刚只是换了个 agent，却收到一句关于图片的报错，成因离操作很远。
  // 清空比"悄悄把图带上再报错"好，也比默默丢图好（静默丢图是最坏的一种，见 agent/session.go）。
  //
  // 模式同理：Cursor 的 agent/plan/ask 与其它 agent 的清单无关，换 agent 必须清空。
  watch(
    agent,
    (a) => {
      model.value = ''
      sessionMode.value = ''
      images.value = []
      agentStore.loadModels(a)
    },
    { immediate: true },
  )

  // 清单到位后默认选中首项（优先 id=agent）：避免触发器一直显示空的「模式」。
  watch(
    sessionModes,
    (modes) => {
      if (!modes.length) {
        sessionMode.value = ''
        return
      }
      if (sessionMode.value && modes.some((m) => m.id === sessionMode.value)) return
      const preferred = modes.find((m) => m.id === 'agent') ?? modes[0]
      sessionMode.value = preferred.id
    },
    { immediate: true },
  )

  onMounted(async () => {
    // 目录就绪后再落默认值：先渲染再选中的顺序能避免"选择器空着闪一下"
    await agentStore.loadCatalog()
    if (!agent.value) agent.value = agentStore.defaultAgent
  })

  // 读默认工作目录。失败**不弹错**：读不到就退化成"没有默认值"，用户自己选一个即可，
  // 那仍然是完整可用的路径 —— 一个可选的便利不该在半路拦住"新建任务"这条主流程。
  onMounted(async () => {
    try {
      defaultProjectPath.value = (await getSettings()).default_project_path || ''
    } catch {
      defaultProjectPath.value = ''
    } finally {
      settingsLoaded.value = true
    }
  })

  /**
   * 预选项目（只做一次）。
   *
   * 为什么挂在"历史项目到了"之后而不是 onMounted 里立刻赋值：`mode` 取 select 还是 path
   * 取决于**它是不是历史项目之一**（下拉框里没有那一项时，`<select>` 会显示成空白，
   * 用户只看到"项目那一栏空着"—— 明明有值却像没选）。等清单到了再判，就一次判对；
   * 清单拉不到时 taskStore.loaded 也会置真（见 store 的注释），所以不会一直等下去。
   *
   * 它同时置 `pathTouched`：这是详情页带过来的**有来处**的选择，必须压过下面那段
   * 默认值逻辑（默认目录 / 最近项目），否则就是"点了新建，项目却换成了别的"。
   */
  const presetApplied = ref(false)
  watch(
    [projects, () => taskStore.loaded],
    ([list]) => {
      if (presetApplied.value) return
      const p = presetPath.value
      if (!p) {
        presetApplied.value = true // 没有预选：交给下面的默认值逻辑
        return
      }
      pathTouched.value = true
      showProjectPicker.value = true
      const hit = list.find((x) => groupKey(x.projectPath) === groupKey(p))
      if (hit) {
        selectedPath.value = hit.projectPath // 与下拉项完全一致（大小写/尾斜杠归一）
      } else {
        // 不在这里"等清单完整再判"：清单拉失败时它也会到（store 在 finally 里置 loaded），
        // 但若就此把值填进 path 模式，用户后面看到下拉里出现它也不会被二次改写 ——
        // 判据是"值已经填好、能提交"，不是"清单一定完整"。落错也不会出事故：
        // 路径是详情页任务的真实项目，服务端会照常校验它是个目录。
        presetUsesCustom.value = true
        mode.value = 'path'
        customPath.value = p
      }
      presetApplied.value = true
    },
    { immediate: true },
  )

  // 项目默认值（已不是必选项）：
  //   ⓪ 详情页带着当前任务的项目进来 → 已经选好（见上面的预选块），这里一概不动；
  //   ① 有默认工作目录 → 什么都不选，直接用它（不再自动选中"最近项目"：
  //      选一个用户没要的目录，比让默认目录生效更容易让人意外地在错的仓库里干活）；
  //   ② 没有默认目录但有历史项目 → 选中最近的一个（与从前一致）；
  //   ③ 两者都没有 → 切到手动填路径（此时路径是必需项，不填就发不出去）。
  // 判空必须等 taskStore.loaded —— 冷启动时（移动端首屏尤其明显）此刻 tasks 往往
  // 还没到，在 onMounted 里判空会把"还没到"当成"没有"（PC 上因列表先到才没暴露）。
  // 用户手动改过路径（pathTouched）之后，这些自动填充一律不再生效。
  // ⚠️ **必须等设置读回再决定**（settingsLoaded 先于一切判断）：两个请求哪个先到不确定，
  // 若在设置还没到时就按 ② 选中了最近项目，默认目录到达后就被"已有选择"挡在外面 ——
  // 表现是"配了默认目录却不生效"，而且只在列表比设置先到的那台/那次出现。
  // 设置读回之前的等待很短（同一批首屏请求），代价远低于这类时序性错误。
  watch(
    [projects, () => taskStore.loaded, defaultProjectPath, settingsLoaded],
    ([list, loaded]) => {
      if (!settingsLoaded.value) return
      if (pathTouched.value || effectiveProjectPath.value) return
      if (defaultProjectPath.value) return // ①：交给默认目录，不动选择器
      if (list.length) {
        selectedPath.value = list[0].projectPath // ②
      } else if (loaded) {
        mode.value = 'path' // ③
        showProjectPicker.value = true // 此时路径是必填项，选择器必须露出来
      }
    },
    { immediate: true },
  )

  // 目录晚于页面就绪（或曾拉取失败后重试成功）：补上默认 agent
  watch(
    () => agentStore.defaultAgent,
    (d) => {
      if (!agent.value) agent.value = d
    },
  )

  /**
   * 有内容可提交：文字或图片任一即可。
   *
   * 「只发一张图、不写字」是正当用法（让 agent 描述/分析图片），与新增会话输入框
   * 同一套口径（见 InterveneInput 的 sendDisabled）。若仍以文字非空为判据，
   * 选了图却发不出去，入口就成了摆设。
   */
  const canSubmitContent = computed(() => !!prompt.value.trim() || images.value.length > 0)

  /**
   * 可提交：有内容 **且** 有一个真正能用的目录。
   *
   * 三条来源任有一条即可：手填路径 / 选中的历史项目 / 服务端默认目录。
   * 一条都没有时禁用提交 —— 此时点下去只会换来后端一句"未指定项目路径"，
   * 让按钮自己说得清楚（见模板里的提示行）比事后报错好。
   */
  const canSubmit = computed(() => {
    if (creating.value || !canSubmitContent.value) return false
    if (mode.value === 'path') return !!customPath.value.trim()
    return !!selectedPath.value || !!defaultProjectPath.value
  })

  /**
   * 「真的没有工作目录」—— 只回答这一件事，**不要**复用它上面的 `canSubmit`。
   *
   * 2026-10-29 线上截图：项目选的是 `D:\code\go\pieqi`、下拉框也显示 `pieqi`，页面却
   * 弹出「还没有工作目录」。成因就是当时这条提示写成 `!canSubmit && canSubmitContent`，
   * 而 `canSubmit` 里带 `creating`（正在提交）—— 点下创建到响应回来那几百毫秒里 canSubmit
   * 变 false，提示便把"正在提交"误报成"没有目录"。措辞与事实相反，用户会以为刚才的选择没生效
   * （实测同一个页面提交中、按钮已转圈，红字还在说"没目录"）。
   *
   * 判据只有两条：没有生效路径，且服务端也没配默认目录 —— 只有两者皆无才真的发不出去。
   * 之所以仍要求"有内容"，是因为空输入框时这条提示只是噪音（此时点击按钮本来也不会发生任何事）。
   */
  const missingWorkdir = computed(
    () => !effectiveProjectPath.value && !defaultProjectPath.value && canSubmitContent.value,
  )

  /**
   * 加图入口的显隐依据。
   *
   * 判据来自**服务端目录**（GET /api/agents 的 image_prompt，见 selectedAgent）——
   * 不用前端的 agent 名硬编码名单：能力取决于配置（claude 走桥时有图片通路，
   * 配成 transport=print 就没有），硬编码必然在配置变化时静默过期。
   *
   * 判据本体的说明在 selectedAgent?.imagePrompt 的来源处（types/api.ts 的 AgentDto）。
   * 注意 selectedAgent 为 undefined 时（目录还没到）取 false：不显示一个可能不该显示的按钮。
   */
  const canAttachImages = computed(() => selectedAgent.value?.imagePrompt === true)

  /**
   * 输入框里的粘贴：把事件转交给 ImagePicker（截图直接贴进来）。
   *
   * 与 `v-if="canAttachImages"` 刻意不同的一点：**不做能力分支**。
   * 这里的 canAttachImages 有两个来源（agent 目录的 imagePrompt、目录还没到时的
   * undefined→false），后者是"暂时不知道"而不是"确定不支持"。把它当成不支持来
   * 拒绝粘贴，用户会在页面刚打开的一瞬间被莫名挡掉；而先收进缩略图条、真不支持时
   * 在 submit 里给一句明确的人话，体验与判据都更干净（那段兜底已经存在，见 submit）。
   *
   * picker 为 null（入口没渲染 / 已满 8 张）时什么都没发生。
   */
  function onPaste(data: DataTransfer | null) {
    if (data) imagePicker.value?.open(data)
  }

  /**
   * 用户动过项目就等于**放弃了预选**：那枚「来自当前任务」徽标不能再挂着 ——
   * 它会变成一句假话（"这个项目来自当前任务"，而用户刚改成了别的）。
   *
   * 预选值本身仍留在 pathTouched 语义里（"用户已决定"），只是不再被强调。
   */
  function markPathTouched() {
    pathTouched.value = true
    presetActive.value = false
  }

  async function submit() {
    if (!canSubmit.value) return
    // 空串是**合法输入**：表示"用服务端的默认工作目录"（见 services/api/tasks.createTask）。
    // 只有"既没选也没默认目录"才是错误 —— 那种情况 canSubmit 已经挡掉了。
    const path = effectiveProjectPath.value
    if (!path && !defaultProjectPath.value) {
      notify.error('请选择或输入项目路径')
      mode.value = 'path'
      return
    }
    // 走到这里说明用户显式给了路径（否则是默认目录），切回"用户已决定"，
    // 免得后台的默认值 watcher 在提交途中把选择器清掉。
    if (path) pathTouched.value = true
    // 兜底：正常情况下 agent 切换时会清空图片，这里防的是"入口被绕过"（例如目录后到、
    // agent 由空变成 claude 而图片已在框里）。宁可前置报一句人话，也不让后端那句
    // 「当前 agent 传输不支持图片（*claude.session）」直接糊到用户脸上。
    if (images.value.length > 0 && !canAttachImages.value) {
      notify.error('当前 Agent 不支持图片，请先移除已选图片')
      return
    }
    creating.value = true
    try {
      // 图片是**按轮**载荷（同 model），只在这一次创建时发出，不落库
      // （落库的只有"这条消息带了几张图"的元数据，见 model.TaskEvent.Images）。
      const task = await taskStore.createTask(
        path,
        prompt.value.trim(),
        agent.value || undefined,
        model.value || undefined,
        images.value.map((i) => ({ data: i.data, mime_type: i.mimeType })),
        sessionMode.value || undefined,
      )
      sessionStore.setThinking(task.id, true)
      router.push(`/sessions/${task.id}`)
    } catch (err) {
      notify.error(err instanceof Error ? err.message : '创建失败')
    } finally {
      creating.value = false
    }
  }

  return {
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
    effectiveProjectPath,
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
    canSubmitContent,
    canSubmit,
    missingWorkdir,
    canAttachImages,
    onPaste,
    markPathTouched,
    submit,
  }
}
