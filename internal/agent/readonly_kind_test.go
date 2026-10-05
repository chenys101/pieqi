package agent

import (
	"encoding/json"
	"testing"
)

// TestDowngradeReadonlyToolKind 只读降级的核心矩阵（ADR-0008）。
//
// 这个测试的重点**不是**"只读命令能降级"（那只是省了几次点击），
// 而是"写操作一条都不能降级"—— 降级 = 从 L2 人工审批变成 L0 静默放行，
// 误判的代价是用户再也看不到那次操作。所以写形态的用例必须比只读的多。
func TestDowngradeReadonlyToolKind(t *testing.T) {
	cases := []struct {
		name string
		kind string
		cmd  string
		want string
	}{
		// ---- 应当降级：单条只读命令 ----
		{"sed 打印范围", "execute", "sed -n '1,35p' internal/api/tasks.go", "read"},
		{"ls 长列表", "execute", "ls -la /c/Users/x/.dsh/", "read"},
		{"cat 文件", "execute", "cat README.md", "read"},
		{"grep 搜索", "execute", `grep -n "DEEPSEEK_API_KEY" config.yaml`, "read"},
		{"head 管道外", "execute", "head -40 web/src/main.ts", "read"},
		{"wc 计数", "execute", "wc -l internal/core/settings_store.go", "read"},
		{"pwd", "execute", "pwd", "read"},
		{"which", "execute", "which qodercli", "read"},
		{"纯 cd", "execute", "cd /d/code/go/pieqi", "read"},
		{"带路径前缀的命令名", "execute", "/usr/bin/sed -n 1p f.go", "read"},
		{"wrapper sudo 后仍只读", "execute", "sudo cat /etc/hosts", "read"},
		{"VAR=val 前缀后可判定", "execute", "FOO=bar grep x f.go", "read"},
		{"find 只查不删", "execute", "find /c/Users/x/.dsh -maxdepth 3 -type f", "read"},
		{"diff 比较", "execute", "diff a.txt b.txt", "read"},

		// ---- 应当降级：全段只读的复合命令（真实数据几乎全是这种）----
		{"分号全只读", "execute", "cd /d/code/go/pieqi; sed -n '58,110p' config.yaml", "read"},
		{"分号 + echo 分隔", "execute", `ls -la /c/Users/x/.dsh/; echo "=== find ==="; find /c/Users/x/.dsh -maxdepth 3 -type f`, "read"},
		{"管道到 head", "execute", "ls -la | head -30", "read"},
		{"管道到 grep", "execute", `ls -la | grep -i node`, "read"},
		{"全只读 &&", "execute", "cd repo && ls", "read"},
		{"丢弃 stderr", "execute", "ls -la /c/Users/x/.dsh/ 2>/dev/null", "read"},
		{"丢弃 stderr 后接分号", "execute", "ls -la x 2>/dev/null; echo done", "read"},
		{"2>&1 合并", "execute", "ls -la x 2>&1", "read"},
		{">/dev/null 丢弃", "execute", "find /tmp -name a >/dev/null", "read"},
		{"纯 cd 后接只读", "execute", "cd /d/code/go/pieqi; sed -n 1,35p internal/api/tasks.go", "read"},
		{"空段忽略", "execute", "ls ; ; cat f.go", "read"},

		// ---- 不应降级：只读命令的**写形态**（本启发式最主要的误判风险）----
		{"sed -i 就地改写", "execute", "sed -i 's/a/b/' f.go", "execute"},
		{"sed --in-place", "execute", "sed --in-place 's/a/b/' f.go", "execute"},
		{"find -delete", "execute", "find /tmp -name '*.log' -delete", "execute"},
		{"find -exec rm", "execute", "find . -name '*.tmp' -exec rm {} \\;", "execute"},
		{"sort -o 写回", "execute", "sort -o out.txt in.txt", "execute"},
		{"cut -o 写回", "execute", "cut -o out.txt -f1 in.txt", "execute"},
		// 复合里只要**一段**是写操作就整条拒绝 —— 这是安全核心。
		{"cd 后接 rm", "execute", "cd x; rm -rf y", "execute"},
		{"只读后接 sed -i", "execute", "ls; sed -i s/a/b/ f.go", "execute"},
		{"只读管道接 tee", "execute", "ls -la | tee out.txt", "execute"},

		// ---- 不应降级：写向重定向 / 命令替换 ----
		{"输出重定向", "execute", "echo hi > out.txt", "execute"},
		{"追加重定向", "execute", "cat a.txt >> b.txt", "execute"},
		{"重定向到真实文件", "execute", "ls -la > listing.txt", "execute"},
		{"命令替换", "execute", "echo $(cat secret.txt)", "execute"},
		{"反引号替换", "execute", "echo `whoami`", "execute"},
		{"换行多命令", "execute", "ls\nrm -rf build", "execute"},

		// ---- 不应降级：白名单之外的命令（可能有构建/网络副作用）----
		{"npm", "execute", "npm run typecheck", "execute"},
		{"go test", "execute", "go test ./internal/...", "execute"},
		{"curl", "execute", "curl -s http://127.0.0.1:3000/ready", "execute"},
		{"node", "execute", "node -e 'console.log(1)'", "execute"},
		{"cp 有写副作用", "execute", "cp a.txt b.txt", "execute"},
		{"mkdir", "execute", "mkdir -p /tmp/x", "execute"},
		{"rm", "execute", "rm -rf build/", "execute"},

		// ---- 其它 kind 一律不动 ----
		{"edit 不受影响", "edit", "sed -n 1p f.go", "edit"},
		{"read 不受影响", "read", "ls -la", "read"},
		{"空 kind 不受影响", "", "ls -la", ""},

		// ---- 退化输入 ----
		{"空命令", "execute", "", "execute"},
		{"只有空白", "execute", "   ", "execute"},
		{"只有赋值无动词", "execute", "FOO=bar", "execute"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DowngradeReadonlyToolKind(c.kind, c.cmd); got != c.want {
				t.Errorf("DowngradeReadonlyToolKind(%q, %q) = %q, want %q", c.kind, c.cmd, got, c.want)
			}
		})
	}
}

