package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"pieqi/internal/config"

	"github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ACPAgent 基于 acp-go-sdk 的 ACP 协议适配器（M1 内核）。
//
// spawn 一个 ACP agent 进程（Claude Code 经 npx 官方 TS 适配器；其他 agent 经各自 --acp），
// 完成 capabilities handshake（acp.ProtocolVersionNumber=1），用 JSON-RPC over stdio 管理会话生命周期。
//
// SDK 真实 API 映射（spec 描述 → acp-go-sdk v0.13.5 实现，以 SDK 为准）：
//   - NewSession        → (*acp.ClientSideConnection).NewSession(acp.NewSessionRequest{Cwd, McpServers})
//   - SendPrompt        → (*acp.ClientSideConnection).Prompt(acp.PromptRequest{SessionId, Prompt: []acp.ContentBlock{acp.TextBlock(...)}})
//     Prompt 阻塞到 agent 结束该轮（返回 acp.PromptResponse{StopReason}），
//     期间 agent 推送的 SessionUpdate 经本实现的 acp.Client.SessionUpdate 回调到达。
//   - OnContentDelta    → 由 acp.Client.SessionUpdate 触发：u.AgentMessageChunk.Content.Text（回答）/ u.AgentThoughtChunk.Content.Text（思考）
//   - OnPermissionRequest → 由 acp.Client.RequestPermission 触发（M3 启用；M1 无回调时自动放行）
//   - Approve/Deny      → 投递到 pendingPerms[reqID] channel，唤醒 RequestPermission 返回
//     （映射 acp.NewRequestPermissionOutcomeSelected / NewRequestPermissionOutcomeCancelled）
//   - InjectToolResult  → ACP 不支持（agent 自行执行工具并报 ToolCallUpdate），返回 ErrNotSupported
//   - Cancel            → (*acp.ClientSideConnection).Cancel(acp.CancelNotification{SessionId})
//   - Close             → (*acp.ClientSideConnection).CloseSession（best-effort）+ cmd.Process.Kill
//   - Done              → (*acp.ClientSideConnection).Done()（连接断开时关闭）+ cmd.Wait()，合并到单一 channel
//
// 注：spec 提到 "SessionUpdate 回调" 与 "RequestPermission 回调"——SDK 把它们建模为
// acp.Client 接口的方法（agent→client 方向的 JSON-RPC 请求/通知），由本结构实现。
type ACPAgent struct {
	cfg     config.ACPConfig
	logger  *zap.Logger
	cmdName string
	cmdArgs []string

	// taskID 是拥有本会话的任务 id（SessionConfig.TaskID 存入）。
	// spawn 时以 PIEQI_TASK_ID 注入子进程环境，供 agent 内 shell 标识自身来源；
	// 空值表示非任务场景，此时不注入。见 SessionConfig.TaskID 的说明。
	taskID string

	// workDir 是子进程的工作目录（= 任务的项目/worktree 目录），
	// 在 NewSession 里于 spawn 之前落定，Start 时作为 cmd.Dir。
	//
	// 为什么必须设它（2026-10-07 修的 bug）：dsh 的沙箱配置是
	// `workspaceRoot: !!js process.cwd()` —— 它把**自己的 cwd** 当作"工作区"，
	// 工作区内的写入不需要审批、工作区外的要。而 pieqi 自身跑在 `~/.pieqi/bin`
	// （工作区之外，见「坑 5」），若不显式设 cmd.Dir，dsh 会**继承那个 cwd**，
	// 于是"工作区"变成了 ~/.pieqi/bin —— 结果 agent 在项目目录里
	// `go build`、`npm run build`、写文件全被当成越界，每次都要人工审批。
	//
	// 设成项目目录后：项目内自由读写，项目外（别的盘、~/.pieqi 等）照旧审批，
	// 沙箱边界仍然有效 —— 修的是"工作区指错了"，不是放宽限制。
	//
	// 多项目天然正确：AgentManager 按任务建会话，每个会话一个 ACPAgent 实例，
	// workDir 各随其任务的项目走（换任务即换工作区）。
	workDir string

	// binPath 是实际 exec 的路径：由 cmdName 经 spawnNameResolver 解析而来
	// （裸名补查常见安装落点，见 resolveSpawnName）。解析不到时等于 cmdName，
	// 让 exec 报出标准错误。cmdName 保持"配置里写的原样"，供诊断/测试断言。
	binPath string

	// 进程与连接（Start 后填充；Start 之前为 nil）
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	conn   acpConn
	done   chan struct{} // 进程退出或连接断开时关闭（Done() 返回它）

	// lifeCtx/lifeCancel：agent 进程的**自持**生命周期 ctx（构造时 WithCancel(Background)，
	// Close 时 cancel）。spawn 只绑它、不绑调用方的 per-turn ctx —— 调用方 ctx 在轮末即被
	// cancel（TaskRunner.runACP 的 defer cancel()），若绑它，跨轮保活（runACPTurn keepAlive=true）
	// 会在轮末被 exec.CommandContext 顺手杀掉 agent 进程，下一轮续问写向已关闭的 stdin
	// 直接失败（"write |1: file already closed"）。进程终止统一由 Close 负责（优雅 EOF →
	// 超时强杀 → lifeCancel 兜底），与调用方 turn 边界解耦。
	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	// agentCaps 握手时 agent 声明的能力（Initialize 后填充）。NewSession 据此决定
	// 续问走 session/load（LoadSession=true）还是 session/resume。
	agentCaps acp.AgentCapabilities

	startOnce sync.Once
	startErr  error
	started   bool

	// 回调（OnXxx 注册；并发读用 mu 保护）
	cbMu       sync.RWMutex
	onDelta    ContentDeltaFunc
	onPerm     PermissionRequestFunc
	onToolCall ToolCallUpdateFunc

	// 权限请求挂起表：reqID -> chan PermissionResponse。
	// RequestPermission 注册并阻塞等；Approve/Deny 投递响应后唤醒。
	permMu       sync.Mutex
	pendingPerms map[string]chan PermissionResponse

	// toolInputs 是 toolCallId -> 工具入参（RawInput）的记忆表，专为**审批时补齐命令原文**。
	//
	// 为什么需要它：审批判定要读**工具名**与**命令原文**，但 dsh 的
	// RequestPermission **两者都不带**（实测 title/kind/rawInput 全空）；
	// 而它在 session/update 里发过 tool_call 的 title（= 工具名，如 "pwsh"）
	// 与 rawInput（含 command），两边用同一个 toolCallId 关联。
	//
	// ⚠️ 工具名（title）与命令原文（rawInput）**必须一起缓存**：
	// 分级链的第一道门 applyToolKindFix 是**按工具名**查表的
	// （pwsh → execute），拿不到名字就整条链失效 —— 实测 2026-10-07 15:03
	// 我自己那两条 `git add; git commit`（本该 L1 免审）就是因为 title 为空
	// 而落到 L2 弹卡的。
	//
	// ⚠️ **顺序不保证，必须等**（2026-10-07 实测修正，见 toolInputWait 注释）：
	// dsh 侧确实在发 requestPermission 前 `await drainUpdates()`，acp-go-sdk 也确实有
	// "通知屏障"（响应带 notificationWatermark，调用方等它排空）—— 但这两条保证的都是
	// **协议线上的顺序**（dsh 先发完 update 再发请求），**不等于 pieqi 侧两个回调的
	// 处理完成顺序**：SessionUpdate（写索引）与 RequestPermission（读索引）在 pieqi
	// 内部是并发的。实测本会话 12:39:37 那次 push 的审批就**跑赢了**它自己的 tool_call
	// （tool_use 事件 seq 1661 确实存在、command 也在，但审批那一刻索引还是空的）。
	// ⇒ 读索引必须带**有界等待**，见 toolInputWait。
	//
	// 增长边界：一个会话内的工具调用量级是百~千（实测最大任务 1347 条），
	// 每条几十字节~几KB，随会话销毁一并释放（见 Close），不做逐出。
	toolInputsMu sync.Mutex
	toolInputs   map[string]toolCallMemo
	// toolInputCond 在写入新入参时广播，唤醒正在等待该 id 的审批方。
	// 与 toolInputs 共用 toolInputsMu（sync.Cond 的约定：等待与判定在同一把锁下）。
	toolInputCond *sync.Cond

	// closeOnce 守护 Close 的幂等；doneOnce 守护 a.done 的关闭。
	// 两者分离，避免 Close 内调 markDone 时重入同一个 Once 导致死锁。
	closeOnce sync.Once
	doneOnce  sync.Once
}

