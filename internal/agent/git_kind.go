package agent

import "strings"

// git 子命令的分档（L0 只读 / L1 本地可逆 / L2 影响远端 / L3 破坏性）。
//
// ## 为什么需要单独一层
//
// ADR-0008 的只读降级是**二值**的：能证明只读 → `read`(L0 免审)，否则维持
// `execute`(L2 弹卡)。这对 `ls`/`sed -n` 够用，但 git 不是二值的：
//
//	git status      只读，该免审
//	git add/commit  本地可逆（有 reflog/工作区可回退），L1 该免审
//	git push        影响**共享远端**，必须人工
//	git reset --hard 直接丢弃未提交内容，L3
//
// 若硬塞进"只读 or execute"两条路，`git status` 与 `git reset --hard` 会得到
// 同一个结果（都维持 execute 弹卡）—— 分档就失去意义。所以本文件返回的是
// **档位**（kind），由调用方决定怎么用。
//
// ## 档位用既有 ToolKind 表达，不新造风险等级
//
// 返回值必须落在 `riskLevelKinds`（internal/core/settings_store.go）认识的
// kind 里，否则会静默落 L2（同样见 AGENTS.md 的警告）：
//
//	L0 → "read"    只读（免审，由 auto_approve_l0 控制）
//	L1 → "edit"    本地可逆写（免审，由 auto_approve_l1 控制）
//	L2 → "execute" 维持原判级（弹卡）
//	L3 → "delete"  破坏性（弹卡）
//
// 注意 L1 用 `edit` 是**语义借用**：git add/commit 不是"编辑文件"，但它在
// pieqi 的分级里等价于"本地可逆的写"。借用既有 kind 而不是新增 `git-write`，
// 是为了不碰 riskLevelKinds 那张唯一真相表（新增 kind 要同时改前后端与文档）。
//
// ## 安全取向
//
// **认不出就不降级**：子命令不在表里 → 返回 `""`（调用方维持原 kind =
// execute/L2 弹卡）。宁可多弹一次，也不放行一个没枚举过的 git 动作。

// gitRiskKinds 是"子命令 → kind"的分档表。
//
// 键是**子命令名**（`git` 之后的第一个非选项词），值是上面说明的 kind。
var gitRiskKinds = map[string]string{
	// ---- L0 只读：不改变任何状态，可安全免审 ----
	"status":        "read",
	"log":           "read",
	"diff":          "read",
	"show":          "read",
	"blame":         "read",
	"describe":      "read",
	"shortlog":      "read",
	"whatchanged":   "read",
	"rev-parse":     "read",
	"ls-files":      "read",
	"ls-tree":       "read",
	"rev-list":      "read",
	"cat-file":      "read",
	"symbolic-ref":  "read",
	"grep":          "read",
	"count-objects": "read",
	"verify-commit": "read",
	"verify-tag":    "read",
	"for-each-ref":  "read",
	"merge-base":    "read",
	"name-rev":      "read",
	"show-ref":      "read",
	"var":           "read",
	"version":       "read",

	// 以下 L0 子命令要**看参数**才安全，见 gitKindFor 的特判：
	"branch":   "read", // 无参/-l/-a/-v/-r 只读；-d/-D/-m/-M 是写
	"tag":      "read", // 无参/-l/-n 只读；-d 是删除
	"remote":   "read", // -v/无参 只读；add/remove/set-url 是写
	"stash":    "read", // list/show 只读；push/pop/drop/clear 是写
	"config":   "read", // --get/--list 只读；其它是写
	"worktree": "read", // list 只读；add/remove/prune 是写

	// ---- L1 本地可逆写：有 reflog / 工作区可回退 ----
	"add":    "edit",
	"commit": "edit",
	"merge":  "edit", // 冲突可 reset --merge 回退（但不 fast-forward 时也可能踩坑，见下）
	"mv":     "edit",
	"switch": "edit",
	// ⚠️ `restore` / `checkout` **不在这里** —— 它们带路径参数时会丢弃工作区改动，
	// 必须由 gitKindFor 的特判降到 L3（见那里）。放进这张表会被当成"本地可逆"免审，
	// 而"丢弃未提交改动"是不可逆的。
	"apply":       "edit",
	"cherry-pick": "edit",
	"revert":      "edit",
	"am":          "edit",

	// ---- L2 影响远端 / 全局：必须人工 ----
	"push":          "execute",
	"fetch":         "execute",
	"pull":          "execute",
	"clone":         "execute",
	"init":          "execute",
	"submodule":     "execute",
	"lfs":           "execute",
	"filter-branch": "delete",
	"gc":            "execute", // 可能删不可达对象（有 reflog 兜底，但属"动仓库内部"，不降级）
	"prune":         "execute", // 同上

	// ---- L3 破坏性：丢弃内容 / 改写历史 ----
	"clean": "delete", // -f/-fd/-fdx 会删未跟踪文件
	"reset": "delete", // 默认 --mixed 退暂存；--hard 丢工作区改动
}