// TestDowngradeReadonly_TaskSessionRegression 用真实会话里出现过的命令做回归。
//
// 取自 6164e2a2（排查 dsh ACP 问题，453 次工具调用 → 106 次人工审批，其中 100 次
// 是 decision）。这些命令当时**全部弹卡**。它们是典型的 qoder 探查命令：
// 几乎全是复合的（100 条里 81 条含 `;`、78 条含 `|`、53 条含 `2>/dev/null`），
// 但每段都是只读 —— 所以应当整条放行。
//
// 这正是"见复合语法就放弃"版本命中率为 0 的原因：只认单条命令，
// 在这种数据上等于这个功能完全不起作用。
func TestDowngradeReadonly_TaskSessionRegression(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{`sed -n '1,35p' internal/api/tasks.go`, "read"},
		{`ls -la /c/Users/13631/.dsh/`, "read"},
		{`where dsh`, "read"},
		{`date`, "read"},
		// 真实命令原文（节选自该会话的 tool_use.tool_name）——全段只读，应放行。
		{`ls -la /c/Users/13631/.dsh/ 2>/dev/null; echo "=== find files ==="; find /c/Users/13631/.dsh -maxdepth 3 -type f 2>/dev/null | head -60`, "read"},
		{`cd /d/code/go/pieqi; sed -n '58,110p' config.yaml`, "read"},
		{`cd /d/code/go/pieqi; sed -n '1,35p' internal/api/tasks.go`, "read"},
		{`ls -la ~/.pieqi/ 2>/dev/null | head -20; echo "=== logs ==="; ls -la ~/.pieqi/logs/ 2>/dev/null | tail -10`, "read"},
		{`date; echo "=== mtimes ==="; ls -la --time-style=full-iso /c/Users/13631/.dsh/profiles/acp/`, "read"},
		// 含非白名单命令（node/npm/curl）→ 维持 L2：它们可能有构建或网络副作用。
		{`cd /d/code/go/pieqi; npm view @deepseek-ai/dsh versions --json 2>&1 | tail -5`, "execute"},
		{`curl -s -m 15 -X DELETE http://127.0.0.1:3000/api/tasks/x | head -c 100`, "execute"},
		// 含写操作 → 维持 L2。
		{`cp /c/Users/x/cordis.patch.yml /c/Users/x/cordis.patch.yml.bak-$(date +%H%M%S) && ls /c/Users/x/`, "execute"},
		{`rm -f /c/Users/13631/AppData/Local/Temp/x/task.json; rmdir /c/Users/x 2>/dev/null; echo cleaned`, "execute"},
	}
	for _, c := range cases {
		if got := DowngradeReadonlyToolKind("execute", c.cmd); got != c.want {
			t.Errorf("cmd %q → %q, want %q", c.cmd, got, c.want)
		}
	}
}

// TestDowngradeReadonlyPermission 从 RawInput 取命令并降级。
func TestDowngradeReadonlyPermission(t *testing.T) {
	mk := func(kind, raw string) PermissionRequest {
		return PermissionRequest{ReqID: "r", ToolKind: kind, RawInput: json.RawMessage(raw)}
	}

	// execute + 只读命令 → read
	got := DowngradeReadonlyPermission(mk("execute", `{"command":"ls -la"}`))
	if got.ToolKind != "read" {
		t.Errorf("ToolKind = %q, want read", got.ToolKind)
	}

	// execute + 写命令 → 原样
	got = DowngradeReadonlyPermission(mk("execute", `{"command":"rm -rf build/"}`))
	if got.ToolKind != "execute" {
		t.Errorf("ToolKind = %q, want execute", got.ToolKind)
	}

	// 非 execute 一律不动（即使 rawInput 里是只读命令）
	got = DowngradeReadonlyPermission(mk("edit", `{"command":"ls -la"}`))
	if got.ToolKind != "edit" {
		t.Errorf("ToolKind = %q, want edit", got.ToolKind)
	}

	// RawInput 不可解析 / 无 command 字段 → 不降级（不能凭缺失就当作无害）
	for _, raw := range []string{``, `not json`, `{"cmd":"ls"}`, `{"command":123}`} {
		got = DowngradeReadonlyPermission(mk("execute", raw))
		if got.ToolKind != "execute" {
			t.Errorf("rawInput %q: ToolKind = %q, want execute", raw, got.ToolKind)
		}
	}

	// 原始请求不被就地修改（返回副本，避免调用方共享的 DTO 被悄悄改写）
	orig := mk("execute", `{"command":"ls -la"}`)
	_ = DowngradeReadonlyPermission(orig)
	if orig.ToolKind != "execute" {
		t.Errorf("original mutated to %q, want execute (must return a copy)", orig.ToolKind)
	}
}