// 编译期断言：ACPAgent 同时实现 AgentAdapter 与 acp.Client。
var (
	_ AgentAdapter = (*ACPAgent)(nil)
	_ acp.Client   = (*ACPAgent)(nil)
)

// acpConn 抽象 ACP 客户端连接（*acp.ClientSideConnection 的方法子集），让 ACPAgent.NewSession
// 的 load/resume 路径可单测：生产由 *acp.ClientSideConnection 实现，测试注入 fake。
// 仅收录 ACPAgent 实际调用的方法（Initialize/NewSession/LoadSession/ResumeSession/
// Prompt/Cancel/CloseSession/Done/SetLogger）。
type acpConn interface {
	Initialize(ctx context.Context, params acp.InitializeRequest) (acp.InitializeResponse, error)
	NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error)
	LoadSession(ctx context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error)
	ResumeSession(ctx context.Context, params acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error)
	Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error)
	Cancel(ctx context.Context, params acp.CancelNotification) error
	CloseSession(ctx context.Context, params acp.CloseSessionRequest) (acp.CloseSessionResponse, error)
	Done() <-chan struct{}
	SetLogger(l *slog.Logger)
}

// 编译期断言：*acp.ClientSideConnection 满足 acpConn（生产路径）。
var _ acpConn = (*acp.ClientSideConnection)(nil)

// NewACPAgent 创建一个未启动的 ACPAgent。
// 仅做配置解析与 spawn 命令构建；不 spawn 进程，可安全用于测试 spawn 参数等。
// 调用 Start（或首个 NewSession 触发 ensureStarted）后才 spawn 进程与握手。
func NewACPAgent(cfg config.ACPConfig, logger *zap.Logger) *ACPAgent {
	if logger == nil {
		logger = zap.NewNop()
	}
	if cfg.InitTimeout <= 0 {
		cfg.InitTimeout = 30 * time.Second
	}
	name, args := buildSpawnCommand(cfg)
	lifeCtx, lifeCancel := context.WithCancel(context.Background())
	return &ACPAgent{
		cfg:          cfg,
		logger:       logger,
		cmdName:      name,
		cmdArgs:      args,
		binPath:      spawnNameResolver(name),
		done:         make(chan struct{}),
		pendingPerms: make(map[string]chan PermissionResponse),
		toolInputs:   make(map[string]toolCallMemo),
		lifeCtx:      lifeCtx,
		lifeCancel:   lifeCancel,
	}
}

// initToolInputCond 惰性初始化条件变量（需在 toolInputsMu 上）。
//
// 惰性而非构造器里直接建：NewACPAgent 的返回值字面量里加不上
// sync.NewCond(&a.toolInputsMu)（自引用），且单测会直接 new(ACPAgent) 绕开构造器。
func (a *ACPAgent) initToolInputCond() {
	if a.toolInputCond == nil {
		a.toolInputCond = sync.NewCond(&a.toolInputsMu)
	}
}

// acpWorkDir 校验并返回可用的子进程工作目录；不可用时返回空串（由调用方跳过 cmd.Dir）。
//
// 返回空串的两种情况，都**不能**设 cmd.Dir：
//   - 未配置（非任务场景，如标题生成）；
//   - 目录不存在或不是目录 —— exec.Cmd 对不存在的 Dir 会在 Start 时报错
//     （"chdir ...: no such file or directory"），那会让整个会话起不来。
//     这里宁可不设（退回继承 pieqi 的 cwd、即旧行为），也不要让 spawn 失败。
func acpWorkDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return ""
	}
	return dir
}

// buildSpawnCommand 由 ACPConfig 推导 spawn 命令分词。
// cfg.SpawnCommand 非空则优先用它；否则按 AgentType 取默认。
func buildSpawnCommand(cfg config.ACPConfig) (string, []string) {
	name, args := defaultSpawnCommand(cfg.AgentType)
	explicit := len(cfg.SpawnCommand) > 0
	if explicit {
		name, args = cfg.SpawnCommand[0], cfg.SpawnCommand[1:]
	}
	// npm shim → node 直启（原因与判据见 nodeCLIAgent）。claude-code 那条改写会决定
	// 「用哪个包的入口」，用户既然显式写了命令就绝不替他换包，因此只在默认路径上改写；
	// dsh 的命令名对应的包是固定的，显式写法（如 [dsh,--profile,acp]）同样安全改写。
	if entryRel, keepArgs, ok := nodeCLIAgent(cfg.AgentType, name); ok && (keepArgs || !explicit) {
		// 命中已安装的全局入口就绕开 shim；找不到才按原命令走（Unix 上 npx / 全局 bin 正常）。
		// config 无需写绝对路径。
		if entry, ok := nodeCLIResolver(entryRel); ok {
			if keepArgs {
				return "node", append([]string{entry}, args...)
			}
			return "node", []string{entry}
		}
	}
	return name, args
}

// nodeCLIAgent 判断「这个 spawn 命令名其实是 npm 发的 shim，应该换成 node 直启入口」。
// keepArgs=true 时原参数要保留（dsh 的 --profile acp 是真参数）；false 时整段丢弃
// （claude-code 的 `-y @scope/pkg@latest` 是 npx 的包选择器，对 node 直启无意义）。
func nodeCLIAgent(agentType, name string) (entryRel string, keepArgs bool, ok bool) {
	switch {
	case agentType == "claude-code" && name == "npx":
		return filepath.Join("@agentclientprotocol", "claude-agent-acp", "dist", "index.js"), false, true
	case name == "dsh":
		return filepath.Join("@deepseek-ai", "dsh", "lib", "bin.js"), true, true
	}
	return "", false, false
}

// nodeCLIResolver 定位 npm 全局入口的可覆盖钩子（测试注入用）；生产默认 resolveNodeCLI。
var nodeCLIResolver = resolveNodeCLI

// resolveNodeCLI 在若干 node_modules 根下找 entryRel 指向的入口文件，返回绝对路径
// （不依赖进程 cwd 之外的配置）。候选顺序：项目本地 node_modules →
// %APPDATA%\npm\node_modules（Windows 上 npm 的默认 prefix）→
// node 可执行文件同级 node_modules → Unix 系统 prefix。
//
// 第 3 项不是凑数：托管式 node（volta / scoop / 自定义 prefix）把全局包装在 node
// 自己的目录旁边，%APPDATA%\npm 压根不存在（本机实测就是这种形态）。
func resolveNodeCLI(entryRel string) (string, bool) {
	var roots []string
	if cwd, err := os.Getwd(); err == nil {
		roots = append(roots, filepath.Join(cwd, "node_modules"))
	}
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		roots = append(roots, filepath.Join(appdata, "npm", "node_modules"))
	}
	if nodePath, err := exec.LookPath("node"); err == nil {
		roots = append(roots, filepath.Join(filepath.Dir(nodePath), "node_modules"))
	}
	roots = append(roots, "/usr/local/lib/node_modules", "/usr/lib/node_modules")
	for _, r := range roots {
		abs, err := filepath.Abs(filepath.Join(r, entryRel))
		if err != nil {
			continue
		}
		if st, err := os.Stat(abs); err == nil && !st.IsDir() {
			return abs, true
		}
	}
	return "", false
}

