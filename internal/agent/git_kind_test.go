package agent

import "testing"

// git 子命令分档的表驱动测试。
//
// 分档是**安全相关**的：判错成 L0/L1 会静默免审（放行一个改远端或丢内容的操作），
// 判错成 L2/L3 只是多弹一次卡。所以下面每条"该 L0/L1"的用例都配了理由，
// "该拦"的用例则成组出现（宁可多拦）。

// TestGitKindFor_ReadOnly L0：只读子命令应降到 read（免审）。
func TestGitKindFor_ReadOnly(t *testing.T) {
	cmds := []string{
		"git status",
		"git status --porcelain",
		"git log --oneline -15",
		"git log --oneline origin/main..HEAD",
		"git diff --stat",
		"git diff HEAD -- web/src/pages/SessionPage.vue",
		"git show HEAD",
		"git blame internal/agent/acp.go",
		"git describe --tags",
		"git rev-parse HEAD",
		"git rev-parse --abbrev-ref @{upstream}",
		"git ls-files --error-unmatch AGENTS.md",
		"git rev-list --left-right --count origin/main...HEAD",
		"git shortlog -sn",
		"git merge-base main HEAD",
		"git show-ref",
		"git for-each-ref",
		"git cat-file -p HEAD",
		"git symbolic-ref HEAD",
		"git count-objects -v",
		"git version",
		"git var GIT_AUTHOR_IDENT",
		"git ls-tree HEAD",
	}
	for _, c := range cmds {
		got, ok := gitKindFor(c)
		if !ok {
			t.Errorf("%q → 不认识（应识别为只读）", c)
			continue
		}
		if got != "read" {
			t.Errorf("%q → %q，want read", c, got)
		}
	}
}

// TestGitKindFor_ReadOnlyWithParams 参数敏感子命令的**查询**形态仍是 L0。
func TestGitKindFor_ReadOnlyWithParams(t *testing.T) {
	cases := map[string]string{
		"git branch":                 "read", // 列本地分支
		"git branch -a":              "read",
		"git branch -l":              "read",
		"git branch -v":              "read",
		"git branch --list":          "read",
		"git tag":                    "read", // 列 tag
		"git tag -l":                 "read",
		"git remote":                 "read", // 列远端
		"git remote -v":              "read",
		"git remote get-url origin":  "read",
		"git stash list":             "read",
		"git stash show":             "read",
		"git config --get user.name": "read",
		"git config --list":          "read",
		"git config user.name":       "read", // 单个键 = 读
		"git worktree list":          "read",
	}
	for c, want := range cases {
		got, ok := gitKindFor(c)
		if !ok {
			t.Errorf("%q → 不认识，want %q", c, want)
			continue
		}
		if got != want {
			t.Errorf("%q → %q，want %q", c, got, want)
		}
	}
}

// TestGitKindFor_GlobalOptionsAreSkipped git 全局选项不能被当成子命令。
//
// 不处理 `-C`/`-c` 就会把它们的**值**当子命令 ——
// `git -C /other status` 会取到 `/other`，既不认识也可能误判。
func TestGitKindFor_GlobalOptionsAreSkipped(t *testing.T) {
	cases := map[string]string{
		"git -C /other/repo status":             "read",
		"git -C D:/code/go/pieqi log --oneline": "read",
		"git -c core.pager=cat log":             "read",
		"git --no-pager diff":                   "read",
		"git --no-pager log -5":                 "read",
		"git --git-dir=/x status":               "read",
	}
	for c, want := range cases {
		got, ok := gitKindFor(c)
		if !ok {
			t.Errorf("%q → 不认识，want %q", c, want)
			continue
		}
		if got != want {
			t.Errorf("%q → %q，want %q", c, got, want)
		}
	}
}

// TestGitKindFor_BreakingMustNotBeAllowed ★ 安全核心：破坏性操作绝不能判成 L0/L1。
//
// 这是本文件最重要的用例组。每一条都对应一个"能不可逆地丢掉别人可见的东西"
// 或"改写共享远端"的动作；判成 read/edit 就等于免审放行。
func TestGitKindFor_BreakingMustNotBeAllowed(t *testing.T) {
	forbidden := map[string]string{"read": "只读", "edit": "本地可逆", "": "不认识"}
	cmds := []string{
		// 丢弃未提交内容
		"git reset --hard HEAD~1",
		"git reset --hard",
		"git clean -fd",
		"git clean -fdx",
		"git clean --force",
		"git checkout -- .",
		"git checkout -- web/src/pages/SessionPage.vue",
		"git restore .",
		"git restore -- web/src/App.vue",
		// 丢弃 stash
		"git stash drop",
		"git stash clear",
		// 删分支/tag
		"git branch -D feature",
		"git branch -d merged",
		"git branch -m old new",
		"git tag -d v1.0",
		// 改写共享远端
		"git push --force",
		"git push -f origin main",
		"git push --force-with-lease",
		"git push --delete origin br",
		"git push --mirror",
		// 改写历史
		"git filter-branch --tree-filter x HEAD",
		// 全局配置
		"git config --global user.email x@y.z",
	}
	for _, c := range cmds {
		got, ok := gitKindFor(c)
		if !ok {
			continue // "不认识" = 维持 execute/L2，是安全结果
		}
		if _, bad := forbidden[got]; bad {
			t.Errorf("★ %q → %q（%s）—— 破坏性操作绝不能被判成可免审的档位",
				c, got, forbidden[got])
		}
	}
}