// gitDangerousFlags 是"子命令本身只读，但某些标志让它变危险"的高危标志表。
//
// 键是子命令，值是该子命令下**一旦出现就升到 L3** 的标志前缀。
// 与 readonlyCommands 的 writeFlags 同思路，但那个表只能表达"不准降级"，
// 这里要表达"升到 L3"（更明确）。
var gitDangerousFlags = map[string][]string{
	"reset":    {"--hard", "--merge", "--keep"},
	"clean":    {"-f", "-fd", "-fdx", "-ff", "-ffd", "-ffdx", "--force"},
	"branch":   {"-D", "--delete", "-d", "-m", "-M", "--move"},
	"tag":      {"-d", "--delete"},
	"push":     {"--force", "-f", "--force-with-lease", "--delete", "-d", "--mirror", "--all"},
	"checkout": {"--"}, // checkout -- <path> 丢弃工作区改动
	"restore":  {"--"}, // restore <path> 丢弃工作区改动
	"stash":    {"drop", "clear"},
	"config":   {"--global", "--system", "--unset", "--add", "--replace-all"},
	"worktree": {"remove", "prune"},
}

// gitSubcommandISH 是要跳过的 git 全局选项（它们可能带值，也可能不带）。
//
// `git -C /path status`、`git -c k=v log`、`git --no-pager diff` 都是常见写法，
// 不跳过就会把 `-C` 的值当成子命令。
//
// 只列"确定吃一个值"的：`-C`/`-c`/`--git-dir`/`--work-tree`/`--namespace`。
// 不吃值的（`--no-pager`/`-P`/`--bare`/`--version`）单独列出，避免误吞子命令。
var gitGlobalOptionsWithValue = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path"}
var gitGlobalOptionsNoValue = []string{"--no-pager", "-P", "--bare", "--literal-pathspecs", "--no-replace-objects"}

// gitKindFor 由一条**纯 git 命令**求它的分档 kind。
//
// 入参 cmd 应当是**单段**命令（调用方已按 `;`/`&&`/`|` 切段），且已确认
// 首词是 `git`。第二返回值为 false 表示"不认识/无法安全判断"——
// 调用方应维持原 kind（execute/L2 弹卡），**不要**当成"这条命令无害"。
//
// 判定顺序（每一步不通过就返回 false → 保守）：
//  1. 剥掉 git 全局选项，取到子命令；
//  2. 子命令必须在 gitRiskKinds 里；
//  3. 该子命令的高危标志（gitDangerousFlags）优先于基础档位 ——
//     `reset` 基础是 delete、`reset --hard` 仍是 delete；
//     但 `branch` 基础是 read、`branch -D` 必须是 delete；
//  4. 别名一律不认（配置里查不到，不猜）。
func gitKindFor(cmd string) (string, bool) {
	words := strings.Fields(cmd)
	if len(words) == 0 {
		return "", false
	}
	// 首词必须是 git（调用方保证，这里再核一次）
	if baseCommandName(words[0]) != "git" {
		return "", false
	}

	sub, rest, ok := splitGitSubcommand(words[1:])
	if !ok {
		return "", false
	}

	base, known := gitRiskKinds[sub]
	if !known {
		// 子命令不在表里（含用户 alias）：不猜。
		return "", false
	}

	// 高危标志优先：把档位**升**到 L3。
	if flags, ok := gitDangerousFlags[sub]; ok {
		for _, f := range flags {
			if hasFlag(rest, f) {
				return "delete", true
			}
		}
	}

	// 参数敏感的子命令：按首个参数细分（见各 case 注释）。
	switch sub {
	case "branch", "tag", "remote", "stash", "config", "worktree":
		return gitKindForParamSensitive(sub, rest)
	case "restore":
		// `git restore <path>` / `git restore .` 丢弃工作区改动 —— 与 `--` 无关，
		// **有路径参数就是丢弃**。只有 `-s <tree>`（指定源）之类的少数形态不是。
		// 判据取保守侧：只要出现非标志参数就 L3。
		if hasNonFlagArg(rest) {
			return "delete", true
		}
		return "", false // 无路径的 restore 语义不明确 ⇒ 不猜
	case "checkout":
		// `git checkout -- <path>` 由 gitDangerousFlags("--") 拦下（L3）。
		// 但 `git checkout <path>`（无 --）在某些 git 版本同样丢弃改动；
		// 而 `git checkout <branch>` 是切分支（本地可逆）。
		// 两者无法从文本区分（同名既可能是分支也可能是文件）⇒ **不猜**，维持 L2。
		return "", false
	}

	return base, true
}

