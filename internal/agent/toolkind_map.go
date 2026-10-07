package agent

import "strings"

// 各 agent 的 tool_name → ACP ToolKind 修正层（统一转换）。
//
// ## 为什么需要这一层
//
// ACP 的 `ToolKind` 是**语义**字段（read / edit / execute / …），本应由各 agent
// 按真实语义填写，pieqi 的整套分级（riskLevelKinds → L0..L3 → 免审与否）都认它。
// 但实测三家填得并不可靠：
//
//	claude（经 sdk-bridge）  ✅ 准 —— 桥的 TOOL_KINDS 按工具名映射（见下）
//	qoder                    ⚠️ 所有 shell 一律报 execute（所以另有 ADR-0008 只读降级）
//	dsh                      ❌ tool_call 一律写死 kind:"other"，审批请求里干脆连 kind 都不带
//
// 后果是 dsh 的**读文件、搜索、编辑**全落 L2（`other` 属 L2），与 `rm -rf` 同一档、
// 每次都要人点 —— 实测 dsh 1442 次工具调用里 809 次 pwsh + 255 read + 162 edit + 124 grep
// 全部如此。这一层就是把它们纠正回真实语义。
//
// ## 为什么按 agent 分别维护
//
// **tool_name 在各家之间不可移植**（实测）：
//
//	读文件   claude `Read`      / dsh `read`      / qoder `Read <path>`
//	命令     claude `PowerShell`/ dsh `pwsh`      / qoder `powershell.exe`、甚至 `cd`/`ls`/`git`
//	抓网页   claude `WebFetch`  / dsh `web_fetch` / qoder `Fetch`
//
// 大小写不同、命名不同，qoder 还会把**参数拼进名字**（`Read D:\...\acp.go`）、
// 把**命令首词**当名字（`cd` 751 次、`ls` 139 次）。所以任何"一张通用表"都会误判，
// 必须每家一份。
//
// ## 与 claude 的关系：它是语义基准
//
// claude 的表是唯一经过实践检验的（services/claude-sdk-bridge/src/session.js 的
// TOOL_KINDS），因此**表里所有值都必须取自它的值域**（即 kindValues 全集），
// "以 claude 为准"在这里是类型约束而不是风格问题。新增条目时若写了词表外的值，
// toolKindFromName 会忽略它并维持原 kind（保守侧，见该函数注释）。
//
// ## 安全边界（改动前必读）
//
//  1. **白名单式**：只认列出的名字；认不出的一律维持 `other`(L2 强制人工)。
//     绝不能加"除危险外都放行"这类兜底 —— 那等于把 L2 变成通配符。
//  2. **不可配置**：绝不做成 config.yaml / 设置页可编辑。一旦可配置，L2/L3 的
//     "靠字段不存在承载不可配置性"（settings_store.go）就被摧毁了（ADR-0008 明确拒绝）。
//  3. **判据是 agent 自报的名字**：dsh 的 `pwsh` 能跑任意命令、qoder 的 shell 名字
//     就是命令首词（`ls` 与 `rm` 同为"命令名"但语义相反）。所以命令类工具一律归
//     `execute`，**只能**再靠命令原文（readonly_kind.go 的白名单）决定能否降级 ——
//     绝不按名字把 shell 工具直接判成 read。
//  4. 这一层是**修正**，不是判级：claude 报得准就不动它（见 toolKindFor 的 claude 分支）。

// kindValues 是 claude 表用到的全部 kind 取值 —— 也是本文件的**合法值域**。
//
// 与 internal/core/settings_store.go 的 riskLevelKinds 必须能对上：
// 每个值都要能被 RiskOfKind 认出来，否则等于发了个 pieqi 不认识的 kind（静默落 L2）。
// 有测试（toolkind_map_test.go）守着这条。
var kindValues = map[string]struct{}{
	"read": {}, "search": {}, "fetch": {}, "think": {}, // L0
	"edit": {}, "move": {}, // L1
	"execute": {}, "switch_mode": {}, "other": {}, // L2
	"delete": {}, // L3
}

