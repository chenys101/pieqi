package agent

import (
	"encoding/json"
	"testing"
)

// ADR-0015 的回归矩阵：「测试/构建类命令」「PowerShell 只读 cmdlet」
// 「解释器内联代码」「原文扫描」「非 shell 工具按参数分档」。
//
// 与 readonly_kind_test.go 的分工相同：**不该降级的用例必须比该降级的多**，
// 因为降级 = 从"人工点一次"变成"静默放行"，误放的代价用户看不见。

// TestClassifyShellCommand_RunnerAndPowerShell 2026-10-20~21 真实命令的回归集。
//
// 每条都是当天实际弹过卡的命令（或它的等价形式）。判据：纯测试/构建/只读
// 探查 → read/edit（免审）；带写标志、装依赖、改基线的 → execute（弹卡）。
func TestClassifyShellCommand_RunnerAndPowerShell(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		// ---------- 应当免审：测试 / 类型检查 ----------
		{`npm run test 2>&1 | Select-Object -Last 80`, "read"},
		{`npm run typecheck 2>&1 | Select-Object -Last 12`, "read"},
		{`npx vitest run src/features/session/components/sessionPerf.spec.ts 2>&1 | Select-String -Pattern "Tests|×|→" | Select-Object -First 16`, "read"},
		{`npx vitest run src/features/session/components/sessionPerf.spec.ts -t "3 轮" 2>&1 | Select-Object -Skip 4 -First 14`, "read"},
		{`npx playwright test --project=visual 2>&1 | Select-Object -Last 18`, "read"},
		{`npx playwright test --projects=health 2>&1 | Select-Object -Last 15`, "read"},
		{`cd D:\code\go\pieqi\web; npm run test 2>&1 | Select-Object -Last 20`, "read"},

		// ---------- 应当免审：构建（写可重生成的产物 ⇒ L1）----------
		{`npm run build 2>&1 | Select-Object -Last 12`, "edit"},
		{`cd D:\code\go\pieqi\web; npm run build 2>&1 | Select-Object -First 12`, "edit"},

		// ---------- 应当免审：PowerShell 只读探查 ----------
		{`Get-ChildItem "C:\Users\13631\.dsh\sessions" -Directory | Select-Object Name`, "read"},
		{`$p = "x"; Get-Content $p -Raw | ConvertFrom-Json | Select-Object -ExpandProperty rows`, "execute"}, // 赋值+管道：$p 是变量但不是 VAR=val，保守
		// PowerShell 的输出整形（Out-String / Out-Null）是管道尾，不算动作。
		{`git -C D:\code\go\pieqi status --short | Select-Object -First 5`, "read"},
		{`git log --oneline origin/main..HEAD | Select-String "fix"`, "read"},
		{`git -C D:\code\go\pieqi fetch origin 2>&1 | Out-Null`, "execute"}, // Out-Null 不在白名单：保守
		{`git -C D:\code\go\pieqi fetch origin`, "read"},
		{`Get-ChildItem build -Filter "tmp-*" | Select-Object Name`, "read"},
		{`Select-String -Path "C:\Users\x\logs\pieqi.log" -Pattern "acp permission" | Select-Object -First 5`, "read"},
		{`netstat -ano -p TCP | Select-String ":3000 .*LISTENING"`, "read"},
		{`Test-Path internal\core\zz_debug_test.go`, "read"},

		// ---------- 应当免审：解释器里只有读取代码 ----------
		{`node -e "const fs=require('fs');const s=fs.readFileSync('x.vue','utf8');console.log(s.length)"`, "read"},

		// ---------- 必须弹卡：装依赖 / 外部命令 / 不认识 ----------
		{`npm install`, "execute"},
		{`npm i -D typescript`, "execute"},
		{`npm run test --watch`, "execute"},
		{`npm run deploy`, "execute"},
		{`npx some-pkg@latest`, "execute"},
		{`npx playwright install chromium`, "execute"},
		{`npx playwright test --update-snapshots`, "execute"},
		{`npx vitest run -u`, "execute"},
		{`npm run lint:fix`, "execute"},
		{`go build ./...`, "execute"},
		{`go test ./internal/...`, "execute"},
		{`curl -s http://127.0.0.1:3000/api/tasks`, "execute"},

		// ---------- 必须弹卡：写形态 ----------
		{`Remove-Item internal\core\zz_debug_test.go -Force; Test-Path internal\core\zz_debug_test.go`, "execute"},
		{`npm run test 2>&1 | Out-File out.txt`, "execute"},
		{`Get-Content x.log | Tee-Object y.log`, "execute"},
		{`Set-Content -Path a.txt -Value foo`, "execute"},
		// 内联代码里的写/执行动作必须被识别（引号剥离之前先看原文）
		{`node -e "require('fs').writeFileSync('a.txt','x')"`, "execute"},
		{`node -e "require('child_process').execSync('dir')"`, "execute"},
		{`node script.js`, "execute"},

		// ---------- 必须弹卡：原文里带危险痕迹（即使藏在引号里）----------
		{`echo 'Remove-Item -Recurse -Force D:\x'`, "execute"},
		// 变量赋值（`$c = …` 而非 VAR=val）+ 含引号内容的 Replace：保守落 execute。
		// 这类形态靠"看起来只读"是判不出来的，维持人工审是对的选择。
		{`$c = $c.Replace("a", "先读文件"); echo $c`, "execute"},
	}
	for _, c := range cases {
		// 「不可分档」（ok=false）在权限链上等价于"维持原 kind = execute/L2 弹卡"，
		// 所以它对本用例的语义就是 execute —— 把这一点写进判据里，
		// 而不是要求每条危险命令都必须给出一个显式档位。
		got, ok := ClassifyShellCommand(c.cmd)
		if !ok {
			got = "execute"
		}
		if got != c.want {
			t.Errorf("%q → %q，want %q", c.cmd, got, c.want)
		}
	}
}

