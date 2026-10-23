package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
)

type createTaskReq struct {
	// ProjectPath 任意本地路径（绝对路径优先），用于直接在原路径运行，不建 worktree。
	//
	// **可省略**（项目不再是必选项）：空则用设置里的 default_project_path
	// （设置页「默认工作目录」，出厂缺省 <dataRoot>/workspace）。
	// 两者都没有时返回 400 并指向设置页 —— 不猜目录，理由见 resolveTaskPath。
	ProjectPath string `json:"project_path"`
	Prompt      string `json:"prompt" binding:"required"`
	// Agent 可选的 agent 名（见 GET /api/agents）。空 = 服务端默认（Claude Code）。
	// 取值为 agent 业务名（claude / qoder），不是前端展示名。
	Agent string `json:"agent"`
	// Model 可选的模型（见 GET /api/agents/{agent}/models）。空 = 用 agent 自己的默认。
	// 取值是 agent 下发的**不透明选择值**，必须原样回传，前端不得自行拼接。
	//
	// 不做「值必须在清单里」的入参校验：校验要么触发一次探测（起 agent 进程，把 POST 拖到
	// 秒级）、要么依赖可能过期的缓存。两者都不值当 —— 错的取值会让会话打开时**明确报错**
	// （agent 报未知模型），失败是响亮的，不会静默跑错。
	Model string `json:"model"`
	// Images 随首轮 prompt 一起发出的图片（可选）。
	//
	// ⚠️ 与 Model 不同，这里的取值**必须校验**，且在**建任务之前**校验：base64 与 mime
	// 是协议硬要求，图片体积直接决定上下文开销 —— 放进去一张 20MB 的图，代价是真金白银。
	//
	// 校验复用 agent.ValidateImages（规则只有那一份，避免两处判据漂移）。
	// 曾经只在 agent 层（buildPromptBlocks）校验，那是**发送时**才发生的 ——
	// 于是永远发不出去的输入会先拿到 201、留下一个 failed 任务和一串假图片元数据
	// （bytes:0 之类已落盘）。先落库再报错是错的顺序，见下方 createTask 的用法。
	Images []imageReq `json:"images"`
}

// imageReq 一张待发图片的请求载荷。
//
// Data 是**纯 base64**（不带 `data:image/png;base64,` 前缀）—— 带前缀是最常见的
// 误用，且症状（图片损坏/协议报错）离根因很远，故在 agent 层专门为它写了一条错误。
type imageReq struct {
	Data     string `json:"data"`
	MimeType string `json:"mime_type"`
}

// toImageInputs 把请求里的图片载荷转成 agent 层类型。
func toImageInputs(reqs []imageReq) []agent.ImageInput {
	if len(reqs) == 0 {
		return nil
	}
	out := make([]agent.ImageInput, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, agent.ImageInput{Data: r.Data, MimeType: strings.ToLower(strings.TrimSpace(r.MimeType))})
	}
	return out
}