// claudeToolKinds 是 claude（sdk-bridge）的表 —— **语义基准**。
//
// 与 services/claude-sdk-bridge/src/session.js 的 TOOL_KINDS 一一对应；
// 桥那边是权威实现（它才是真正发 kind 的地方），这里保留一份是为了：
//   - 让"合法值域"有个可引用的出处；
//   - claude 走 print 回退路径时（不经桥）也能兜底。
//
// 改动桥的表时必须同步这里，有测试（TestToolKindMap_ClaudeMatchesBridge）守着。
//
// 注意：claude 的**工具名本身已经带大小写**（Read/Edit/Bash），与本表一致，
// 所以查表时不做大小写归一 —— 归一反而会让 `read` 撞上 `Read` 的语义假设。
var claudeToolKinds = map[string]string{
	"Bash":         "execute",
	"PowerShell":   "execute",
	"Edit":         "edit",
	"Write":        "edit",
	"MultiEdit":    "edit",
	"NotebookEdit": "edit",
	"Delete":       "delete",
	"Move":         "move",
	"Rename":       "move",
	"Read":         "read",
	"Grep":         "search",
	"Glob":         "search",
	"WebFetch":     "fetch",
	"WebSearch":    "fetch",
	"Task":         "think",
	"Agent":        "think",
}

// dshToolKinds 是 dsh 的表。
//
// dsh 的 tool_name 是**纯名字、全小写**（实测 15 个，覆盖 1442 次调用里的绝大多数）。
//
// `pwsh` 归 execute 而不是按命令猜：它是一条任意命令的入口，
// "是否只读"只能由命令原文判定（readonly_kind.go）。
//
// 末组（skill/todo_write/goal/job 类）**刻意留在 other**：它们不是只读
// （job_kill 能杀后台任务、create_goal 会改会话目标），但也不该冒充 edit/execute
// 去蹭免审。认不出就不猜 —— 维持 L2 强制人工。
var dshToolKinds = map[string]string{
	"pwsh":       "execute",
	"read":       "read",
	"grep":       "search",
	"glob":       "search",
	"web_fetch":  "fetch",
	"web_search": "fetch",
	"edit":       "edit",
	"write":      "edit",

	// 明确不猜（写出来是为了让下一个人知道"这些是见过的、有意留在 other"，
	// 而不是漏了）：skill / todo_write / job_output / job_kill /
	// get_goal / create_goal / update_goal → 不查表 → 维持原 kind(other/L2)
}

// qoderToolKinds 是 qoder 的表 —— **只覆盖"名字 + 参数"那一类**。
//
// qoder 的 tool_name 有两种形态（实测）：
//  1. `Read <path>` / `Edit <path>` / `Search <query>` —— 名字后拼了参数，
//     所以匹配时必须**取首词**（见 toolKindFor 的 headWord）；
//  2. shell 调用直接把**命令首词**当名字（`cd` 751 次、`ls` 139、`git` 55、`sed` 41…）。
//
// 第 2 类**绝不进这张表**：`ls` 与 `rm` 同为"命令名"但语义相反，
// 按名字放行等于把任意命令洗成只读。它们靠"不查表 → 维持 execute"落 L2，
// 再由 readonly_kind.go 按命令原文降级 —— 这正是 ADR-0008 的路径。
//
// 本表的值与 claude 表同名同义（首词就是 claude 的工具名），这是"以 claude 为准"
// 的直接体现。
var qoderToolKinds = map[string]string{
	"Read":   "read",
	"Grep":   "search",
	"Glob":   "search",
	"Search": "search",
	"Fetch":  "fetch",
	"Edit":   "edit",
	"Write":  "edit",
	// TaskStop / Skill / Agent / Ask / AskUserQuestion / EnterPlanMode / Exit
	// 等：不查表，维持原 kind（保守）。
}