// TestDowngradeReadonlyPermission_UnsafeTextWins 原文扫描优先于其它判据：
//
// 哪怕分类器认为每一段都"干净"，只要原文里有 `Remove-Item` / `Out-File` /
// 进程控制 / 提权痕迹，就整条不降级。
func TestDowngradeReadonlyPermission_UnsafeTextWins(t *testing.T) {
	mk := func(cmd string) PermissionRequest {
		b, _ := json.Marshal(map[string]string{"command": cmd})
		return PermissionRequest{ToolKind: "execute", RawInput: b}
	}
	// 这些命令的分段判据看起来"无害"（打印一段文本），但原文含危险动词。
	for _, cmd := range []string{
		`echo "Remove-Item -Recurse -Force D:\build"`,
		`echo "Stop-Process -Id 1234 -Force"`,
		`echo "icacls D:\code\go\pieqi /setintegritylevel Medium"`,
		`echo "Out-File -FilePath a.txt"`,
		`echo "Set-ExecutionPolicy Bypass"`,
		`echo hello > out.txt`,
		`echo x >> log.txt`,
		`echo $(whoami)`,
	} {
		if got := DowngradeReadonlyPermission(mk(cmd)); got.ToolKind != "execute" {
			t.Errorf("%q → %q，want execute（原文含危险痕迹必须人工审）", cmd, got.ToolKind)
		}
	}
}

// TestDowngradeReadonlyPermission_DeleteStaysManual 删除类**永远**人工审。
//
// 用户 2026-10-21 明确要求："E 类删除需要审核"。这条测试是那句话的机器表达：
// 无论删除以什么形态出现（Remove-Item / del / rm -rf / 递归删除），
// 都必须维持 execute(L2) 而不是 read/edit —— 更要严防它拿到 L3→免审的错路。
func TestDowngradeReadonlyPermission_DeleteStaysManual(t *testing.T) {
	for _, cmd := range []string{
		`Remove-Item build\commit-msg.txt -Force`,
		`Remove-Item "D:\code\go\pieqi\build\probe-home" -Recurse -Force`,
		`rm -rf build/`,
		`rm internal/core/zz_debug_test.go`,
		`del /q *.tmp`,
		`rd /s /q build`,
	} {
		b, _ := json.Marshal(map[string]string{"command": cmd})
		got := DowngradeReadonlyPermission(PermissionRequest{ToolKind: "execute", RawInput: b})
		if got.ToolKind != "execute" {
			t.Errorf("%q → %q，want execute（删除须人工审核）", cmd, got.ToolKind)
		}
	}
	// 分级表本身不动：`delete` 仍是 L3。真正的"不放行"来自
	// Settings.AutoApproveTools 只由 L0/L1 推导（见 core 包的同名测试）。
	if k, ok := kindValues["delete"]; !ok || k != struct{}{} {
		t.Fatalf("kindValues 里必须有 delete（它是对外契约的一部分）")
	}
}

