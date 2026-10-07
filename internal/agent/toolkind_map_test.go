package agent

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// tool_name → ToolKind 修正层的回归测试。
//
// 这一层守的是：**dsh/qoder 把 kind 报错时，pieqi 能不能把它们纠正回真实语义**。
// 报错的具体形态（实测）：
//   - dsh   ：tool_call 一律 kind:"other"，审批请求里连 kind 都不带 ⇒ read/grep/edit 全落 L2
//   - qoder ：所有 shell 一律 execute（另有 ADR-0008 按命令降级）
//   - claude：桥填得准，**不该被动**

// TestToolKindMap_ValuesAreKnownKinds 表里所有值都必须是 pieqi 认识的 kind。
//
// 写一个 riskLevelKinds 不认识的值 = 发了个 pieqi 读不懂的 kind
// （RiskOfKind 兜底 L2），表现为"免审静默失效、卡片全显示执行命令"。
// 这正是 AGENTS.md 警告过的那类改动。
func TestToolKindMap_ValuesAreKnownKinds(t *testing.T) {
	tables := map[string]map[string]string{
		"claude": claudeToolKinds,
		"dsh":    dshToolKinds,
		"qoder":  qoderToolKinds,
	}
	for agentName, tbl := range tables {
		for name, kind := range tbl {
			if _, ok := kindValues[kind]; !ok {
				t.Errorf("%s 表: %q → %q 不在已知 kind 集合内", agentName, name, kind)
			}
		}
	}
}

// TestToolKindMap_ClaudeMatchesBridge claude 表必须与桥的 TOOL_KINDS 一致。
//
// 桥（services/claude-sdk-bridge/src/session.js）才是真正发 kind 的地方；
// Go 侧这份是"值域出处 + print 回退兜底"。两处漂移会让同一条命令在
// sdk-bridge 与 print 两条传输上得到不同审批强度。
//
// 直接读桥的源码做比对（而不是再抄一份期望值）—— 抄一份等于把"同步"这件事
// 交给下一个人记得改，读源码才能真正守住。
func TestToolKindMap_ClaudeMatchesBridge(t *testing.T) {
	const bridgePath = "../../services/claude-sdk-bridge/src/session.js"
	src, err := os.ReadFile(bridgePath)
	if err != nil {
		t.Skipf("读不到桥源码（%v）——非完整检出时跳过", err)
	}

	// 抓 TOOL_KINDS = { Name: "kind", ... } 这一段
	block := regexp.MustCompile(`(?s)const TOOL_KINDS = \{(.*?)\n\};`).FindSubmatch(src)
	if block == nil {
		t.Fatal("在桥源码里找不到 TOOL_KINDS 定义（改名了？请同步本测试）")
	}
	entry := regexp.MustCompile(`(\w+):\s*"([^"]*)"`)
	found := map[string]string{}
	for _, m := range entry.FindAllSubmatch(block[1], -1) {
		found[string(m[1])] = string(m[2])
	}
	if len(found) == 0 {
		t.Fatal("桥的 TOOL_KINDS 解析出 0 条（格式变了？）")
	}

	for name, want := range found {
		if got, ok := claudeToolKinds[name]; !ok {
			t.Errorf("桥有 %q → %q，Go 侧 claude 表缺失", name, want)
		} else if got != want {
			t.Errorf("%q：Go 侧 %q，桥 %q（两处必须一致）", name, got, want)
		}
	}
	for name, got := range claudeToolKinds {
		if _, ok := found[name]; !ok {
			t.Errorf("Go 侧 claude 表多了 %q → %q，桥里没有", name, got)
		}
	}
}

// TestToolKindFor_Dsh 是本次修复的核心：dsh 的工具名要被纠正回真实语义。
func TestToolKindFor_Dsh(t *testing.T) {
	cases := []struct {
		tool string
		want string
	}{
		// 只读类：修复前一律 other(L2) 每次弹卡，修复后 L0 免审
		{"read", "read"},
		{"grep", "search"},
		{"glob", "search"},
		{"web_fetch", "fetch"},
		{"web_search", "fetch"},
		// 写入类：L1 免审（用户已确认接受）
		{"edit", "edit"},
		{"write", "edit"},
		// 命令类：归 execute(L2)，是否只读交给命令原文（readonly_kind.go）
		{"pwsh", "execute"},
	}
	for _, tc := range cases {
		if got := toolKindFor("dsh", tc.tool); got != tc.want {
			t.Errorf("dsh %q → %q，want %q", tc.tool, got, tc.want)
		}
	}
}