// headWord 取 tool_name 的首个空白分词。
//
// 只对 qoder 用：它的 tool_name 常是 `Read D:\code\...\acp.go` 这种"名字 + 参数"，
// 整串匹配必然落空。取首词后 `Read` 就能命中表。
//
// ⚠️ 对 shell 类取首词会得到命令名（`cd`/`ls`/`git`），那**正是我们不希望**拿去查表的
// 东西 —— 所以 qoder 表里刻意没有这些条目，取完首词查不到，维持保守判级。
func headWord(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if i := strings.IndexAny(name, " \t\r\n"); i >= 0 {
		return name[:i]
	}
	return name
}

// toolKindFor 由 agent 类型与 tool_name 求修正后的 ACP ToolKind。
//
// 返回值语义：
//   - 非空：这是**修正后**的 kind（调用方应改用它，而不是 agent 原本报的）；
//   - 空串：**不做修正**，调用方维持 agent 原本报的 kind（含"它没报"的情况）。
//
// 返回空串的三种情况，都属"不猜"：
//  1. claude —— 它的 kind 本来就准（桥在填），再套一层只会引入漂移；
//  2. agentType 未知 / tool_name 为空；
//  3. 名字不在该 agent 的表里。
//
// **不做大小写归一**：claude 的 `Read` 与 dsh 的 `read` 分属两张表，
// 归一会让两者互相命中，也会让 dsh 表里的 `read` 意外匹配 claude 的 `Read`。
func toolKindFor(agentType, toolName string) string {
	if toolName == "" {
		return ""
	}
	switch normalizeAgentType(agentType) {
	case "dsh":
		// dsh 纯名字，整串匹配（不取首词：它的名字里没有参数）
		if k, ok := dshToolKinds[toolName]; ok {
			return k
		}
	case "qoder":
		// 两种形态：整串（`Read`）与"名字 + 参数"（`Read <path>`）。
		// 先整串、再首词 —— 顺序无关紧要（首词命中的集合是整串的子集），
		// 但整串优先能让将来出现"整串有专门条目"时生效。
		if k, ok := qoderToolKinds[toolName]; ok {
			return k
		}
		if k, ok := qoderToolKinds[headWord(toolName)]; ok {
			return k
		}
	case "claude", "claude-code":
		// 桥已按 TOOL_KINDS 填好 kind；这里仅在其**没给**时兜底
		// （print 回退路径不发 kind，靠这张表补）。
		if k, ok := claudeToolKinds[toolName]; ok {
			return k
		}
	}
	return ""
}

// normalizeAgentType 归一 agent 类型，容忍配置里的常见写法。
//
// 与 agent.defaultACPAgentType / ACPProviderConfigFromAgents 用的标识保持一致；
// 大小写与连字符差异（dsh / DSH、claude / claude-code）都归一到同一支。
func normalizeAgentType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "claude-code", "claudecode", "claude_code":
		return "claude"
	case "qodercli":
		return "qoder"
	}
	return t
}

// applyToolKindFix 把修正结果落到一条权限请求上（命中才改，否则原样返回）。
//
// 与 DowngradeReadonlyPermission 的分工：
//   - 本函数先跑，按**工具名**把 agent 报错的 kind 纠正回真实语义（dsh 的 other → read 等）；
//   - 后者再跑，按**命令原文**把"能证明只读"的 execute 降为 read（ADR-0008）。
//
// 两步判据互相独立：前者认名字、后者认命令。顺序不能反 —— 若先降级，
// dsh 的 other 根本不等于 execute，降级那步直接 return，名字修正就永远没机会生效。
func applyToolKindFix(req PermissionRequest, agentType string) PermissionRequest {
	fixed := toolKindFor(agentType, req.ToolTitle)
	if fixed == "" {
		return req
	}
	// 已在正确档位就不动：留痕的日志才只报"真的改了"的那些。
	if req.ToolKind == fixed {
		return req
	}
	req.ToolKind = fixed
	return req
}