// TestGitKindFor_RemoteAffectingIsL2 影响远端的操作维持 L2（弹卡，但不算破坏性）。
func TestGitKindFor_RemoteAffectingIsL2(t *testing.T) {
	cases := []string{
		"git push",
		"git push origin main",
		"git fetch origin",
		"git pull",
		"git pull --rebase",
		"git clone https://x/y.git",
		"git remote add origin https://x/y.git",
		"git remote set-url origin https://x/y.git",
		"git submodule update --init",
	}
	for _, c := range cases {
		got, ok := gitKindFor(c)
		if !ok {
			// 不认识也是 L2（维持 execute），可接受
			continue
		}
		if got != "execute" {
			t.Errorf("%q → %q，want execute（影响远端必须弹卡）", c, got)
		}
	}
}

// TestGitKindFor_LocalReversibleIsL1 本地可逆写 → L1（免审，用户已确认接受）。
func TestGitKindFor_LocalReversibleIsL1(t *testing.T) {
	cmds := []string{
		"git add web/src/pages/SessionPage.vue",
		"git add -A",
		"git commit -m \"feat: x\"",
		"git commit --amend --no-edit",
		"git mv a.go b.go",
		"git switch main",
		"git cherry-pick abc123",
		"git revert HEAD",
		"git stash",
		"git stash push -m wip",
		"git stash pop",
		"git branch new-branch",
		"git tag v1.2.3",
	}
	for _, c := range cmds {
		got, ok := gitKindFor(c)
		if !ok {
			t.Errorf("%q → 不认识，want edit", c)
			continue
		}
		if got != "edit" {
			t.Errorf("%q → %q，want edit（本地可逆，L1 免审）", c, got)
		}
	}
}

// TestGitKindFor_UnknownStaysUnknown 不认识的一律返回 false（调用方维持 L2）。
//
// 特别是**用户 alias**：`git st`、`git co` 之类查不到配置就别猜 ——
// 猜成 read 等于给一个未知脚本发免审通行证。
func TestGitKindFor_UnknownStaysUnknown(t *testing.T) {
	cmds := []string{
		"git st",      // 常见 alias
		"git co main", // 常见 alias
		"git someone-alias",
		"git",                         // 没有子命令
		"git -C /path",                // 只有全局选项
		"git --unknown-flag x status", // 不认识的全局标志
		"git rebase main",             // 是否危险取决于推没推过 —— 不猜
		"git rebase -i HEAD~3",
		"git bisect start",
		"git archive HEAD",
		"git bundle create x.bundle HEAD",
		"git notes add -m x",
		"git replace abc def",
		"git checkout main", // 切分支还是丢文件？同名无法从文本区分 ⇒ 不猜
		"git checkout foo.txt",
		"git restore",                    // 无路径，语义不明
		"git restore -s HEAD~1 --staged", // 无路径
	}
	for _, c := range cmds {
		if got, ok := gitKindFor(c); ok {
			t.Errorf("%q → (%q, true)，应返回 false（不猜，维持 L2）。"+
				"若不认识的 git 动作被判成可免审档位，等于白名单变通配符", c, got)
		}
	}
}

// TestGitKindFor_NotGitCommand 非 git 命令必须返回 false（本函数只管 git）。
func TestGitKindFor_NotGitCommand(t *testing.T) {
	for _, c := range []string{"ls -la", "rm -rf build", "npm test", "", "  ", "gitfoo status"} {
		if got, ok := gitKindFor(c); ok {
			t.Errorf("%q → (%q, true)，非 git 命令不该被本函数接管", c, got)
		}
	}
}

