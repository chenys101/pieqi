package agent

import "testing"

// ClassifyShellCommand 的表驱动测试 —— 它是 DowngradeReadonlyPermission
// 的新判据入口（四档：read/edit/execute/delete），必须与 ADR-0008 的
// 二值降级协作而非互相覆盖。

// TestClassifyShellCommand_TakesWorstSegment 复合命令取**最严**的一段。
//
// 这是与 DowngradeReadonlyToolKind 的关键区别：后者是"全段都只读才降级"（二值），
// 前者要能表达"只读 + 本地可逆 = 本地可逆"这种档位叠加。
func TestClassifyShellCommand_TakesWorstSegment(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{"git status", "read"},
		{"git status; git add -A", "edit"},         // 只读 + 可逆写 → 取较严的 edit
		{"git status && git push", "execute"},      // 混入影响远端 → execute
		{"git log | head -5", "read"},              // 全只读
		{"git add -A; git commit -m x", "edit"},    // 都可逆
		{"git status; git reset --hard", "delete"}, // 含破坏性 → delete
		{"ls -la", "read"},                         // 非 git 只读
		{"ls -la; git status", "read"},             // 混合都只读
		{"ls -la; git add -A", "edit"},
		{"npm test", "execute"}, // 非 git 且非白名单
		{"cat /etc/hosts; git status", "read"},
	}
	for _, tc := range cases {
		got, ok := ClassifyShellCommand(tc.cmd)
		if !ok {
			t.Errorf("%q → 不可分档（ok=false），want %q", tc.cmd, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("%q → %q，want %q", tc.cmd, got, tc.want)
		}
	}
}

// TestClassifyShellCommand_UnsafeRedirectionGivesUp 写向重定向/命令替换直接放弃分档。
//
// 与 ADR-0008 同一取向：`>` 可能写文件、反引号/`$(` 会执行另一条任意命令
// —— 不猜，交调用方维持 L2。
func TestClassifyShellCommand_UnsafeRedirectionGivesUp(t *testing.T) {
	for _, cmd := range []string{
		"git log > out.txt",
		"git status > /tmp/x",
		"echo `rm -rf x`",
		"git log $(rm -rf x)",
		"git status >> log",
	} {
		if got, ok := ClassifyShellCommand(cmd); ok {
			t.Errorf("%q → (%q, true)，含不安全重定向/替换时应返回 false", cmd, got)
		}
	}
}

// TestClassifyShellCommand_EmptyOrBlank 空命令不可分档。
func TestClassifyShellCommand_EmptyOrBlank(t *testing.T) {
	for _, cmd := range []string{"", "   ", "\t", "\n"} {
		if got, ok := ClassifyShellCommand(cmd); ok {
			t.Errorf("%q → (%q, true)，空命令应返回 false", cmd, got)
		}
	}
}

// TestClassifyShellCommand_UnknownGitIsExecute 不认识的 git 动作保守落 execute。
//
// 与 `gitKindFor` 返回 false 的语义配合：在 ClassifyShellCommand 这一层
// "不认识的 git" 必须给一个**明确档位**（execute），因为调用方需要结论。
func TestClassifyShellCommand_UnknownGitIsExecute(t *testing.T) {
	for _, cmd := range []string{
		"git st",            // alias
		"git rebase main",   // 依赖推送状态，不猜
		"git checkout main", // 同名歧义
		"git bisect start",
	} {
		got, ok := ClassifyShellCommand(cmd)
		if !ok {
			t.Errorf("%q → 不可分档，want execute（调用方需要结论）", cmd)
			continue
		}
		if got != "execute" {
			t.Errorf("%q → %q，want execute（不认识的 git 动作保守落 L2）", cmd, got)
		}
	}
}

// TestClassifyShellCommand_WrapperPrefix wrapper 前缀不干扰判定。
func TestClassifyShellCommand_WrapperPrefix(t *testing.T) {
	cases := map[string]string{
		"sudo git status":   "read",
		"env FOO=1 git log": "read",
		"time git status":   "read",
	}
	for cmd, want := range cases {
		got, ok := ClassifyShellCommand(cmd)
		if !ok {
			t.Errorf("%q → 不可分档，want %q", cmd, want)
			continue
		}
		if got != want {
			t.Errorf("%q → %q，want %q", cmd, got, want)
		}
	}
}

// TestClassifyShellCommand_ValuesAreKnownKinds 返回的档位必须是已知 kind。
func TestClassifyShellCommand_ValuesAreKnownKinds(t *testing.T) {
	for _, cmd := range []string{
		"git status", "git add -A", "git push", "git reset --hard",
		"ls -la", "npm test", "git branch -D x", "git stash", "git stash drop",
	} {
		got, ok := ClassifyShellCommand(cmd)
		if !ok {
			continue
		}
		if _, known := kindValues[got]; !known {
			t.Errorf("%q → %q 不在 kindValues 内", cmd, got)
		}
	}
}

// TestDowngradeReadonlyPermission_GitEndToEnd 端到端：一条权限请求带 git 命令时，
// kind 应被改写为对应档位（这才是"dsh 跑 git status 不再弹卡"的判据）。
func TestDowngradeReadonlyPermission_GitEndToEnd(t *testing.T) {
	mk := func(cmd string) PermissionRequest {
		return PermissionRequest{
			ToolKind: "execute",
			RawInput: []byte(`{"command":` + jsonQuote(cmd) + `}`),
		}
	}
	cases := map[string]string{
		"git status --porcelain": "read",
		"git log --oneline -15":  "read",
		"git diff --stat":        "read",
		"git add -A":             "edit",
		"git commit -m x":        "edit",
		"git push origin main":   "execute", // 不变
		"git reset --hard":       "execute", // 不变（delete 不往宽改）
		"git status; git push":   "execute",
	}
	for cmd, want := range cases {
		got := DowngradeReadonlyPermission(mk(cmd))
		if got.ToolKind != want {
			t.Errorf("%q → %q，want %q", cmd, got.ToolKind, want)
		}
	}
}

// jsonQuote 把字符串转成 JSON 字符串字面量（含引号），供构造 RawInput 用。
func jsonQuote(s string) string {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		default:
			b = append(b, []byte(string(r))...)
		}
	}
	b = append(b, '"')
	return string(b)
}