// TestToolKindFor_DshUnknownStaysUnmapped 未列出的名字**不猜**（返回空 = 维持原 kind）。
//
// 这些是 dsh 真实存在但刻意不进表的：它们不是只读（job_kill 能杀任务、
// create_goal 改会话目标），也不该冒充 edit/execute 蹭免审。
func TestToolKindFor_DshUnknownStaysUnmapped(t *testing.T) {
	for _, name := range []string{
		"skill", "todo_write", "job_output", "job_kill",
		"get_goal", "create_goal", "update_goal",
		"some_future_tool",
	} {
		if got := toolKindFor("dsh", name); got != "" {
			t.Errorf("dsh %q 不该被映射（got %q）——认不出就要维持 L2 人工", name, got)
		}
	}
}

// TestToolKindFor_QoderHandlesPastedArguments qoder 的 tool_name 会把参数拼进来。
func TestToolKindFor_QoderHandlesPastedArguments(t *testing.T) {
	cases := []struct {
		tool string
		want string
	}{
		{"Read", "read"},
		{`Read D:\code\go\pieqi\internal\agent\acp.go`, "read"},
		{"Edit", "edit"},
		{`Edit D:\code\go\pieqi\web\src\pages\SessionPage.vue`, "edit"},
		{"Search", "search"},
		{"Search slash|Slash|斜杠", "search"},
		{"Write", "edit"},
		{"Fetch", "fetch"},
	}
	for _, tc := range cases {
		if got := toolKindFor("qoder", tc.tool); got != tc.want {
			t.Errorf("qoder %q → %q，want %q", tc.tool, got, tc.want)
		}
	}
}

// TestToolKindFor_QoderShellCommandsStayUnmapped ★ 安全核心。
//
// qoder 把**命令首词**当 tool_name（`cd`/`ls`/`git`/`sed`/`rm`…）。
// 绝不能让它们按名字拿到 read —— `ls` 与 `rm` 同为"命令名"但语义相反，
// 按名字放行等于把任意命令洗成只读。它们必须落回"不映射"，由命令原文判定。
func TestToolKindFor_QoderShellCommandsStayUnmapped(t *testing.T) {
	for _, name := range []string{
		"cd", "ls", "git", "sed", "cat", "rm", "cp", "mv", "mkdir",
		"curl", "npm", "node", "python", "powershell.exe", "cmd",
		`D="/c/Users/13631/AppData/Local/Programs/Cua/cua-driver/bin/cua-driver.exe";`,
	} {
		if got := toolKindFor("qoder", name); got != "" {
			t.Errorf("qoder shell 名 %q 不该被映射（got %q）——按名字判只读是安全漏洞", name, got)
		}
	}
}

// TestToolKindFor_ClaudeUnmappedNamesStayUnmapped claude 不在表里的名字不猜。
func TestToolKindFor_ClaudeUnmappedNamesStayUnmapped(t *testing.T) {
	for _, name := range []string{"AskUserQuestion", "SomeBrandNewTool", ""} {
		if got := toolKindFor("claude", name); got != "" {
			t.Errorf("claude %q 不该被映射（got %q）", name, got)
		}
	}
}