// defaultSpawnCommand 按 agent 类型返回默认 spawn 命令分词。
//   - claude-code：经官方 TS 适配器 @agentclientprotocol/claude-agent-acp（原 @zed-industries/claude-code-acp，
//     包名已迁移至 @agentclientprotocol 命名空间），由 npx 拉起 Node.js 进程。
//   - qodercli / codex 等：原生 ACP，直接 spawn 自身 --acp。
//   - dsh：ACP 是一个 profile，`dsh --profile acp`（等价简写 `dsh acp`）。
//   - 其他：按 "<agentType> --acp" 兜底。
func defaultSpawnCommand(agentType string) (string, []string) {
	switch agentType {
	case "", "claude-code":
		return "npx", []string{"-y", "@agentclientprotocol/claude-agent-acp@latest"}
	case "qodercli":
		return "qodercli", []string{"--acp"}
	case "codex":
		return "codex", []string{"--acp"}
	case "dsh":
		return "dsh", []string{"--profile", "acp"}
	default:
		return agentType, []string{"--acp"}
	}
}

// spawnNameResolver 把配置里的 spawn 命令名解析为可直接 exec 的路径。
// 可覆盖（测试注入用），同 adapterResolver 模式。
var spawnNameResolver = resolveSpawnName

// vendorHomeDirs 命令名 → 用户主目录下的安装根目录名。
// 注意 **vendor 目录名与命令名不同**（qodercli 装在 ~/.qoder/），故必须显式映射，
// 不能靠 ".<命令名>" 推导。
var vendorHomeDirs = map[string][]string{
	"qodercli": {".qoder"},
}

// spawnHomeEnvs 命令名 → 声明安装根的环境变量名（安装器可选提供）。
var spawnHomeEnvs = map[string][]string{
	"qodercli": {"QODER_HOME"},
}

// spawnFallbackPaths 返回裸名 LookPath 失败后可按序尝试的候选**文件**路径。
func spawnFallbackPaths(name string) []string {
	if name == "" {
		return nil
	}
	exe := name
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}

	// 收集安装根：用户主目录下的 vendor 目录 + 环境变量声明的根。
	var roots []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		for _, vd := range vendorHomeDirs[name] {
			roots = append(roots, filepath.Join(home, vd))
		}
	}
	for _, env := range spawnHomeEnvs[name] {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			roots = append(roots, v)
		}
	}

	var files []string
	seen := make(map[string]bool)
	for _, r := range roots {
		for _, p := range []string{
			// Windows 安装器的典型落点：<root>/bin/<name>/<name>.exe
			// —— bin 下还有一层**与可执行文件同名**的子目录（qodercli 就是这种）
			filepath.Join(r, "bin", name, exe),
			filepath.Join(r, "bin", exe),
			filepath.Join(r, exe),
		} {
			if !seen[p] {
				seen[p] = true
				files = append(files, p)
			}
		}
	}
	return files
}