func (s *Server) createTask(c *gin.Context) {
	var req createTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Trim 首尾空白：PWA 文本框常带末尾换行，存进 task 的 prompt 也会带 \n（见 createTask 注释）。
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is empty"})
		return
	}

	// 选路前先校验图片入参：非法输入必须在这里、且**在任何落库之前**被拒绝。
	// 放行到 agent 层的代价不只一句晚来的报错 —— 任务会先被 store.Create 持久化、
	// 事件里写进假元数据（如 bytes:0 的空图），再以 failed 收场，留下需要人工清理的垃圾。
	// 400 而不是 500：这是**用户可修正**的输入问题（换格式 / 去掉图）。
	reqImages := toImageInputs(req.Images)
	if err := agent.ValidateImages(reqImages); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Agent 选路：校验后落到 task 上，TaskRunner 在 Open 会话时据此选 agent。
	// 空串归一为默认（Claude Code），未知 agent 直接 400（不静默兜底，见 resolveAgent）。
	agentName, err := s.resolveAgent(strings.TrimSpace(req.Agent))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 项目不再预注册：一律用 project_path（绝对路径），deriveProjectID 派生稳定 id。
	abs, err := s.resolveTaskPath(req.ProjectPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	projectID := deriveProjectID(abs)
	projectPath := abs
	worktreePath := abs // 直接用原路径，run 里跳过 worktree 创建

	// 预置首条 user 事件（seq=1）：POST 返回即带该事件，前端立刻渲染用户气泡，
	// 且已持久化，WS 推送不会丢。续问事件由 Resume 以 EventUser 追加，风格一致。
	// 带图时记图片**元数据**（类型/大小/哈希），base64 本体绝不入任务文件
	// —— 理由见 model.TaskEvent.Images。
	task, err := s.store.Create(&model.Task{
		Source:       model.SourceHTTP,
		Agent:        agentName,
		Model:        strings.TrimSpace(req.Model),
		ProjectID:    projectID,
		ProjectPath:  projectPath,
		WorktreePath: worktreePath,
		Prompt:       req.Prompt,
		Events: []model.TaskEvent{{
			Type: model.EventUser, Text: req.Prompt, Seq: 1, At: time.Now(),
			Images: core.ImageMeta(reqImages),
		}},
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.bus.Publish(core.Event{Type: "task_created", TaskID: task.ID, Task: task})
	// 带图走 StartRich（图只在内存过一次手，见 core.StartRich）；不带图仍走 Start。
	if len(reqImages) > 0 {
		s.runner.StartRich(c.Request.Context(), task, reqImages)
	} else {
		s.runner.Start(c.Request.Context(), task)
	}
	// 异步生成一句话标题（大模型摘要）：不阻塞创建，生成后经 WS 推送替换前端截断标题
	s.runner.GenerateTitleAsync(task.ID)
	c.JSON(http.StatusCreated, task)
}

// ErrNoDefaultProjectPath 既没传 project_path、设置里也没有默认目录时的错误文案。
//
// 为什么**不**在这里临时造一个目录（例如自动建 <dataRoot>/workspace 再落进去）：
// 建任务的地方没有任何"用户看着某个目录"的语境，静默选一个目录意味着 agent 会在
// 用户没预期的位置改代码；而正确解法就在设置页，一句指路的报错比一次惊奇的正确便宜。
const ErrNoDefaultProjectPath = "未指定项目路径，且设置里没有「默认工作目录」；请先到设置页配置，或在新建任务时选择路径"

// resolveTaskPath 求本次任务的工作目录：显式路径优先，空则回落设置里的默认目录。
//
// 返回绝对后的路径，且**保证它是已存在的目录** —— 两种来源走同一段校验，
// 因为"设置里写错了"与"请求里写错了"对用户是同一件事，报错文案也该同构。
func (s *Server) resolveTaskPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	fromSettings := false
	if p == "" {
		if s.settings == nil {
			return "", fmt.Errorf("%s", ErrNoDefaultProjectPath)
		}
		p = strings.TrimSpace(s.settings.Get().DefaultProjectPath)
		if p == "" {
			return "", fmt.Errorf("%s", ErrNoDefaultProjectPath)
		}
		fromSettings = true
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		if fromSettings {
			// 明确点出"这是来自设置的值"：否则用户会以为前端选了错目录，
			// 而实际改的地方在设置页（默认目录被删掉/挪走了）。
			return "", fmt.Errorf("默认工作目录不存在或不是目录：%s（来自设置，请到设置页修改）", abs)
		}
		return "", fmt.Errorf("path not found or not a directory: %s", abs)
	}
	return abs, nil
}

// deriveProjectID 从绝对路径派生一个稳定的 project ID（用于分组与并发限流）。
// 取最后一段目录名，空则用 "custom"。
func deriveProjectID(absPath string) string {
	base := filepath.Base(absPath)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "custom"
	}
	return base
}

// normalizeProjectKey 统一 listTasks 的分组键。
// 历史数据可能混用 "/" 与 "\"（如 G:/... vs G:\...）且 Windows 路径大小写不敏感，
// 归一后避免同一项目被分成两组（对应前端侧栏的重复分组问题）。
func normalizeProjectKey(p string) string {
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return filepath.Clean(p)
}

func (s *Server) listTasks(c *gin.Context) {
	// 只要事件的**条数**，不要内容：ListSummaryEvents 不克隆那 3.9 万条事件
	// （实测全库任务 33.9MB，事件占单任务 JSON 的 83%），而本接口下发的只有 3.3MB
	// 轻量视图。见 core.TaskStore.ListSummaryEvents 的判据。
	tasks, counts := s.store.ListSummaryEvents()
	// 按 project_path 分组（REQ-01）
	groups := map[string]*taskGroup{}
	order := []string{}
	for i, t := range tasks {
		key := normalizeProjectKey(t.ProjectPath)
		g, ok := groups[key]
		if !ok {
			g = &taskGroup{ProjectPath: t.ProjectPath}
			if t.ProjectID != "" {
				g.ProjectID = t.ProjectID
			}
			groups[key] = g
			order = append(order, key)
		}
		// 列表只给轻量视图：事件流是详情页的重载荷（见 model.TaskSummary）
		n := 0
		if i < len(counts) {
			n = counts[i]
		}
		g.Tasks = append(g.Tasks, model.NewTaskSummary(t, n))
		g.count(t.Status)
	}
	out := make([]*taskGroup, 0, len(order))
	for _, k := range order {
		out = append(out, groups[k])
	}
	c.JSON(http.StatusOK, gin.H{"projects": out})
}