// TestToolKindFor_NoCrossAgentLeak 各家表**互不串味**。
//
// 实测三家命名不同（claude `Read` / dsh `read` / qoder `Read <path>`）。
// 若查表做了大小写归一或跨表查找，dsh 的 `read` 会命中 claude 的 `Read`、
// 反之亦然 —— 现在语义恰好相同所以看不出来，但一旦某家改语义就会静默串味。
func TestToolKindFor_NoCrossAgentLeak(t *testing.T) {
	// claude 的工具名不应在 dsh 上命中（dsh 表里没有大写名）
	if got := toolKindFor("dsh", "Read"); got != "" {
		t.Errorf("claude 风格名 %q 不该在 dsh 表命中（got %q）", "Read", got)
	}
	// dsh 的小写名不应在 claude 上命中
	if got := toolKindFor("claude", "read"); got != "" {
		t.Errorf("dsh 风格名 %q 不该在 claude 表命中（got %q）", "read", got)
	}
	// qoder 独有的名字不该在 dsh 上命中
	if got := toolKindFor("dsh", "Search"); got != "" {
		t.Errorf("qoder 名 %q 不该在 dsh 表命中（got %q）", "Search", got)
	}
}

// TestNormalizeAgentType 配置里的常见写法都要归一到同一支。
func TestNormalizeAgentType(t *testing.T) {
	cases := map[string]string{
		"dsh": "dsh", "DSH": "dsh", " dsh ": "dsh",
		"claude": "claude", "claude-code": "claude", "Claude-Code": "claude",
		"qoder": "qoder", "qodercli": "qoder",
	}
	for in, want := range cases {
		if got := normalizeAgentType(in); got != want {
			t.Errorf("normalizeAgentType(%q)=%q want %q", in, got, want)
		}
	}
}

// TestApplyToolKindFix_OnlyChangesWhenMapped 只在该改的时候改，且不误伤。
func TestApplyToolKindFix_OnlyChangesWhenMapped(t *testing.T) {
	t.Run("dsh other → read（本次修复的靶心）", func(t *testing.T) {
		got := applyToolKindFix(PermissionRequest{ToolTitle: "read", ToolKind: "other"}, "dsh")
		if got.ToolKind != "read" {
			t.Fatalf("ToolKind=%q want read", got.ToolKind)
		}
	})
	t.Run("claude 报得准就不动", func(t *testing.T) {
		got := applyToolKindFix(PermissionRequest{ToolTitle: "Read", ToolKind: "read"}, "claude")
		if got.ToolKind != "read" {
			t.Fatalf("ToolKind=%q want read（不变）", got.ToolKind)
		}
	})
	t.Run("未映射的名字保持原 kind", func(t *testing.T) {
		got := applyToolKindFix(PermissionRequest{ToolTitle: "job_kill", ToolKind: "other"}, "dsh")
		if got.ToolKind != "other" {
			t.Fatalf("ToolKind=%q want other（不猜）", got.ToolKind)
		}
	})
	t.Run("空 tool_name 保持原 kind", func(t *testing.T) {
		got := applyToolKindFix(PermissionRequest{ToolTitle: "", ToolKind: "execute"}, "dsh")
		if got.ToolKind != "execute" {
			t.Fatalf("ToolKind=%q want execute（不变）", got.ToolKind)
		}
	})
}

// TestApplyToolKindFix_DoesNotDowngradeToReadByShellName 端到端安全判据：
// dsh 的 pwsh 只能改成 execute，**绝不能**因为命令里有 `ls` 就被名字判成 read。
func TestApplyToolKindFix_DoesNotDowngradeToReadByShellName(t *testing.T) {
	got := applyToolKindFix(PermissionRequest{ToolTitle: "pwsh", ToolKind: "other"}, "dsh")
	if got.ToolKind != "execute" {
		t.Fatalf("pwsh 应归 execute（再由命令原文决定能否降级），got %q", got.ToolKind)
	}
	if got.ToolKind == "read" {
		t.Fatal("pwsh 绝不能被名字直接判成 read —— 它能跑任意命令")
	}
}

// TestToolKindMap_AllKeysLowercaseForDsh dsh 的名字是全小写（实测），
// 表里若混入大写键就是永远命不中的死条目。
func TestToolKindMap_AllKeysLowercaseForDsh(t *testing.T) {
	for name := range dshToolKinds {
		if name != strings.ToLower(name) {
			t.Errorf("dsh 表键 %q 含大写 —— dsh 实测发的是全小写，该条目永不命中", name)
		}
	}
}