// gitKindForParamSensitive 处理"基础只读、但可能只是参数写"的子命令。
//
// 这些子命令（branch/tag/remote/stash/config/worktree）的共同点：
// **同一子命令名，不带参数时是查询（安全），带特定参数时是写**。
// 例如 `git branch` 列分支（安全）vs `git branch -d x` 删分支（危险）。
// 高危标志已在上游拦过一轮，这里处理"不带标志但是写操作"的形态。
func gitKindForParamSensitive(sub string, rest []string) (string, bool) {
	// 取第一个非标志参数（"动词"，如 stash 的 push/drop、remote 的 add）
	var verb string
	for _, w := range rest {
		if strings.HasPrefix(w, "-") {
			continue
		}
		verb = w
		break
	}

	switch sub {
	case "stash":
		switch verb {
		case "", "list", "show": // `git stash` 无参 = stash push（写！）
			if verb == "" {
				return "edit", true // 无参 stash = 保存当前改动（可 pop 回来）
			}
			return "read", true
		case "push", "save", "pop", "apply", "branch", "create":
			return "edit", true // 本地可逆
		case "drop", "clear":
			return "delete", true // 丢弃 stash 内容
		}
		return "", false
	case "remote":
		switch verb {
		case "", "-v", "show", "get-url":
			return "read", true
		case "add", "remove", "rm", "rename", "set-url", "set-head", "prune":
			return "execute", true // 改远端配置，影响后续 push/fetch
		}
		return "", false
	case "config":
		// 已在高危标志里拦了 --global/--system/--unset/--add。
		// 剩下的：带 --get/--list/-l 是读；带两个非标志词是写（git config k v）。
		for _, w := range rest {
			if w == "--get" || w == "--list" || w == "-l" || w == "--get-all" || w == "--get-regexp" {
				return "read", true
			}
		}
		nonFlag := 0
		for _, w := range rest {
			if !strings.HasPrefix(w, "-") {
				nonFlag++
			}
		}
		if nonFlag >= 2 {
			return "edit", true // git config key value —— 写本地配置
		}
		if nonFlag == 1 {
			return "read", true // git config key —— 读单个值
		}
		return "", false
	case "worktree":
		switch verb {
		case "", "list":
			return "read", true
		case "add", "move", "repair", "lock", "unlock":
			return "edit", true
		}
		return "", false
	case "branch", "tag":
		// 无参数 = 列表（只读）。带一个非标志参数 = 创建/删除分支，
		// 但删除已由 -d/-D 拦下（见 gitDangerousFlags）；此处剩下的
		// "创建分支/tag" 属本地可逆。
		if verb == "" {
			return "read", true
		}
		return "edit", true
	}
	return "", false
}

// splitGitSubcommand 跳过 git 全局选项，返回子命令与其余参数。
//
// 处理三种形态：
//
//	git -C /path status        → sub=status, rest=[]
//	git -c core.pager=cat log  → sub=log
//	git --no-pager diff        → sub=diff
func splitGitSubcommand(args []string) (sub string, rest []string, ok bool) {
	i := 0
	for i < len(args) {
		w := args[i]
		if !strings.HasPrefix(w, "-") {
			break // 子命令
		}
		// 吃一个值的全局选项：`-C /path`（也可能是 `-C/path`）
		if matched, consumesValue := matchOption(w, gitGlobalOptionsWithValue); matched {
			if consumesValue && !strings.Contains(w, "=") && len(w) == len(strings.SplitN(w, "=", 2)[0]) {
				// 形如 `-C` 且没有 `=`：下一个词是它的值
				if !strings.Contains(w, "/") || w == "-C" || w == "-c" {
					i += 2
					continue
				}
			}
			i++
			continue
		}
		if _, ok := matchOption(w, gitGlobalOptionsNoValue); ok {
			i++
			continue
		}
		// 其它不认识的标志（如 `--paginate`）：保守起见，**当作子命令解析失败**。
		// 猜它吃不吃值都可能把后面的词误当子命令 —— 不猜。
		return "", nil, false
	}
	if i >= len(args) {
		// 只有 `git` 或 `git -C x`：没有子命令 ⇒ 不认识（不降级）
		return "", nil, false
	}
	return args[i], args[i+1:], true
}

// matchOption 判断 w 是否匹配 opts 之一（支持 `--opt=value` 形态）。
// 返回 (是否匹配, 是否仍需吃下一个词作为值)。
func matchOption(w string, opts []string) (bool, bool) {
	for _, o := range opts {
		if w == o {
			return true, true
		}
		if strings.HasPrefix(w, o+"=") {
			return true, false // 值已在 = 后面
		}
	}
	return false, false
}

// hasFlag 判断参数里是否出现该标志（精确匹配，避免 `-f` 误伤 `--force-with-lease`）。
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// hasNonFlagArg 判断参数里是否有非标志词（即"路径/分支名/键值"这类实参）。
func hasNonFlagArg(args []string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return true
		}
	}
	return false
}