type taskGroup struct {
	ProjectID   string               `json:"project_id"`
	ProjectPath string               `json:"project_path"`
	Counts      map[string]int       `json:"counts"`
	Tasks       []*model.TaskSummary `json:"tasks"`
}

func (g *taskGroup) count(st model.TaskStatus) {
	if g.Counts == nil {
		g.Counts = map[string]int{}
	}
	g.Counts[string(st)]++
}

func (s *Server) getTask(c *gin.Context) {
	t, ok := s.store.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.JSON(http.StatusOK, t)
}

// taskCapabilities GET /api/tasks/:id/capabilities：该任务会话**此刻**的能力位。
//
// 为什么单开一个接口而不是塞进 Task DTO：这些能力来自 ACP 握手（对端在 Initialize
// 里声明什么），是**会话级的运行时事实**，而 Task 是持久化模型 —— 把运行时能力
// 写进落盘结构，会立刻产生"磁盘上的那份说的和实际不符"（重启后会话没了，
// 记录里却还写着支持收图）。
//
// 前端在进入详情页后问一次，据此决定要不要显示加图入口。
func (s *Server) taskCapabilities(c *gin.Context) {
	id := c.Param("id")
	if _, ok := s.store.Get(id); !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"image_prompt": s.runner.SupportsImagePrompt(id),
	})
}

// backfillTitles POST /api/admin/backfill-titles：为所有"有 prompt 但没标题"的任务补标题。
//
// **仅内网**（与自重启同档，走 BindOpGateMiddleware）：每条任务都会起一次 agent 进程，
// 外网可触发等于把本机资源变成外部可驱动的东西。
//
// 存在的理由：标题只在创建那一刻生成，失败就再也补不回来。2026-10-16 起有 4 天
// "标题全线为空"，而当时唯一的补救是重新建一次任务。这个入口让用户修好凭据后
// 一条命令就能把那批补上。
//
// body（可选）：{"force": true} —— 连本进程内已尝试过的任务一起重扫
// （默认跳过：它们刚失败过，重扫只会再起一轮 agent）。
func (s *Server) backfillTitles(c *gin.Context) {
	var req struct {
		Force bool `json:"force"`
	}
	// body 可为空：PWA/curl 不带 body 时按默认（不 force）处理。
	_ = c.ShouldBindJSON(&req)
	n := s.runner.BackfillTitles(req.Force)
	c.JSON(http.StatusAccepted, gin.H{"scheduled": n, "force": req.Force})
}

type interveneReq struct {
	Kind       string `json:"kind" binding:"required"` // "decision" | "append_prompt"
	DecisionID string `json:"decision_id"`
	Choice     string `json:"choice"` // approve | approve_session | deny
	Text       string `json:"text"`
	// Model 续问（append_prompt）这一轮要用的模型（见 GET /api/agents/{agent}/models），
	// 空 = 沿用会话当前路由。取值同样是 agent 下发的**不透明选择值**，原样回传。
	//
	// 只在续问路径（Resume → 新一轮 prompt）有意义；对 decision 忽略，也不落库
	// —— 它是**按轮**的选择，作用是"这条消息换个模型跑"，不是改任务的长期模型。
	Model string `json:"model"`
	// Images 随这条消息一起发出的图片（可选）。与 Model 同为**按轮**载荷，不落库。
	//
	// 只对终态/choice 的 append_prompt（走 Resume 起新进程）有意义。两类请求**不收图**：
	//   - decision：那是回答一张审批卡，没有"这条消息"可言；
	//   - running / waiting_input(approval)：走 stdin 注入，那是纯文本通道。
	// 给这两类带图会被**明确 400**（而不是静默丢图，见 intervene 里的说明）。
	Images []imageReq `json:"images"`
}