// resolveSpawnName 解析 spawn 命令名，返回可直接 exec 的路径：
//  1. 空串 → 原样返回（由调用方报"empty spawn command"）；
//  2. 含路径分隔符（相对/绝对路径）→ **原样返回**，绝不改写用户显式指定的路径；
//  3. exec.LookPath 命中 → 返回解析结果；
//  4. 依次尝试 spawnFallbackPaths → 命中即返回绝对路径；
//  5. 都没命中 → 原样返回裸名（让 exec 报出标准错误，调用方再补充提示）。
//
// 动机（2026-10-02 线上排查）：Windows 的 PATH 是**进程启动那一刻从父进程继承的
// 环境快照**，不动态读注册表。安装器只把落点写进用户级 PATH，而宿主（IDE / 常驻
// 服务 / 沙箱 shell）往往早于安装启动 —— 它派生的 pieqi 进程就继承不到该项，裸名
// `qodercli` 直接报 `exec: "qodercli": executable file not found in %PATH%`
// （典型症状：claude 正常、只有 qoder 全挂）。
// 有了这层回退，配置里只写裸名也能跑，且配置可跨机器复制。
func resolveSpawnName(name string) string {
	if name == "" {
		return name
	}
	if strings.ContainsAny(name, `/\`) || filepath.IsAbs(name) {
		return name
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, cand := range spawnFallbackPaths(name) {
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
	}
	return name
}

// CmdName 返回 spawn 命令名（测试与诊断用）。
func (a *ACPAgent) CmdName() string { return a.cmdName }

// CmdArgs 返回 spawn 命令参数（测试与诊断用）。
func (a *ACPAgent) CmdArgs() []string {
	out := make([]string, len(a.cmdArgs))
	copy(out, a.cmdArgs)
	return out
}

// BinPath 返回实际 exec 的可执行文件路径（裸名经 resolveSpawnName 解析后的绝对
// 路径；解析不到时与 CmdName 相同）。测试与诊断用。
func (a *ACPAgent) BinPath() string {
	if a.binPath == "" {
		return a.cmdName
	}
	return a.binPath
}

// Start spawn agent 进程、建立 ClientSideConnection、完成 initialize 握手。
// 非接口方法：供 AgentManager（M4）显式启动；NewSession 也会经 ensureStarted 懒启动。
// 幂等：重复调用返回首次的结果。
func (a *ACPAgent) Start(ctx context.Context) error {
	a.startOnce.Do(func() {
		a.startErr = a.startInternal(ctx)
		if a.startErr == nil {
			a.started = true
		} else {
			// 启动失败：释放自持 ctx（此路径可能没走到 Close，如 cmd.Start 直接失败）。
			// cancel 幂等，若已由 Close 释放过则无副作用。
			a.lifeCancel()
		}
	})
	return a.startErr
}

func (a *ACPAgent) startInternal(ctx context.Context) error {
	if a.cmdName == "" {
		return fmt.Errorf("acp: empty spawn command (agent_type=%q)", a.cfg.AgentType)
	}
	// 绑自持 lifeCtx 而非调用方 ctx：进程存活期 = 会话存活期，不随调用方 turn 结束被取消
	// （见 lifeCtx 字段注释）。调用方 ctx 只用于下面的 initialize 握手超时。
	// binPath：裸名已经 resolveSpawnName 解析（构造期），起不来时日志能直接看到绝对路径。
	binPath := a.BinPath()
	if binPath != a.cmdName {
		a.logger.Info("acp spawn command resolved",
			zap.String("configured", a.cmdName), zap.String("resolved", binPath))
	}
	cmd := exec.CommandContext(a.lifeCtx, binPath, a.cmdArgs...)
	// 子进程的工作目录 = 任务的项目目录（见 acpWorkDir）。
	// 必须在 Start 之前设，且目录必须存在，否则 exec 直接失败。
	if dir := acpWorkDir(a.workDir); dir != "" {
		cmd.Dir = dir
	}
	cmd.Stderr = newLineCollector(a.logger, "acp agent stderr")
	// 把"我是替哪个任务在跑"注入子进程环境。这不是可选装饰：agent 里执行的
	// shell 正是靠读它才能在自己发起的 HTTP 请求里带上 taskId（当前唯一消费者
	// 是 POST /api/admin/restart —— 服务要据此知道该把哪个会话接回来）。
	// 见 SessionConfig.TaskID 的完整理由。空值不注入，保持环境干净。
	if a.taskID != "" {
		cmd.Env = append(os.Environ(), "PIEQI_TASK_ID="+a.taskID)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("acp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("acp: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		// 找不到可执行文件是最常见的一类失败（PATH 未生效 / 装到非 PATH 目录）。
		// 把已尝试的候选落点一并报出来，避免下一个人再去猜安装位置。
		if errors.Is(err, exec.ErrNotFound) {
			hint := ""
			if cands := spawnFallbackPaths(a.cmdName); len(cands) > 0 {
				hint = "，也已尝试安装落点: " + strings.Join(cands, ", ")
			}
			return fmt.Errorf("acp: start %s: %w（未在 PATH 中找到 %q%s；请将其加入 PATH，或在配置里写绝对路径）",
				binPath, err, a.cmdName, hint)
		}
		return fmt.Errorf("acp: start %s: %w", binPath, err)
	}
	a.cmd = cmd
	a.stdin = stdin
	a.stdout = stdout

	// 用本结构作为 acp.Client 实现：SessionUpdate/RequestPermission 等回调落到下面的方法。
	a.conn = acp.NewClientSideConnection(a, stdin, stdout)
	a.conn.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil))) // 静音 SDK 自带日志；后续可桥接 zap

	// 合并进程退出与连接断开到 a.done（对应 Phase 1 liveProc.done）。
	go a.watchExit()

	// initialize 握手（capabilities handshake），用 InitTimeout 兜底防止永久挂起。
	initCtx, cancel := context.WithTimeout(ctx, a.cfg.InitTimeout)
	defer cancel()
	initResp, err := a.conn.Initialize(initCtx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo: &acp.Implementation{
			Name:    "pieqi",
			Title:   acp.Ptr("Pieqi"),
			Version: "0.1.0",
		},
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
	})
	if err != nil {
		// 握手失败：统一走 Close 清理（杀进程 + markDone + 清 pending），避免与 watchExit 双重 close done。
		a.Close(ctx)
		return fmt.Errorf("acp: initialize (protocol v%d): %w", acp.ProtocolVersionNumber, err)
	}
	// 存下 agent 声明的能力，供 NewSession 决定续问走 session/load 还是 session/resume。
	a.agentCaps = initResp.AgentCapabilities
	// agent_caps 必须打进日志：续问走 load 还是 resume 完全由它决定，
	// 而这两条路径对"会话能否被接回"的结论截然不同。不打印它，出问题时
	// 只能靠翻协议流量反推（见 docs/adr 里"重启接回"那节的排查纪要）。
	a.logger.Debug("acp initialized",
		zap.Any("protocol_version", initResp.ProtocolVersion),
		zap.Any("agent_info", initResp.AgentInfo),
		zap.Bool("load_session", a.agentCaps.LoadSession))
	return nil
}

// watchExit 等进程退出或连接断开，关闭 a.done（仅一次）。
func (a *ACPAgent) watchExit() {
	waitCh := make(chan struct{})
	go func() {
		_ = a.cmd.Wait()
		close(waitCh)
	}()
	select {
	case <-waitCh:
	case <-a.conn.Done():
		// 连接先断（agent 关了 stdout）：杀进程促其退出。
		_ = a.cmd.Process.Kill()
		<-waitCh
	}
	a.markDone()
}

func (a *ACPAgent) markDone() {
	a.doneOnce.Do(func() { close(a.done) })
}

// ensureStarted 懒启动：首个 NewSession 触发 Start。
func (a *ACPAgent) ensureStarted(ctx context.Context) error {
	if a.started {
		return nil
	}
	return a.Start(ctx)
}

// --- AgentAdapter 实现 ---

// NewSession 创建 ACP 会话，返回 sessionId。
// 首次调用会懒触发 Start（spawn + initialize 握手）。
//
// cfg.ResumeFrom 非空时为续问路径：复用已有会话上下文而非新建。优先 session/load
// （agent 在 Initialize 声明 LoadSession 能力时），否则 session/resume。会话丢失
// （agent 报错）返回错误，由调用方 surface（不静默失败）。
func (a *ACPAgent) NewSession(ctx context.Context, cfg SessionConfig) (string, error) {
	// 先校验入参，避免无效请求也去 spawn 进程。
	if cfg.Cwd == "" {
		return "", errors.New("acp: NewSession requires Cwd")
	}
	// 身份必须在 ensureStarted（真正 spawn）之前落定 —— spawn 只发生一次，
	// 而环境变量只在那一刻注入。续问路径走的是同一个已存在进程，不会重新
	// inject，所以这里赋值对两条路径都成立（同一任务的 id 不会变）。
	a.taskID = cfg.TaskID
	// 工作目录同样必须在 spawn 前落定：它决定子进程的 cwd，
	// 也就是 dsh 沙箱眼里的 workspaceRoot（见 acpWorkDir 的完整说明）。
	a.workDir = cfg.Cwd
	if err := a.ensureStarted(ctx); err != nil {
		return "", err
	}
	sessCtx, cancel := context.WithTimeout(ctx, a.cfg.InitTimeout)
	defer cancel()

	// 续问路径：ResumeFrom 非空时复用已有会话
	if cfg.ResumeFrom != "" {
		sid := acp.SessionId(cfg.ResumeFrom)
		mcpServers := toACPMcpServers(cfg.MCP)
		// 优先 session/load（agent 声明 LoadSession 能力时），否则 session/resume。
		// 两者都要求 Cwd；load 还要求 McpServers 非 nil（已由 toACPMcpServers 保证）。
		//
		// 两条路径的响应都可能带 configOptions，本实现不使用（不做会话内选模型）。
		// dsh 实测只实现 resume（caps 无 load）。
		if a.agentCaps.LoadSession {
			if _, err := a.conn.LoadSession(sessCtx, acp.LoadSessionRequest{
				Cwd:        cfg.Cwd,
				SessionId:  sid,
				McpServers: mcpServers,
			}); err != nil {
				return "", fmt.Errorf("acp: load session %s: %w", cfg.ResumeFrom, err)
			}
		} else {
			if _, err := a.conn.ResumeSession(sessCtx, acp.ResumeSessionRequest{
				Cwd:        cfg.Cwd,
				SessionId:  sid,
				McpServers: mcpServers,
			}); err != nil {
				return "", fmt.Errorf("acp: resume session %s: %w", cfg.ResumeFrom, err)
			}
		}
		return string(sid), nil
	}

	// 新建会话路径（原逻辑）
	resp, err := a.conn.NewSession(sessCtx, acp.NewSessionRequest{
		Cwd:        cfg.Cwd,
		McpServers: toACPMcpServers(cfg.MCP),
	})
	if err != nil {
		return "", fmt.Errorf("acp: new session: %w", err)
	}
	return string(resp.SessionId), nil
}

// RealSessionID 返回会话的真实 session ID（用于持久化与续问）。
// ACPAgent 的 sessionID 即真实协议资源 ID，直接返回入参。
func (a *ACPAgent) RealSessionID(sessionID string) string { return sessionID }

// SendPrompt 发送一轮 prompt，阻塞到 agent 结束该轮（PromptResponse 返回）。
// 期间 agent 推送的 AgentMessageChunk/AgentThoughtChunk 经 SessionUpdate 回调到达 OnContentDelta。
func (a *ACPAgent) SendPrompt(ctx context.Context, sessionID, prompt string) error {
	if !a.started {
		return errors.New("acp: SendPrompt before Start")
	}
	return a.promptOnce(ctx, sessionID, prompt)
}

// promptOnce 发一轮 prompt，不做任何重试。
func (a *ACPAgent) promptOnce(ctx context.Context, sessionID, prompt string) error {
	if _, err := a.conn.Prompt(ctx, acp.PromptRequest{
		SessionId: acp.SessionId(sessionID),
		Prompt:    []acp.ContentBlock{acp.TextBlock(prompt)},
	}); err != nil {
		return fmt.Errorf("acp: prompt: %w", err)
	}
	return nil
}

// OnContentDelta 注册内容增量回调。
func (a *ACPAgent) OnContentDelta(fn ContentDeltaFunc) {
	a.cbMu.Lock()
	a.onDelta = fn
	a.cbMu.Unlock()
}

// OnPermissionRequest 注册权限请求回调（M3 启用；M1 不注册时自动放行）。
func (a *ACPAgent) OnPermissionRequest(fn PermissionRequestFunc) {
	a.cbMu.Lock()
	a.onPerm = fn
	a.cbMu.Unlock()
}

// OnToolCallUpdate 注册工具调用更新回调。
func (a *ACPAgent) OnToolCallUpdate(fn ToolCallUpdateFunc) {
	a.cbMu.Lock()
	a.onToolCall = fn
	a.cbMu.Unlock()
}

// Approve 批准权限请求：按 ReqID 选中指定 OptionID，唤醒等待中的 RequestPermission。
// 未找到 ReqID（已超时/取消/不存在）返回错误。
func (a *ACPAgent) Approve(ctx context.Context, reqID, optionID string) error {
	ch, ok := a.takePending(reqID)
	if !ok {
		return fmt.Errorf("acp: no pending permission for req %q", reqID)
	}
	select {
	case ch <- PermissionResponse{Selected: true, OptionID: optionID}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// Deny 拒绝权限请求：按 ReqID 投递拒绝响应。
// 选 reject 选项的语义由调用方在 Approve(reqID, rejectOptionID) 时体现；
// 本方法投递 Cancelled（适用于无 reject 选项或要求中止该轮的情况）。
func (a *ACPAgent) Deny(ctx context.Context, reqID string) error {
	ch, ok := a.takePending(reqID)
	if !ok {
		return fmt.Errorf("acp: no pending permission for req %q", reqID)
	}
	select {
	case ch <- PermissionResponse{Selected: false}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// RespondPermission 对权限请求给出中性审批响应：allow=true→Approve(reqID, optionID)，
// allow=false→Deny(reqID)。与 Approve/Deny 语义一致。
func (a *ACPAgent) RespondPermission(ctx context.Context, reqID string, allow bool, optionID string) error {
	if allow {
		return a.Approve(ctx, reqID, optionID)
	}
	return a.Deny(ctx, reqID)
}

// takePending 取出并删除一个 pending channel（投递完后不再保留）。
func (a *ACPAgent) takePending(reqID string) (chan PermissionResponse, bool) {
	a.permMu.Lock()
	defer a.permMu.Unlock()
	ch, ok := a.pendingPerms[reqID]
	if ok {
		delete(a.pendingPerms, reqID)
	}
	return ch, ok
}

// InjectToolResult ACP 路径不支持：工具由 agent 自行执行并报 ToolCallUpdate。
// PrintAgent 回退路径（M4）才实现真正的 stdin 注入。
func (a *ACPAgent) InjectToolResult(ctx context.Context, sessionID, toolCallID string, result string, isError bool) error {
	return ErrNotSupported
}

// Cancel 取消正在进行的 prompt turn：发 session/cancel 通知。
func (a *ACPAgent) Cancel(ctx context.Context, sessionID string) error {
	if !a.started {
		return errors.New("acp: Cancel before Start")
	}
	if err := a.conn.Cancel(ctx, acp.CancelNotification{SessionId: acp.SessionId(sessionID)}); err != nil {
		return fmt.Errorf("acp: cancel: %w", err)
	}
	return nil
}

// closeGracefulWait 优雅关闭时等待 adapter 进程自行退出的上限；超时才强杀兜底。
const closeGracefulWait = 5 * time.Second

// Close 关闭 adapter：best-effort 关会话 + 优雅退出 + 强杀兜底。幂等。
//
// 孤儿防线：不能直接 Process.Kill——Windows 的 TerminateProcess 只杀 node adapter 自身，
// 其子进程 claude.exe 会变孤儿残留。正确顺序是让 adapter 自己收尾：
//  1. CloseSession RPC（best-effort，3s）让 agent 结束会话；
//  2. 关 stdin（EOF）→ TS adapter 的 connection.closed → agent.dispose()，
//     由它自行杀 claude 子进程并 exit(0)（见 adapter index.js 的 shutdown 路径）；
//  3. 等进程退出（≤closeGracefulWait），未退才 Process.Kill 兜底（防 adapter 卡死）。
func (a *ACPAgent) Close(ctx context.Context) error {
	a.closeOnce.Do(func() {
		// best-effort 关会话（agent 不支持 close 能力时会报错，忽略）
		if a.conn != nil && a.started {
			closeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			_, _ = a.conn.CloseSession(closeCtx, acp.CloseSessionRequest{})
			cancel()
		}
		// 关 stdin（EOF）触发 adapter 优雅 dispose（自己清 claude 子进程，零孤儿）
		if a.stdin != nil {
			_ = a.stdin.Close()
		}
		// 等进程自行退出；watchExit 的 cmd.Wait 退出时会 markDone 关 a.done
		if a.cmd != nil && a.cmd.Process != nil {
			select {
			case <-a.done:
				// adapter 已优雅退出（dispose → exit 0），无需强杀
			case <-time.After(closeGracefulWait):
				// 5s 没退：adapter 卡死/不响应 EOF，强杀兜底（可能遗留 claude 子进程，
				// 由空闲回收 + 关停 CloseAll 的系统性防护兜住）
				_ = a.cmd.Process.Kill()
			}
		}
		// 取消所有挂起的权限请求（让 RequestPermission 不再死等）
		a.permMu.Lock()
		for id, ch := range a.pendingPerms {
			select {
			case ch <- PermissionResponse{Selected: false}:
			default:
			}
			delete(a.pendingPerms, id)
		}
		a.permMu.Unlock()
		// 释放工具入参记忆表：进程已结束，留着只是内存（一个会话可达上千条）。
		// 置 nil 而非清空 map —— 与构造器对称，且让"已关闭"这件事对后续查询
		// 表现为查不到（toolInput 对 nil map 读是安全的）。
		// **广播唤醒**可能还在 toolInputWait 上等的审批方，否则它们要干等到
		// 300ms 超时（虽然不影响正确性，但会让关闭变慢、且日志里全是超时告警）。
		a.toolInputsMu.Lock()
		a.toolInputs = nil
		if a.toolInputCond != nil {
			a.toolInputCond.Broadcast()
		}
		a.toolInputsMu.Unlock()
		a.markDone()
		// 释放自持 lifeCtx（最后一步）：若上面优雅等待超时且进程仍在，此处经 CommandContext
		// 兜底强杀。cancel 幂等，重复 Close / 启动失败路径重复调用均安全。
		a.lifeCancel()
	})
	return nil
}

// Done 返回进程退出/连接断开的信号 channel（对应 Phase 1 liveProc.done）。
func (a *ACPAgent) Done() <-chan struct{} { return a.done }

// --- acp.Client 实现（agent→client 方向的 JSON-RPC 请求/通知） ---

// SessionUpdate 处理 agent 推送的会话更新（内容增量 / 工具调用 / 计划等）。
// 这是 M1 端到端出文本的核心入口：AgentMessageChunk.Content.Text → OnContentDelta 回调。
func (a *ACPAgent) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	u := params.Update
	sid := string(params.SessionId)

	switch {
	case u.AgentMessageChunk != nil:
		// 回答正文增量
		if t := u.AgentMessageChunk.Content.Text; t != nil && t.Text != "" {
			a.cbMu.RLock()
			fn := a.onDelta
			a.cbMu.RUnlock()
			if fn != nil {
				fn(ContentDelta{SessionID: sid, Text: t.Text, IsThought: false})
			}
		}
	case u.AgentThoughtChunk != nil:
		// 思考过程增量（M2 同链路推送）
		if t := u.AgentThoughtChunk.Content.Text; t != nil && t.Text != "" {
			a.cbMu.RLock()
			fn := a.onDelta
			a.cbMu.RUnlock()
			if fn != nil {
				fn(ContentDelta{SessionID: sid, Text: t.Text, IsThought: true})
			}
		}
	case u.ToolCall != nil:
		// 新工具调用开始（M3 映射 EventToolUse）
		toolCallID := string(u.ToolCall.ToolCallId)
		rawIn := rawAnyToJSON(u.ToolCall.RawInput)
		// 注意类型差异：ToolCall.Title 是 string，而 ToolCallUpdate.Title 是
		// *string（SDK 两处不一致），故取值写法不同，别照抄。
		title := u.ToolCall.Title
		// 记下**工具名 + 入参**供审批时补齐（见 toolInputs 注释）。
		a.rememberToolInput(toolCallID, title, rawIn)
		// 「收到了什么」必须可观测：审批回填失败时，靠这条日志能立刻区分
		// "tool_call 压根没来" vs "来了但晚了"，以及"没记到工具名"。
		if title != "" || len(rawIn) > 0 {
			a.logger.Debug("acp tool call remembered",
				zap.String("tool_call_id", toolCallID),
				zap.String("title", title),
				zap.Int("bytes", len(rawIn)),
			)
		}
		a.cbMu.RLock()
		fn := a.onToolCall
		a.cbMu.RUnlock()
		if fn != nil {
			fn(ToolCallUpdateInfo{
				SessionID: sid, ToolCallID: toolCallID,
				Title: u.ToolCall.Title, Status: string(u.ToolCall.Status),
				Kind: string(u.ToolCall.Kind), IsNew: true,
				// 工具入参/输出（RawOutput 在开始事件里通常为空，留 nil 即可）。
				RawInput:  rawIn,
				RawOutput: rawAnyToJSON(u.ToolCall.RawOutput),
			})
		}
	case u.ToolCallUpdate != nil:
		// 工具调用状态变更（M3 映射 EventToolResult）
		toolCallID := string(u.ToolCallUpdate.ToolCallId)
		rawIn := rawAnyToJSON(u.ToolCallUpdate.RawInput)
		updTitle := ""
		if u.ToolCallUpdate.Title != nil {
			updTitle = *u.ToolCallUpdate.Title
		}
		// 状态变更里也常带 RawInput/Title（部分 agent 只在 update 里给，如 dsh 的
		// pwsh 调用）；同样记下。空值不覆盖已有记录，见 rememberToolInput。
		a.rememberToolInput(toolCallID, updTitle, rawIn)
		a.cbMu.RLock()
		fn := a.onToolCall
		a.cbMu.RUnlock()
		if fn != nil {
			info := ToolCallUpdateInfo{
				SessionID: sid, ToolCallID: toolCallID,
				IsNew: false,
			}
			if u.ToolCallUpdate.Title != nil {
				info.Title = *u.ToolCallUpdate.Title
			}
			if u.ToolCallUpdate.Status != nil {
				info.Status = string(*u.ToolCallUpdate.Status)
			}
			if u.ToolCallUpdate.Kind != nil {
				info.Kind = string(*u.ToolCallUpdate.Kind)
			}
			// 工具入参/输出（RawOutput 在 completed/failed 时承载结果，→ EventToolResult.Result）。
			info.RawInput = rawIn
			info.RawOutput = rawAnyToJSON(u.ToolCallUpdate.RawOutput)
			fn(info)
		}
	}
	// 其他更新类型（Plan/UserMessageChunk/UsageUpdate 等）M1 暂不处理，留给后续里程碑。
	return nil
}

// toolCallMemo 一次 tool_call 记住的东西：工具名 + 入参。
//
// 两者必须一起存：审批请求里 dsh **两样都不发**，而分级链的第一道门
// （applyToolKindFix）按工具名查表、第二道门（DowngradeReadonlyPermission）
// 按命令原文判定 —— 少任何一样，整条链就退化成"一律 L2 弹卡"。
type toolCallMemo struct {
	// Title 是工具名（dsh 的 tool_call.title = event.data.name，如 "pwsh"）。
	Title string
	// RawInput 是工具入参 JSON（含 shell 命令的 command 字段）。
	RawInput json.RawMessage
}

// rememberToolInput 记下一次工具调用的**工具名与入参**，供后续审批补齐。
//
// **空入参不覆盖已有记录**：同一个 toolCallId 会先来 ToolCall（带完整入参）再来
// 若干 ToolCallUpdate（多数只带状态，RawInput 为空）。若用空值覆盖，就会把先到的
// 命令原文抹掉 —— 那时审批恰好排在 update 之后，就会拿不到命令。
// Title 同理：空 title 不覆盖已记下的名字。
//
// toolCallId 为空时同样丢弃：没有键就无法关联，存了也永远查不到。
//
// 写入后**广播**唤醒可能正在等这个 id 的审批方（见 toolInputWait）。
func (a *ACPAgent) rememberToolInput(toolCallID, title string, raw json.RawMessage) {
	if toolCallID == "" {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" && len(raw) == 0 {
		return // 两样都空：没什么可记的，也别无谓地唤醒等待方
	}
	a.toolInputsMu.Lock()
	defer a.toolInputsMu.Unlock()
	if a.toolInputs == nil { // 兜底：未经构造器直接 new(ACPAgent) 的单测
		a.toolInputs = make(map[string]toolCallMemo)
	}
	memo := a.toolInputs[toolCallID]
	if title != "" {
		memo.Title = title
	}
	if len(raw) > 0 {
		memo.RawInput = raw
	}
	a.toolInputs[toolCallID] = memo
	a.initToolInputCond()
	a.toolInputCond.Broadcast()
}

// toolInput 查一个 toolCallId 记录的工具名与入参。第二个返回值为 false 表示"没记过"——
// 调用方据此维持保守判级，**不要**把它当成"这条命令没有副作用"。
//
// 本函数**只看当下**，不等待；需要等待请用 toolInputWait。
func (a *ACPAgent) toolInput(toolCallID string) (toolCallMemo, bool) {
	if toolCallID == "" {
		return toolCallMemo{}, false
	}
	a.toolInputsMu.Lock()
	defer a.toolInputsMu.Unlock()
	memo, ok := a.toolInputs[toolCallID]
	if !ok || (memo.Title == "" && len(memo.RawInput) == 0) {
		return toolCallMemo{}, false
	}
	return memo, true
}

// toolInputWaitTimeout 审批侧等待 tool_call 入参的上限。
//
// 取值理由：dsh 侧 `drainUpdates()` + acp-go-sdk 的通知屏障已把差距压到
// "同一批数据的两个回调"级别（实测都是同秒内），正常应在毫秒级到达。
// 300ms 足够覆盖调度抖动，又短到用户察觉不到（审批卡本来就要人反应几秒）。
//
// ⚠️ 超时**不是失败**，而是"退回保守判级"：命令取不到 ⇒ 维持 execute/L2 ⇒
// 仍然弹卡让人判断。宁可多问一次，也不猜。
const toolInputWaitTimeout = 300 * time.Millisecond

// toolInputWait 查 toolCallId 记下的工具名与入参，**没到就等一小会儿**（见 toolInputWaitTimeout）。
//
// 为什么必须等：SessionUpdate（写索引）与 RequestPermission（读索引）在 pieqi
// 内部是并发的。协议层的顺序保证（dsh 的 drainUpdates、SDK 的通知屏障）只覆盖
// "线上先发完 update 再发请求"，**不覆盖 pieqi 两个回调的处理完成顺序**。
// 实测（2026-10-07 12:39:37）审批就曾跑赢自己的 tool_call，导致卡片上没有命令原文。
//
// 实现用 sync.Cond 而非轮询/sleep：
//   - 写入方（rememberToolInput）在拿到入参时 Broadcast，等待方立刻醒来 ——
//     命中时**零延迟**，不会像固定 sleep 那样每次都白等；
//   - 超时用 time.AfterFunc 定时 Broadcast（Cond 没有原生超时），
//     醒来后重新判断条件即可。
//
// 返回 false 表示"等到超时也没来" —— 调用方维持保守判级。
func (a *ACPAgent) toolInputWait(toolCallID string) (toolCallMemo, bool) {
	if toolCallID == "" {
		return toolCallMemo{}, false
	}
	a.toolInputsMu.Lock()

	// 快路径：已经在索引里（绝大多数情况）。
	if memo, ok := a.toolInputs[toolCallID]; ok && (memo.Title != "" || len(memo.RawInput) > 0) {
		a.toolInputsMu.Unlock()
		return memo, true
	}

	a.initToolInputCond()
	deadline := time.Now().Add(toolInputWaitTimeout)
	// 定时广播：Cond 无原生超时，用 AfterFunc 在到点时唤醒去重新判断。
	timer := time.AfterFunc(toolInputWaitTimeout, func() {
		a.toolInputsMu.Lock()
		if a.toolInputCond != nil {
			a.toolInputCond.Broadcast()
		}
		a.toolInputsMu.Unlock()
	})
	defer timer.Stop()

	for {
		memo, ok := a.toolInputs[toolCallID]
		if ok && (memo.Title != "" || len(memo.RawInput) > 0) {
			a.toolInputsMu.Unlock()
			return memo, true
		}
		// 已关闭（Close 把 toolInputs 置 nil）：不会再有新入参了，立刻返回。
		// 缺这条判断的话，等待方会一路 Wait 到超时 —— 不影响正确性，
		// 但会让关闭路径白等 300ms，且日志里多出误导性的超时告警。
		if a.toolInputs == nil {
			a.toolInputsMu.Unlock()
			return toolCallMemo{}, false
		}
		if !time.Now().Before(deadline) {
			a.toolInputsMu.Unlock()
			return toolCallMemo{}, false // 超时：等不到了，退回保守判级
		}
		a.toolInputCond.Wait() // 等 Broadcast（新入参到达 或 超时唤醒）
	}
}

// RequestPermission 处理 agent 的权限请求（M3 启用；M1 无回调时自动放行）。
//
// 流程：
//  1. 由 ToolCallId 生成 ReqID（空则 uuid），注册 pending channel。
//  2. 若注册了 OnPermissionRequest 回调：通知（非阻塞）桥接层，桥接层随后调 Approve/Deny 投递响应。
//     若未注册：自动放行首个 allow 选项（M1 端到端文本测试用，等价 example 的 --yolo）。
//  3. 阻塞等响应，转换为 acp.RequestPermissionResponse 返回。
func (a *ACPAgent) RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	reqID := string(params.ToolCall.ToolCallId)
	if reqID == "" {
		reqID = uuid.New().String()
	}
	opts := toPermissionOptions(params.Options)

	a.cbMu.RLock()
	onPerm := a.onPerm
	a.cbMu.RUnlock()

	// 无回调：自动放行（M1 默认行为）
	if onPerm == nil {
		outcome := autoApproveOutcome(opts)
		return acp.RequestPermissionResponse{Outcome: outcome}, nil
	}

	// 有回调：注册 pending，通知，阻塞等 Approve/Deny
	ch := make(chan PermissionResponse, 1)
	a.permMu.Lock()
	a.pendingPerms[reqID] = ch
	a.permMu.Unlock()

	title := ""
	if params.ToolCall.Title != nil {
		title = *params.ToolCall.Title
	}
	// 权限链路此前**没有任何日志**，"批准了却被拒"这类事故无从定位（选项 id 是回选的，
	// 答错 id agent 只会静默当作未批准）。这里把请求与响应都记下来，代价是一行 Debug。
	a.logger.Debug("acp permission requested",
		zap.String("req_id", reqID),
		zap.String("session", string(params.SessionId)),
		zap.String("title", title),
		zap.String("kind", toolKindString(params.ToolCall.Kind)),
		zap.String("options", formatPermissionOptions(opts)),
	)
	// 只读降级（ADR-0008）：qodercli 把所有 shell 调用一律报 execute，于是
	// `sed -n '1,35p' f.go` 与 `rm -rf build/` 在 pieqi 眼里是同一个 ToolKind，
	// 都落 L2、都必须人工点一次。这里把**能证明只读**的命令降为 read(L0)，
	// 命中 L0 免审不再中断。证明不了的一律维持 execute（保守侧）。
	//
	// 审批链要两样东西，**都**可能缺（dsh 两样都不发）：
	//   - tool（工具名）：applyToolKindFix 按它查表（pwsh → execute）；
	//   - rawInput（命令原文）：DowngradeReadonlyPermission 按它判档。
	// 来源有两条，顺序不能反：
	//   1. 请求自带（qoder/claude 会把工具名与入参放进 RequestPermission）；
	//   2. 请求没带时，回查本会话记下的 tool_call 记录（**dsh 走这条**）。
	// 回查是**有界等待**（toolInputWait，上限 300ms）：SessionUpdate 与
	// RequestPermission 在 pieqi 内部并发，实测审批可能跑赢自己的 tool_call
	// （2026-10-07 12:39:37 的 push 就是这样，卡片上没有命令）。
	// 等不到就维持保守判级 —— 仍然弹卡，但不猜。
	rawInput := rawAnyToJSON(params.ToolCall.RawInput)
	backfilledInput, backfilledTitle := false, false
	if len(rawInput) == 0 || strings.TrimSpace(title) == "" {
		if memo, ok := a.toolInputWait(reqID); ok {
			if len(rawInput) == 0 && len(memo.RawInput) > 0 {
				rawInput = memo.RawInput
				backfilledInput = true
			}
			// ★ 工具名同样必须回填：拿不到它就查不到映射表，
			// 分级链第一道门直接失效（实测 2026-10-07 15:03：我自己那两条
			// `git add; git commit` 本该 L1 免审，因 title 为空而落 L2 弹卡）。
			if strings.TrimSpace(title) == "" && memo.Title != "" {
				title = memo.Title
				backfilledTitle = true
			}
		} else {
			// 两样都没等到 ⇒ 卡片上既没工具名也没命令，人无法判断批的是什么。
			// 这条 Warn 是**可观测性**的关键：没有它，"为什么这张卡没信息"
			// 只能靠翻协议流量反推。
			a.logger.Warn("acp permission has no tool/command text (waited, then gave up)",
				zap.String("req_id", reqID),
				zap.String("agent", a.cfg.AgentType),
				zap.String("tool", title),
				zap.String("kind", toolKindString(params.ToolCall.Kind)),
				zap.Duration("waited", toolInputWaitTimeout),
			)
		}
	}
	perm := PermissionRequest{
		ReqID:      reqID,
		SessionID:  string(params.SessionId),
		ToolCallID: string(params.ToolCall.ToolCallId),
		ToolTitle:  title,
		ToolKind:   toolKindString(params.ToolCall.Kind),
		Status:     toolCallStatusString(params.ToolCall.Status),
		RawInput:   rawInput,
		Options:    opts,
	}
	// 第一步：按**工具名**修正 agent 报错的 kind（toolkind_map.go）。
	// dsh 把一切写死成 other(L2)，read/grep/edit 因此全要人工点；
	// 这一步把它们纠正回真实语义。claude 报得准，不动它。
	byName := applyToolKindFix(perm, a.cfg.AgentType)
	// 第二步：按**命令原文**做只读降级（ADR-0008）。
	// 必须在第一步之后 —— 降级只认 execute，dsh 的 other 若没先被纠正，
	// 这一步会直接 return，名字修正就白做了（顺序见 toolkind_map.go 的说明）。
	perm = DowngradeReadonlyPermission(byName)
	if backfilledInput || backfilledTitle {
		// 回填成功的证据：没有这条日志，就无法区分"dsh 补上了工具名/命令"
		// 与"本来就没有、维持保守判级"——审批行为会变得无法解释。
		a.logger.Debug("acp permission backfilled from tool call",
			zap.String("req_id", reqID),
			zap.Bool("title", backfilledTitle),
			zap.Bool("raw_input", backfilledInput),
			zap.String("command", commandFromRawInput(rawInput)),
		)
	}
	onPerm(perm)
	if perm.ToolKind != toolKindString(params.ToolCall.Kind) {
		a.logger.Debug("acp permission kind adjusted",
			zap.String("req_id", reqID),
			zap.String("agent", a.cfg.AgentType),
			zap.String("tool", title),
			zap.String("from", toolKindString(params.ToolCall.Kind)),
			zap.String("to", perm.ToolKind),
			zap.String("command", commandFromRawInput(perm.RawInput)),
		)
	}

	select {
	case resp := <-ch:
		a.logger.Debug("acp permission resolved",
			zap.String("req_id", reqID),
			zap.Bool("selected", resp.Selected),
			zap.String("option", resp.OptionID),
		)
		return acp.RequestPermissionResponse{Outcome: toACPOutcome(resp, opts)}, nil
	case <-ctx.Done():
		a.takePending(reqID)
		a.logger.Debug("acp permission cancelled by ctx", zap.String("req_id", reqID), zap.Error(ctx.Err()))
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, ctx.Err()
	case <-a.done:
		a.takePending(reqID)
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, errors.New("acp: agent exited during permission request")
	}
}

// ReadTextFile 实现 acp.Client：M1 暂不真正读文件，返回不支持。
// 后续里程碑按需实现（agent 经 fs/readTextFile 让客户端代读）。
func (a *ACPAgent) ReadTextFile(ctx context.Context, params acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, ErrNotSupported
}

// WriteTextFile 实现 acp.Client：M1 暂不真正写文件，返回不支持。
func (a *ACPAgent) WriteTextFile(ctx context.Context, params acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, ErrNotSupported
}

// CreateTerminal 实现 acp.Client：M1 不支持 terminal 系列。
func (a *ACPAgent) CreateTerminal(ctx context.Context, params acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, ErrNotSupported
}

// KillTerminal 实现 acp.Client。
func (a *ACPAgent) KillTerminal(ctx context.Context, params acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, ErrNotSupported
}

// TerminalOutput 实现 acp.Client。
func (a *ACPAgent) TerminalOutput(ctx context.Context, params acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, ErrNotSupported
}

// ReleaseTerminal 实现 acp.Client。
func (a *ACPAgent) ReleaseTerminal(ctx context.Context, params acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, ErrNotSupported
}

// WaitForTerminalExit 实现 acp.Client。
func (a *ACPAgent) WaitForTerminalExit(ctx context.Context, params acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, ErrNotSupported
}

// --- 辅助 ---

// toACPMcpServers 把我们的 MCPServer DTO 转为 acp.McpServer（仅 stdio 形态）。
// 非 stdio 传输（http/sse/acp）M1 不需要，留给后续按需扩展。
func toACPMcpServers(in []MCPServer) []acp.McpServer {
	if len(in) == 0 {
		return []acp.McpServer{}
	}
	out := make([]acp.McpServer, 0, len(in))
	for _, s := range in {
		out = append(out, acp.McpServer{Stdio: &acp.McpServerStdio{
			Name:    s.Name,
			Command: s.URL,
		}})
	}
	return out
}

// toPermissionOptions 把 acp.PermissionOption 列表转为本适配器 DTO。
func toPermissionOptions(in []acp.PermissionOption) []PermissionOption {
	out := make([]PermissionOption, 0, len(in))
	for _, o := range in {
		out = append(out, PermissionOption{ID: string(o.OptionId), Name: o.Name, Kind: string(o.Kind)})
	}
	return out
}

// formatPermissionOptions 把选项列表压成 "id(allow_once),id2(reject_once)" 形式，仅供日志。
// 必须能一眼看出 agent 给了哪些 optionId —— 回选错了 id 是"批准被当成拒绝"的唯一原因。
func formatPermissionOptions(opts []PermissionOption) string {
	parts := make([]string, 0, len(opts))
	for _, o := range opts {
		parts = append(parts, o.ID+"("+o.Kind+")")
	}
	return strings.Join(parts, ",")
}

// autoApproveOutcome 无回调时的自动放行：选首个 allow_once，次选 allow_always，
// 再次选首个选项，都没有则 Cancelled（等价 example/claude-code 的 --yolo）。
func autoApproveOutcome(opts []PermissionOption) acp.RequestPermissionOutcome {
	pick := func(kind string) (string, bool) {
		for _, o := range opts {
			if o.Kind == kind {
				return o.ID, true
			}
		}
		return "", false
	}
	if id, ok := pick(PermissionOptionAllowOnce); ok {
		return acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(id))
	}
	if id, ok := pick(PermissionOptionAllowAlways); ok {
		return acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(id))
	}
	if len(opts) > 0 {
		return acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(opts[0].ID))
	}
	return acp.NewRequestPermissionOutcomeCancelled()
}

// toACPOutcome 把审批响应转为 ACP outcome。
// Selected=true → 选中该 OptionID；Selected=false → Cancelled。
func toACPOutcome(resp PermissionResponse, opts []PermissionOption) acp.RequestPermissionOutcome {
	if resp.Selected && resp.OptionID != "" {
		return acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(resp.OptionID))
	}
	return acp.NewRequestPermissionOutcomeCancelled()
}

// toolKindString 安全取 *acp.ToolKind 的字符串值（空指针返回空串）。
// RequestPermission 的 ToolCall 字段是 acp.ToolCallUpdate，其 Kind/Status 为指针。
func toolKindString(k *acp.ToolKind) string {
	if k == nil {
		return ""
	}
	return string(*k)
}

// toolCallStatusString 安全取 *acp.ToolCallStatus 的字符串值（空指针返回空串）。
func toolCallStatusString(s *acp.ToolCallStatus) string {
	if s == nil {
		return ""
	}
	return string(*s)
}

// rawAnyToJSON 把 any 序列化为 json.RawMessage，供 EventToolUse.Input / 审批 RawInput 等使用。
//   - nil 或 typed nil（如装在 any 里的 *T(nil)）返回 nil，避免输出 "null"。
//   - string 视作已序列化的 JSON，直接转，避免 json.Marshal 二次编码加引号。
//   - 其他类型走 json.Marshal，失败返回 nil。
func rawAnyToJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	// 排除 typed nil（指针/map/slice/chan/func/interface 装在 any 里为 nil 的情形）。
	if rv := reflect.ValueOf(v); rv.IsValid() {
		switch rv.Kind() {
		case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
			if rv.IsNil() {
				return nil
			}
		}
	}
	if s, ok := v.(string); ok {
		return json.RawMessage(s)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// newLineCollector 构造一个把每行输出写到 logger（warn 级）的 io.Writer，
// 用作 agent 子进程的 stderr 收集器，避免 stderr 输出丢失又不会刷屏。
func newLineCollector(logger *zap.Logger, label string) io.Writer {
	return &lineCollector{logger: logger, label: label}
}

type lineCollector struct {
	logger *zap.Logger
	label  string
	mu     sync.Mutex
	buf    []byte
}

func (lc *lineCollector) Write(p []byte) (int, error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.buf = append(lc.buf, p...)
	for {
		idx := -1
		for i, b := range lc.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := string(lc.buf[:idx])
		lc.buf = lc.buf[idx+1:]
		if line != "" {
			lc.logger.Warn(lc.label, zap.String("line", line))
		}
	}
	return len(p), nil
}