// TestNonCommandToolKind 非 shell 工具按**参数**分档（ADR-0015）。
func TestNonCommandToolKind(t *testing.T) {
	ok := []struct{ name, raw string }{
		{"lsp", `{"query":"NewACPClient","file":"internal/agent/acp.go"}`},
		{"lsp grep", `{"pattern":"func New","path":"internal"}`},
		{"job_output", `{"job_id":"abc","tail":50}`},
		{"todo_write", `{"todos":[{"content":"x","status":"in_progress"}]}`},
		{"gopls__definition", `{"file":"a.go","line":3,"character":5}`},
	}
	for _, tc := range ok {
		if k, got := NonCommandToolKind(tc.name, []byte(tc.raw)); !got || k != "read" {
			t.Errorf("%s %s → (%q,%v)，want (read,true)", tc.name, tc.raw, k, got)
		}
	}

	bad := []struct{ name, raw string }{
		{"job_kill", `{"job_id":"abc"}`},                 // 会终止进程：不在表里
		{"lsp_rename", `{"file":"a.go","line":1}`},       // 工具名带写动词
		{"lsp", `{"file":"a.go","new_name":"B"}`},        // 未知参数键
		{"lsp", `{"command":"apply_edit"}`},              // 参数值带写动词
		{"unknown_tool", `{"query":"x"}`},                // 不在表里
		{"job_output", `{"job_id":"a","action":"delete"}`}, // 参数值带写动词
	}
	for _, tc := range bad {
		if k, got := NonCommandToolKind(tc.name, []byte(tc.raw)); got {
			t.Errorf("%s %s → (%q,true)，want 拒绝", tc.name, tc.raw, k)
		}
	}
	// 空参数：没有写意图，按设计允许。
	if k, got := NonCommandToolKind("todo_write", []byte(`{}`)); !got || k != "read" {
		t.Errorf("todo_write {} → (%q,%v)，want (read,true)", k, got)
	}
}

// TestApplyToolKindFix_NonCommandTool dsh 的非 shell 工具经名字表不中时，
// 由参数分档补上；别的 agent 不享受这条（避免把同名不同参数语义的工具洗成只读）。
func TestApplyToolKindFix_NonCommandTool(t *testing.T) {
	req := PermissionRequest{
		ToolKind: "other",
		ToolTitle: "lsp",
		RawInput: []byte(`{"query":"NewACPClient","file":"internal/agent/acp.go"}`),
	}
	if got := applyToolKindFix(req, "dsh"); got.ToolKind != "read" {
		t.Errorf("dsh lsp 查询 → %q，want read", got.ToolKind)
	}
	// 非 dsh：维持原 kind。
	if got := applyToolKindFix(req, "qoder"); got.ToolKind != "other" {
		t.Errorf("qoder lsp 查询 → %q，want other（不猜）", got.ToolKind)
	}
	// 写意图：dsh 也不降。
	req.RawInput = []byte(`{"file":"a.go","new_name":"B"}`)
	if got := applyToolKindFix(req, "dsh"); got.ToolKind != "other" {
		t.Errorf("dsh lsp rename → %q，want other", got.ToolKind)
	}
}

// TestGitKindFor_FetchIsRead 钉住 ADR-0015 对 fetch 的改动（双向）。
func TestGitKindFor_FetchIsRead(t *testing.T) {
	for _, cmd := range []string{"git fetch", "git fetch origin", "git -C D:\\x fetch --prune"} {
		got, ok := gitKindFor(cmd)
		if !ok || got != "read" {
			t.Errorf("%q → (%q,%v)，want (read,true)", cmd, got, ok)
		}
	}
	// push / pull 不受影响。
	for _, cmd := range []string{"git push", "git pull", "git push --force"} {
		if got, _ := gitKindFor(cmd); got == "read" || got == "edit" {
			t.Errorf("%q → %q，want execute/delete（影响远端仍需人工）", cmd, got)
		}
	}
}