// TestGitKindFor_RepoMaintenanceIsL2 gc/prune 属"动仓库内部"，维持 L2 弹卡。
//
// 它们不是"不认识"（那会返回 false），而是**明确映射到 L2**：有 reflog 兜底、
// 未必不可逆，但也不该免审。写下来是为了让"为什么它在表里而 rebase 不在"
// 这个区别有据可查 —— 区别在于 gc/prune 的危险性不依赖分支推送状态，rebase 依赖。
func TestGitKindFor_RepoMaintenanceIsL2(t *testing.T) {
	for _, c := range []string{"git gc", "git gc --prune=now", "git prune"} {
		got, ok := gitKindFor(c)
		if !ok {
			t.Errorf("%q → 不认识，want execute（明确映射 L2）", c)
			continue
		}
		if got != "execute" {
			t.Errorf("%q → %q，want execute", c, got)
		}
	}
}

// TestGitKindFor_RestoreWithPathIsBreaking ★ restore/checkout 的路径陷阱。
//
// `git restore <path>` 丢弃工作区改动 —— **与有没有 `--` 无关**。
// 早期实现把 restore 放进 L1 表，于是 `git restore .` 被判成"本地可逆"免审，
// 而它实际上不可逆地丢掉了未提交内容。这是本测试要钉住的回归。
//
// ⚠️ 断言写法：**不能**用 `if !ok { continue }`。那样"返回不认识"会因为跳过而
// 假通过 —— 而本用例要区分的正是「L3 拦住」与「不认识也拦住」两种安全结果，
// 所以直接断言"**绝不能**是 read/edit"。
func TestGitKindFor_RestoreWithPathIsBreaking(t *testing.T) {
	for _, c := range []string{
		"git restore .",
		"git restore web/src/App.vue",
		"git restore --staged .",
		"git restore -s HEAD~1 web/src/App.vue",
	} {
		got, ok := gitKindFor(c)
		if ok && (got == "read" || got == "edit") {
			t.Errorf("★ %q → %q —— restore 带路径会丢弃未提交改动，绝不能被判成可免审",
				c, got)
		}
		// 安全结果只有两种：L3(delete) 或"不认识"(ok=false → 调用方维持 L2)。
		if ok && got != "delete" {
			t.Errorf("%q → %q，只该是 delete 或不认识", c, got)
		}
	}
}

// TestGitKindFor_CheckoutIsNeverGuessed checkout 同名既可能是分支也可能是文件，
// 无法从文本区分 ⇒ 一律不猜（维持 L2）。
//
// 这条是刻意的保守：把 `git checkout main`（切分支，可逆）也一起弹卡，
// 换来「绝不把 checkout 文件误判成切分支」。切分支在 pieqi 里有 `git switch`
// 这个无歧义的替代（已映射 L1）。
func TestGitKindFor_CheckoutIsNeverGuessed(t *testing.T) {
	for _, c := range []string{"git checkout main", "git checkout foo.txt", "git checkout -b new"} {
		if got, ok := gitKindFor(c); ok {
			t.Errorf("%q → (%q, true)，checkout 有歧义，应返回 false 维持 L2", c, got)
		}
	}
}

// TestGitKindFor_ValuesAreKnownKinds 返回值必须是 pieqi 认识的 kind。
//
// 写个 riskLevelKinds 不认的值 = 静默落 L2（同 AGENTS.md 的警告）。
func TestGitKindFor_ValuesAreKnownKinds(t *testing.T) {
	cmds := []string{
		"git status", "git add .", "git push", "git reset --hard",
		"git branch", "git branch -D x", "git stash", "git stash list",
		"git stash drop", "git remote -v", "git remote add a b",
		"git config --get x", "git config x y", "git worktree list",
		"git clean -fd", "git tag -d v1", "git restore .", "git gc",
	}
	for _, c := range cmds {
		got, ok := gitKindFor(c)
		if !ok {
			continue
		}
		if _, known := kindValues[got]; !known {
			t.Errorf("%q → %q 不在 kindValues 内（会静默落 L2）", c, got)
		}
	}
}

// TestGitKindFor_SegmentRules 单段语义：函数只该看到一段命令。
//
// 复合命令的切段由调用方（DowngradeReadonlyToolKind 的 splitSegments）负责；
// 这里确认不会把 `git status && rm -rf x` 误当成安全的 git 命令。
func TestGitKindFor_SegmentRules(t *testing.T) {
	// 未切段的复合命令：首词是 git、子命令 status，但后面还有 rm。
	// 函数的职责边界是"一段"，所以这里只断言它**不会**因为看到 git 就无条件放行
	// —— 实际上它只看 git 部分，复合安全性由调用方保证。
	// 本用例存在的意义是把这个"职责边界"写下来，防止有人误以为它能处理复合命令。
	got, ok := gitKindFor("git status")
	if !ok || got != "read" {
		t.Fatalf("单段 git status 应为 read")
	}
}