func (s *Server) intervene(c *gin.Context) {
	id := c.Param("id")
	t, ok := s.store.Get(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	// 终态任务（completed/failed/cancelled）允许 append_prompt 续问：走 Resume 复用 session 重跑一轮。
	// decision 仅对 waiting_input 生效；running/waiting_input 的 append_prompt 走 stdin 注入。
	isTerminal := t.Status == model.TaskCompleted || t.Status == model.TaskFailed || t.Status == model.TaskCancelled
	if t.Status != model.TaskWaitingInput && t.Status != model.TaskRunning && !isTerminal {
		c.JSON(http.StatusConflict, gin.H{"error": "task not interventionable: " + string(t.Status)})
		return
	}
	var req interveneReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 图片入参同样在**任何副作用之前**校验（续问会 append 事件 / RecordIntervention）。
	// 与 createTask 同一口径、同一份规则（agent.ValidateImages）：不合法就 400，
	// 别等到 Resume→PromptRich 才报错，那时事件已经写进任务了。
	reqImages := toImageInputs(req.Images)
	if err := agent.ValidateImages(reqImages); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// 终态任务只接受 append_prompt（续问）；decision 对终态无意义
	if isTerminal && req.Kind != "append_prompt" {
		c.JSON(http.StatusConflict, gin.H{"error": "terminal task only supports append_prompt"})
		return
	}
	// 路径 B（choice waiting_input）：claude 进程已 end_turn 退出，恢复必须走 Resume。
	// 只接受 append_prompt（选项文本）；decision 对 choice 无意义。
	if t.Status == model.TaskWaitingInput && t.CurrentDecision != nil &&
		t.CurrentDecision.Kind == model.DecisionKindChoice {
		if req.Kind != "append_prompt" {
			c.JSON(http.StatusConflict, gin.H{"error": "choice decision requires option text (append_prompt)"})
			return
		}
		if err := s.runner.ResumeRich(id, req.Text, req.Model, reqImages); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		// choice 续问也是一次干预（R6）：进程已退出走 Resume，但"用户被请回来
		// 做了一件事"这个事实与 approve/deny 同价。
		s.runner.RecordIntervention(id, model.Intervention{
			TaskID: id, Kind: "append_prompt", Text: req.Text, Source: model.SourceHTTP,
		})
		c.JSON(http.StatusAccepted, gin.H{"ok": true, "resumed": true})
		return
	}
	in := model.Intervention{
		TaskID: id, Kind: req.Kind, DecisionID: req.DecisionID,
		Choice: req.Choice, Text: req.Text, Source: model.SourceHTTP,
	}
	if isTerminal {
		// 同步检查 Resume 前置条件（worktree/session 存在），异步启动
		if err := s.runner.ResumeRich(id, req.Text, req.Model, reqImages); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		// 终态续问同样是一次干预（R6 / AC-R6-01）：入口在 intervene，口径不变
		s.runner.RecordIntervention(id, model.Intervention{
			TaskID: id, Kind: "append_prompt", Text: req.Text, Source: model.SourceHTTP,
		})
		c.JSON(http.StatusAccepted, gin.H{"ok": true, "resumed": true})
		return
	}
	// 运行中 / waiting_input(approval) 的输入走 stdin 注入，那是**纯文本通道**，
	// 收不了图（见 interveneReq.Images 的说明）。带图到这里必须**明确拒绝**。
	//
	// 为什么不能放任它往下走：Intervene 收的是 model.Intervention，那个结构里
	// 根本没有图片字段 —— 图会被**静默丢掉**，用户以为 agent 看到了图，
	// 而它只会对着没图的上下文说些不相干的话。这正是 agent 层一直严防的失败模式
	// （见 session.go「静默丢图是最坏的选择」），接口层不能自己制造一个。
	if len(reqImages) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "运行中的任务不支持随消息发图（stdin 注入是纯文本通道）；请等该轮结束后再续问带图",
		})
		return
	}
	if err := s.runner.Intervene(id, in); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) cancelTask(c *gin.Context) {
	id := c.Param("id")
	if err := s.runner.Cancel(id); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) deleteTask(c *gin.Context) {
	id := c.Param("id")
	// 若仍在跑先取消
	_ = s.runner.Cancel(id)
	// 保活的 ACP 会话跨轮存活：删除任务时显式关闭，避免会话/子进程残留为孤儿
	s.runner.CloseAgentSession(id)
	// Feedback P0：任务删除时一并清掉 checkpoint 快照（preview 由 WatchBus 收）
	if s.feedback != nil {
		s.feedback.Cleanup(id)
	}
	// P1：清掉 checks 重跑记录
	if s.checks != nil {
		s.checks.Cleanup(id)
	}
	if err := s.store.Delete(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	s.bus.Publish(core.Event{Type: "task_deleted", TaskID: id})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// hookCallback PreToolUse hook 子进程回连：阻塞等决策。
func (s *Server) hookCallback(c *gin.Context) {
	var p core.HookPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(p.TaskID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task_id required"})
		return
	}
	res := s.hooks.RegisterPending(p)
	c.JSON(http.StatusOK, res)
}
