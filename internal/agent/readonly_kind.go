package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

// 只读 shell 命令 → L0(read) 降级（见 docs/adr/0008-readonly-execute-downgrade.md）。
//
// 为什么需要它：ACP 的 ToolKind 由 agent 自己填，而 qodercli 对**所有** shell 调用
// 一律报 "execute"。于是一条 `sed -n '1,35p' file.go` 与一条 `rm -rf build/`
// 在 pieqi 眼里是同一个 ToolKind，都落 L2、都必须人工点一次。实测一次探查型会话
// 能积累上百次审批，而其中绝大多数只是读文件。
//
// 这里的判据是"**能证明**它只读"，不是"看起来像只读"：
//   - 只认白名单里的只读命令名；
//   - 出现任何复合/重定向/替换语法就放弃（`;`、`&&`、`|`、`>`、反引号、`$(`）——
//     复合命令里任意一段都可能是写操作，逐段拆解既复杂又容易漏；
//   - 命令自带的写形态（`sed -i`、`find -delete`、`sort -o`）显式排除。
//
// 降级失败一律维持原 kind（execute → L2 人工审批），即"证明不了就往保守侧倒"，
// 与 settings_store.go 里 `other` 归 L2 是同一条原则。
//
// 注意这不是安全门禁，只是降噪：真正的兜底是 worktree + Baseline/Checkpoint 可回退
// （ADR-0002）。启发式基于文本，构造性的绕过总是可能的。

// readonlyCommands 只读命令白名单（命令名 → 该命令的**写标志**黑名单）。
//
// 值为空切片表示"这个命令没有已知的写形态"（如 `pwd`、`ls`）；
// 非空时只要参数里出现任一项就不降级。之所以按命令逐个列写标志，
// 是因为 `sed -i`、`sort -o`、`find -delete` 这类"只读命令的写用法"是
// 本启发式最主要的误判来源 —— 漏一个就等于把写操作降成 L0 免审。
var readonlyCommands = map[string][]string{
	// cd 是唯一的"非读取类"白名单项：它不改变文件系统，只是切目录。
	// 之所以单独点出来，是因为它是复合命令的常见前缀 —— 但复合语法
	// 在下面的 shellCompoundTokens 检查里已经整体放弃了，能走到这里的
	// `cd` 必然是**单条** cd，放行安全。
	"cd":     {},
	"ls":     {},
	"cat":    {},
	"head":   {},
	"tail":   {},
	"wc":     {},
	"pwd":    {},
	"echo":   {},
	"date":   {},
	"which":  {},
	"where":  {},
	"type":   {},
	"file":   {},
	"stat":   {},
	"du":     {},
	"df":     {},
	"nl":     {},
	"uniq":   {},
	"jq":     {},
	"diff":   {},
	"comm":   {},
	"join":   {},
	"column": {},
	"cut":    {"-o", "--output"},
	"tr":     {},
	"sort":   {"-o", "--output"},
	"grep":   {},
	"rg":     {},
	"sed":    {"-i", "--in-place"},
	"find":   {"-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fls"},
	"awk":    {"-i", "--in-place"},

	// ---- PowerShell 原生 cmdlet（2026-10-21 补，见 ADR-0015）----
	//
	// 为什么必须补：本机的 agent 跑的是 PowerShell（dsh 的 `pwsh` 工具），
	// 真实命令里 `Get-Content`/`Select-String`/`Where-Object` 远比 `cat`/`grep`
	// 常见。原来这张表只认 POSIX 命令名，于是 `Get-Content x.log | Select-String y`
	// 一律落 L2 弹卡 —— 实测 2026-10-20~21 的 101 次弹卡里，绝大多数都是这类。
	//
	// 判据与上面完全相同：**能证明只读**才进表；带写能力的（`Set-`/`Remove-`/
	// `New-`/`Out-File`/`Tee-Object`/`Export-`…）一个都不进。Cmdlet 名大小写
	// 不敏感，查表前统一 lower（见 baseCommandName）。
	"get-content":   {},
	"get-childitem": {},
	// netstat/Tasklist/IPconfig 之流是诊断类外部命令：只打印系统状态，不写任何东西。
	"netstat":            {},
	"tasklist":           {},
	"ipconfig":           {},
	"whoami":             {},
	"hostname":           {},
	"systeminfo":         {},
	"get-item":           {},
	"get-itemproperty":   {},
	"get-location":       {},
	"get-command":        {},
	"get-date":           {},
	"get-process":        {},
	"get-service":        {},
	"get-filehash":       {},
	"get-acl":            {},
	"get-host":           {},
	"get-help":           {},
	"get-alias":          {},
	"get-module":         {},
	"get-history":        {},
	"get-variable":       {},
	"get-member":         {},
	"get-unique":         {},
	"measure-object":     {},
	"select-object":      {},
	"select-string":      {},
	"sort-object":        {},
	"where-object":       {},
	"foreach-object":     {},
	"group-object":       {},
	"compare-object":     {},
	"format-table":       {},
	"format-list":        {},
	"format-wide":        {},
	"format-custom":      {},
	"out-string":         {},
	"out-host":           {},
	"out-gridview":       {},
	"write-output":       {},
	"write-host":         {},
	"write-warning":      {},
	"write-verbose":      {},
	"test-path":          {},
	"test-connection":    {},
	"test-netconnection": {},
	"resolve-path":       {},
	"join-path":          {},
	"split-path":         {},
	"convertto-json":     {},
	"convertfrom-json":   {},
	"convertto-csv":      {},
	"convertfrom-csv":    {},
	"start-sleep":        {},
}

// 复合/替换/重定向语法：出现任一即放弃降级。
//
// 用 `strings.Contains` 扫全文而不是靠分词，是因为这些 token 无论出现在
// 引号内外都足以说明"这条命令不止一件事"，而粗糙的全文扫描在安全侧
// 只会**多**放弃降级（少放行），不会误放行。
var shellCompoundTokens = []string{
	";", "&&", "||", "|", ">", "<", "`", "$(", "\n", "\r",
	"&>", ">>",
}

// separators 段分隔符：按这些切分后，**每一段都必须是只读命令**才降级。
//
// 为什么必须切段而不是"见复合语法就放弃"：真实数据里 qoder 的 shell 调用
// 几乎全是复合的 —— 实测某会话 100 次审批里 81 次含 `;`、78 次含 `|`、
// 53 次含 `2>`。只认单条命令等于命中率为 0，这个功能就不会有人受益。
// 切段后 `ls -la X 2>/dev/null; echo "---"; find Y | head -60` 这类
// 纯读取的探查命令可以整条放行，而任意一段是写操作则整条拒绝。
//
// 分隔符只列"命令边界"：`;`、`&&`、`||`、`|`、换行。
// 重定向（`>`/`<`）**不**作为分隔符而是单独的拒绝判据（见 hasUnsafeRedirection）。
var separators = []string{"&&", "||", ";", "|", "\n", "\r"}

// hasUnsafeRedirection 判断命令是否含**写向**重定向或命令替换。
//
// `2>/dev/null`、`2>&1`、`>/dev/null` 这类丢弃输出的重定向是无害的，且极其常见
// （实测 53/100 次审批都带 `2>/dev/null`），因此放行；除此之外的任何 `>`
// 都可能是写文件，一律拒绝。`<` 读输入不改文件，为简单起见同样只放行 /dev/null。
//
// 命令替换（反引号、`$(`）会执行**另一条**任意命令，绝不放行。
func hasUnsafeRedirection(cmd string) bool {
	// 先抹掉 PowerShell 的反引号转义（`` `n ``/`` `$ ``/`` `" ``），再把**引号串里的**
	// 反引号同样抹掉 —— 那里的反引号是转义（``"a`nb"``）或数据，不是命令替换。
	// 只剩引号外的裸反引号时才判不安全（那是 POSIX 的命令替换）。
	cmd = backtickEscapeRe.ReplaceAllString(cmd, "")
	cmd = doubleQuotedRe.ReplaceAllStringFunc(cmd, func(s string) string {
		return strings.ReplaceAll(s, "`", "")
	})
	if strings.Contains(cmd, "`") || strings.Contains(cmd, "$(") {
		return true
	}
	// 逐个扫 `>`：任何一处不是"丢弃输出"形态就判为不安全。
	for i := 0; i < len(cmd); i++ {
		if cmd[i] != '>' {
			continue
		}
		if !isDiscardTarget(cmd[i:]) {
			return true
		}
	}
	return false
}

// isDiscardTarget 判断从 `>` 开始的重定向是否是"丢弃输出"（写到 /dev/null 或合并到 1/2）。
// 入参以 `>` 开头（`>` 或 `>>` 均可）。
func isDiscardTarget(rest string) bool {
	r := strings.TrimPrefix(rest, ">")
	r = strings.TrimPrefix(r, ">") // >>
	r = strings.TrimSpace(r)
	for _, ok := range []string{"/dev/null", "&1", "&2"} {
		if strings.HasPrefix(r, ok) {
			return true
		}
	}
	return false
}

// splitSegments 按命令边界切段。切分是朴素的（不处理引号内的分隔符），
// 但偏保守方向是安全的：引号里的 `;` 会被误当分隔符，切出的"段"多半
// 不是合法只读命令 → 拒绝降级（少放行，不是多放行）。
//
// ⚠️ 但有一类"保守"是**有害**的：多行 here-string。实测 2026-10-07 15:03，
// 我自己那条 `git add …; $msg = @'…多行提交信息…'@; git commit -m $msg`
// 被切成几十段，其中大段是中文散文（提交信息本身），没有一段命中白名单
// ⇒ 整条判成 execute(L2) 弹卡 —— 而 `git add`/`git commit` 本该是 L1 免审。
// 这个形态是**日常**（写多行提交信息就是这么写的），不是边缘情况。
//
// 故先剥掉 here-string 与引号串的**内容**（它们是数据、不是命令），再切段。
// 剥掉是安全的：留下的仍是"命令骨架"，判定只会更准，不会更宽。
func splitSegments(cmd string) []string {
	cmd = stripDataLiterals(cmd)
	segs := []string{cmd}
	for _, sep := range separators {
		var next []string
		for _, s := range segs {
			next = append(next, strings.Split(s, sep)...)
		}
		segs = next
	}
	return segs
}

// stripDataLiterals 把 here-string 与引号串的内容替换成占位符，只留命令骨架。
//
// 处理三种形态（PowerShell 为 dsh 的主要 shell，Bash 亦覆盖）：
//   - PowerShell here-string：@'…'@ 与 @"…"@（**必须整体跨行匹配**，否则内容会被当命令）；
//   - 单/双引号串：'…' 与 "…"（同样跨行）；
//   - 反引号串：`…`（**不剥** —— 它是命令替换，剥掉会把"执行另一条命令"藏起来，
//     那正是 hasUnsafeRedirection 要拦的）。
//
// 未闭合的引号/here-string：**原样返回**（不剥）。因为"未闭合"说明我们对结构的
// 理解是错的，此时任何裁剪都可能把真正的命令裁掉 —— 保守侧是不动它、让它照旧
// 落 L2 弹卡。
func stripDataLiterals(cmd string) string {
	out := cmd

	// ① PowerShell here-string：@'...'@ / @"..."@（非贪婪、跨行）
	out = hereStringRe.ReplaceAllString(out, "@''@")
	// ①b PowerShell 反引号转义（反引号 + n/t/$/" 等）：先把**转义本身**抹成空，
	// 这样引号串里不会再出现会把双引号串切碎的反引号，也不会被
	// hasUnsafeRedirection 误判成命令替换（ADR-0015）。
	out = backtickEscapeRe.ReplaceAllString(out, "")
	// ② 普通引号串（跨行，非贪婪）。先双后单，避免嵌套引号时互相吃掉。
	//    PowerShell 里 `"..."` 内的 `""` 是转义引号（`"say ""hi"""`），
	//    逐对匹配恰好能正确切开，无需特殊处理。
	out = doubleQuotedRe.ReplaceAllString(out, "\"\"")
	// 单引号串：PowerShell 里 'it''s' 的 '' 是转义单引号，非贪婪配对会把它拆成
	// 'it' + 's' —— 第二段因此漏在"命令骨架"里（ADR-0015 回归用例覆盖）。
	// 这一版仍是逐对替换，拆出的残段会被 hasUnsafeRedirection 按"引号内数据"
	// 放过，但残段本身只是一两个字符，切段后不足以凑成任何白名单命令 ⇒ 判 execute。
	out = singleQuotedRe.ReplaceAllString(out, "''")
	return out
}

// 正则说明（都带 (?s) 让 . 跨行、非贪婪避免吞掉后续命令）：
//
//	here-string : @' 一直到 '@（或 @" 到 "@），整体换成 @''@
//	双引号串     : 从 " 到最近的 "（要求长度 ≥1，避免把空串 "" 反复替换）
//	单引号串     : 从 ' 到最近的 '
//
// 用正则而非手写状态机：本函数的产物只用于**判定**，不用于执行 ——
// 判错的方向由调用方兜底（判不出来就维持 L2 弹卡）。
var (
	hereStringRe   = regexp.MustCompile(`(?s)@'.*?'@|@".*?"@`)
	doubleQuotedRe = regexp.MustCompile(`(?s)"[^"]*"`)
	singleQuotedRe = regexp.MustCompile(`(?s)'[^']*'`)
)

// backtickEscapeRe 匹配 PowerShell 的反引号转义（n、t、$、" 等单字符转义，以及 `u{XXXX}）。
//
// 为什么不直接把反引号串整个剥掉：POSIX shell 的反引号是**命令替换**
// （感叹号里包命令那种写法），剥掉等于把"执行另一条命令"藏起来 —— 那正是
// hasUnsafeRedirection 要拦的。而 PowerShell 里反引号是转义符，
// 最常见的用法是 "a`nb"（换行）与 "`""（字面引号）。
//
// 折中：只剥**单字符转义**（反引号后紧跟一个字符），且必须出现在双引号串内
// 或者后面那个字符本身就是转义目标（n/t/r/0/$/"/'/`）。剩下的裸反引号
// 仍按命令替换拒绝。
var backtickEscapeRe = regexp.MustCompile("`[ntr0$\"'`\\\\]" + "|`u\\{[0-9a-fA-F]{1,6}\\}")

// hasInlineScript 判断命令里是否带"解释器 + 内联代码"（`node -e …` / `python -c …`）。
//
// 为什么单独判：这类命令的语义全在**引号里那段代码**，而切段前会把引号内容剥空 ——
// 剥完之后 `node -e "…"` 看起来就是一条无害的 `node`。见 ADR-0015 的教训：
// 一次缩进错误的块替换让这条闸掉了两秒，测试立刻抓出 `node -e "…writeFileSync…"`
// 被判成 read。宁可多弹，也不能让它有变宽的机会。
func hasInlineScript(cmd string) bool {
	for _, w := range strings.Fields(stripDataLiterals(cmd)) {
		if w == "-e" || w == "-c" || w == "--eval" {
			return true
		}
	}
	return false
}

// inlineScriptOnly 判断整条命令是否是"**单条**解释器 + 内联代码"（`node -e "…"`）。
//
// 判据：
//   - 切段（剥引号后按 `;`/`|`/换行）之后**只剩一段**；
//   - 该段的命令名是 node/python；
//   - 该段带 `-e`/`-c`/`--eval`。
//
// 复合命令（`cd x; node -e …`）不算 —— 那种交给逐段判定，内联段会落 execute
// 并让整条命令维持人工审（保守侧）。
func inlineScriptOnly(cmd string) bool {
	stripped := stripDataLiterals(cmd)
	segs := splitSegments(cmd)
	nonEmpty := 0
	for _, s := range segs {
		if strings.TrimSpace(s) != "" {
			nonEmpty++
		}
	}
	if nonEmpty != 1 {
		return false
	}
	words := strings.Fields(stripped)
	if len(words) == 0 {
		return false
	}
	switch baseCommandName(words[0]) {
	case "node", "nodejs", "python", "python3", "py":
	default:
		return false
	}
	for _, w := range words[1:] {
		if w == "-e" || w == "-c" || w == "--eval" {
			return true
		}
	}
	return false
}

// isReadonlySegment 单段是否只读：命令名在白名单里且无该命令的写标志。
func isReadonlySegment(seg string) bool {
	words := strings.Fields(seg)
	i := 0
	for i < len(words) {
		w := words[i]
		// 重定向目标（`2>/dev/null` 被切分后可能留下 `/dev/null` 这种孤立词）跳过。
		if strings.HasPrefix(w, "/dev/null") {
			i++
			continue
		}
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") {
			i++ // VAR=val 前缀
			continue
		}
		if _, ok := commandWrappers[baseCommandName(w)]; ok {
			i++
			continue
		}
		break
	}
	if i >= len(words) {
		return false // 整段都是赋值/wrapper/重定向，没有可判定的动词 → 保守拒绝
	}

	name := baseCommandName(words[i])
	writeFlags, ok := readonlyCommands[name]
	if !ok {
		return false // 白名单之外：npm/go/docker/curl 等可能有构建或网络副作用
	}
	args := words[i+1:]
	for _, bad := range writeFlags {
		for _, a := range args {
			// 精确匹配写标志（避免 `-o` 误伤 `--output-style`）。
			if a == bad {
				return false
			}
		}
	}
	return true
}

// baseCommandName 取命令名并剥掉路径前缀与 Windows 可执行扩展名，统一小写。
//
// 2026-10-21 起统一小写（ADR-0015）：PowerShell 的 cmdlet 名**大小写不敏感**
// （`Get-Content` / `get-content` 是同一条），而任务说明里大家习惯写首字母大写。
// 只做 lower 不做别的归一：`git`/`ls` 这些小写命令名本就不受影响，而
// 大写形态的路径（`D:\Code\...`）不会走到这里（这里只处理命令名）。
func baseCommandName(word string) string {
	// 剥路径：取最后一个 `/`（POSIX）或 `\`（Windows，如 `C:\Windows\System32\curl.exe`）之后的部分。
	if i := strings.LastIndexByte(word, '/'); i >= 0 {
		word = word[i+1:]
	}
	if i := strings.LastIndexByte(word, '\\'); i >= 0 {
		word = word[i+1:]
	}
	// 剥可执行扩展名：`icacls.exe` / `npm.cmd` / `pwsh.ps1` 与裸名等价。
	// 只剥这几种 —— 别把 `a.txt` 当成命令名 `a` 放行（那等于白名单通配）。
	//
	// ⚠️ 不是 `.ps1`：PowerShell 里 `.ps1` 文件**默认不能直接执行**（要经过
	// `ExecutionPolicy`；`.\x.ps1` 是"用文件关联运行"，属于任意外部程序），
	// 剥掉它会让 `evil.ps1` 冒充 `evil` 去查白名单。`.cmd`/`.bat` 同样要小心，
	// 但它们是 shell 脚本、内容仍受本文件分段判断约束，故保留。
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
		if len(word) > len(ext) && strings.EqualFold(word[len(word)-len(ext):], ext) {
			word = word[:len(word)-len(ext)]
			break
		}
	}
	return strings.ToLower(word)
}

// wrappers 常见的前置 wrapper 命令名：跳过它们再看真正的命令。
// 注意 `sudo` 只读命令仍是只读，所以放行是安全的。
var commandWrappers = map[string]struct{}{
	"command": {},
	"env":     {},
	"sudo":    {},
	"nice":    {},
	"time":    {},
	// PowerShell 的输出管道不是"动作"，只是给结果排队（ADR-0015）：
	//   `systeminfo | Out-String`、`Get-ChildItem Build | Format-Table`、`... | head`
	// 这些在 POSIX 写法里是 `cmd | out` 两段（`out` 有白名单），而 cmdlet 版把它们
	// 拆成了 `cmdlet | Out-String` —— 段首的 `Out-String` 直接落成了"混合命令"。
	// 只跳"输出整形"，绝不跳 `Tee-Object`（它会写文件）。
	"out-string":   {},
	"out-host":     {},
	"out-gridview": {},
	// `Out-Null`（`… 2>&1 | Out-Null` = 丢弃输出）同理：它不是动作，只是丢弃。
	"out-null": {},
}

// 注意：上面 `out-gridview` 与 `out-null` 分属"会开 GUI / 纯丢弃"两类，
// 但两者都**不产生文件副作用**，所以放在同一张 wrapper 表里。

// DowngradeReadonlyToolKind 把"能证明只读"的 execute 命令降级为 read（L0）。
//
// 命中条件（全部满足才降级）：
//   - kind == "execute"（其它 kind 有 agent 自己的语义，不动）；
//   - 不含写向重定向 / 命令替换；
//   - 按 `;`、`&&`、`||`、`|`、换行切段后，**每一段**都是白名单里的只读命令
//     且不带写标志。
//
// 「每一段都只读」是这里的安全核心：复合命令里只要有一处是写操作，
// 整条就不降级。这既覆盖了真实数据（探查型命令几乎全是复合的），
// 又不会让 `cd x; rm -rf y` 这类命令拿到免审。
//
// 返回原 kind 表示不降级。本函数是纯函数，无副作用，便于表驱动测试。
func DowngradeReadonlyToolKind(kind, command string) string {
	if kind != "execute" {
		return kind
	}
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return kind
	}
	if hasUnsafeRedirection(cmd) {
		return kind
	}
	for _, seg := range splitSegments(cmd) {
		if strings.TrimSpace(seg) == "" {
			continue // `a; ; b` 这类空段忽略
		}
		if !isReadonlySegment(seg) {
			return kind
		}
	}
	return "read"
}

// ClassifyShellCommand 由一条 shell 命令求它**最严**的档位 kind。
//
// 与 DowngradeReadonlyToolKind（二值：read or execute）的分工：
// 这里返回四档（read/edit/execute/delete），用于 git 这类"同一条命令
// 里存在多个档位"的场景 —— 例如 `git status` 该免审，而 `git push` 必须弹卡，
// 二值判断无法区分。
//
// 判定规则（**取整条命令里最严的那一段**）：
//   - 含写向重定向 / 命令替换 ⇒ 直接 execute（不确定，交人工）；
//   - 逐段：git 段用 gitKindFor 求档位；非 git 段用只读白名单判定
//     （只读 → read；否则 → execute）；
//   - 全程取最高档（read < edit < execute < delete）。
//
// 第二返回值 false 表示"无法给出可信档位"（空命令、含不安全重定向等），
// 调用方应维持原 kind（execute/L2 弹卡）。
//
// ⚠️ 本函数只做**分类**，不决定放行。放行仍由 PermissionWire.tryAutoApprove
// 按 kind 走免审名单 —— 与 ADR-0008"降级发生在分类阶段、不是放行阶段"一致。
func ClassifyShellCommand(command string) (string, bool) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return "", false
	}
	if hasUnsafeRedirection(cmd) {
		// 写向重定向/命令替换：任意一段都可能是写操作，不猜。
		return "", false
	}
	// 原文扫描（ADR-0015）：切段会先剥掉引号内容，删除/直写/进程控制等动词若藏在
	// 引号里就会被判成"干净"。这里对**原文**再拦一次 —— 放在本函数里而不是只放在
	// DowngradeReadonlyPermission 里，是为了让任何直接调用分类器的路径拿到同一条闸。
	if permissionReqHasUnsafeText(cmd) {
		return "", false
	}
	// 解释器内联代码（`node -e "…"` / `python -c "…"`）若**原文干净**（没有写/执行动词），
	// 说明它就是一段只读查询（`readFileSync` + `compileTemplate`）。
	//
	// 为什么可以在这一层放行 —— 逐条核过：
	//   ① 原文扫描已确认 code 里没有任何写/执行动词（write/exec/spawn/require('http'…）；
	//   ② hasUnsafeRedirection 已确认没有 `$(` 与引号外反引号；
	//   ③ 整条命令只有这一种"解释器 + -e/-c"时，副作用就只能来自那段代码本身。
	// 这就是 ADR-0015 里"内联代码的唯一读审入口"：原文完整时才看得到代码。
	// 只在**这是一条纯内联代码命令**时生效：混着别的段（`x; node -e …`）则交给逐段判定。
	if inlineScriptOnly(cmd) {
		return "read", true
	}

	worst := "read"
	seenAny := false
	for _, seg := range splitSegments(cmd) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		seenAny = true

		kind := segmentKind(seg)
		worst = worseKind(worst, kind)

		// 已经到最严就别再看了（delete 是上限）。
		if worst == "delete" {
			return worst, true
		}
	}
	if !seenAny {
		return "", false
	}
	return worst, true
}

// segmentKind 单段的档位：git 段走 git 分档表，其余走只读白名单。
//
// 非 git 段只有两种结果：能证明只读 → read，否则 → execute（保守）。
// 这是刻意的：非 git 命令（npm/docker/curl…）没有"本地可逆"的通用判据，
// 硬造一个档位只会扩大放行面。
func segmentKind(seg string) string {
	words := strings.Fields(seg)
	if len(words) == 0 {
		return "execute"
	}
	// 跳过 wrapper 前缀（command/env/sudo/nice/time）后看真正的命令名，
	// 与 isReadonlySegment 的 wrapper 处理保持一致。
	i := 0
	for i < len(words) {
		w := words[i]
		if strings.HasPrefix(w, "/dev/null") {
			i++
			continue
		}
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") {
			i++ // VAR=val 前缀
			continue
		}
		if _, ok := commandWrappers[baseCommandName(w)]; ok {
			i++
			continue
		}
		break
	}
	if i >= len(words) {
		return "execute"
	}

	name := baseCommandName(words[i])
	if name == "git" {
		// 从真正的命令名处**重新拼接**再交给 gitKindFor：它的入参约定是
		// "首词必须是 git"，而这里已剥掉了 wrapper/赋值前缀
		// （`sudo git status` 若整段传进去，words[0] 是 sudo，首词检查会失败）。
		cmdFromGit := strings.Join(words[i:], " ")
		if kind, ok := gitKindFor(cmdFromGit); ok {
			return kind
		}
		// 不认识的 git 动作：保守 L2。
		return "execute"
	}
	// package runner（npm run test/build、npx vitest、playwright test…）见 shell_runner.go。
	if kind, ok := packageRunnerKindFor(words[i:]); ok {
		return kind
	}
	// 解释器 + 内联代码（`node -e "…"` / `python -c "…"`）：段里的代码已被引号剥离
	// 吃空，**在这里证明不了任何东西** ⇒ 落 execute。它的只读判定只在
	// DowngradeReadonlyPermission 的原文扫描里做（ADR-0015）。
	if _, isInline := scriptRunnerKindFor(words[i:]); isInline {
		return "execute"
	}
	if isReadonlySegment(seg) {
		return "read"
	}
	return "execute"
}

// kindSeverity 档位的严苛程度（数字越大越严），用于取"最严的一段"。
var kindSeverity = map[string]int{
	"read":        0,
	"edit":        1,
	"move":        1, // 同为 L1
	"think":       0, // L0
	"search":      0, // L0
	"fetch":       0, // L0
	"execute":     2,
	"other":       2,
	"switch_mode": 2,
	"delete":      3,
}

// worseKind 取两个档位里更严的那个（未知档位按最严处理，保守侧）。
func worseKind(a, b string) string {
	sa, oka := kindSeverity[a]
	sb, okb := kindSeverity[b]
	if !oka {
		return a // 不认识的档位：不擅自替换成别的
	}
	if !okb {
		return b
	}
	if sb > sa {
		return b
	}
	return a
}

// DowngradeReadonlyPermission 对一条权限请求应用命令分档，返回（可能被改写的）请求副本。
//
// 命令文本取自 RawInput 的 command 字段（ACP 的 ShellExecute rawInput 形态）；
// 取不到就原样返回 —— 没有可信的命令文本就不降级。
//
// 两条判据，顺序与理由：
//  1. **git 分档**（ClassifyShellCommand）：能区分 read/edit/execute/delete 四档，
//     处理 `git status`（免审）与 `git push`（弹卡）这类同前缀不同性质的命令。
//  2. **只读降级**（DowngradeReadonlyToolKind，ADR-0008）：二值兜底，
//     覆盖 `ls`/`sed -n`/`grep` 等非 git 的只读命令。
//  3. **原文扫描**（permissionReqHasUnsafeText，ADR-0015）：命令原文里出现
//     删除/直写/进程控制/提权痕迹就整条不降级 —— 不管它出现在引号里还是代码串里。
//     这三条都在**同一个出口**上生效（见函数末尾），所以兜底路径也拿不到例外。
//
// 为什么先 1 后 2：ClassifyShellCommand 更**细**，能给出 edit（L1 免审）这样的档位；
// 而 2 只会给 read 或维持 execute。反过来先跑 2 的话，一条 `git add .`
// 既不在只读白名单（维持 execute），也就永远拿不到它应得的 L1。
//
// 安全取向：三条都"证明不了就维持原 kind"（execute/L2 弹卡）。
func DowngradeReadonlyPermission(req PermissionRequest) PermissionRequest {
	if req.ToolKind != "execute" {
		return req
	}
	// 取命令文本：command 优先，其次是 code/script/cmd…（见 rawInputCommandKeys）。
	// 入参里压根没有可执行文本（如 lsp 的 {method,params}）⇒ 本来就不该走降级 ——
	// 这里显式拦下来，否则"没有命令"会被当成"没有副作用"放行（ADR-0015）。
	cmd, hasText := commandFromRawInputThenAny(req.RawInput)
	if !hasText {
		return req
	}
	// 命令**原文**里就带着需要人类判断的痕迹（引号内的删除/进程控制/
	// Here-String 写文件…）⇒ 不降级（ADR-0015，见 permissionReqHasUnsafeText）。
	if permissionReqHasUnsafeText(cmd) {
		return req
	}

	// ① 命令分档（更细）
	if kind, ok := ClassifyShellCommand(cmd); ok {
		// 只往**更宽**的方向改（execute → read/edit）；不把 execute 升成 delete
		// —— 升档是"更严"，会改变前端卡片的显示强度，那是另一件事（且
		// ClassifyShellCommand 对含重定向等不确定情况本就返回 false）。
		if kindSeverity[kind] < kindSeverity["execute"] {
			req.ToolKind = kind
		}
		return req
	}

	// ② 只读降级兜底（含重定向/无法分档的情况）
	if out := DowngradeReadonlyToolKind(req.ToolKind, cmd); out != req.ToolKind {
		req.ToolKind = out
	}
	return req
}

// rawInputCommandKeys 是工具入参里可能承载「要执行的文本」的字段名。
//
// 为什么不止 `command`：dsh / qoder 的 shell 工具用 `command`，但有些 agent
// 把脚本放在 `code`/`script`/`input`/`cmd` 里。第二个返回值 ok=false 表示
// **入参里根本没有可执行的文本字段**（如 lsp 的 {method,params}）——
// 那种请求无从证明安全性，调用方应保持保守判级而不是"没命令就等于只读"。
var rawInputCommandKeys = []string{"command", "cmd", "code", "script", "input", "shell"}

// commandFromRawInputThenAny 优先取 command；没有再取其它文本字段。
// 第二返回值 ok 表示"该请求里有可执行的文本"，用于决定能否继续做只读降级。
func commandFromRawInputThenAny(raw []byte) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", false
	}
	var found string
	hasText := false
	for _, k := range rawInputCommandKeys {
		s, _ := m[k].(string)
		if s == "" {
			continue
		}
		hasText = true
		if k == "command" {
			return s, true // command 优先
		}
		if found == "" {
			found = s
		}
	}
	return found, hasText
}

// rawInputHasCommand 报告入参里是否带可执行文本（不解码内容）。
func rawInputHasCommand(raw []byte) bool {
	_, ok := commandFromRawInputThenAny(raw)
	return ok
}

// decodeRawInputObject 把 rawInput 解成 map；非 JSON 对象返回 false。
func decodeRawInputObject(raw []byte) (map[string]any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	return m, true
}

// permissionReqHasUnsafeText 判断**命令原文**里是否带着需要人类判断的痕迹。
//
// ## 为什么必须有这一层（2026-10-21 定，ADR-0015）
//
// 分类器（ClassifyShellCommand）先按 `;`/`|`/换行切段，而 splitSegments
// 会先用 stripDataLiterals 把引号串**内容**剥成空串 —— 于是
// `$c.Replace("先读文件", "先读或删除文件")` 里那段中文散文不会干扰分段。
// 但"剥掉内容再判"同时意味着**内容里的危险动作不可见**：
//
//	echo '{"command":"Remove-Item -Recurse -Force D:\x"}'   // 只是打印一段 JSON
//	... | jq -r .command | bash -c ...                        // 才真正执行
//
// 前一条确实可以放行，但两者的**原文**长得一样，而本项目的纪律是
// "证明不了就往保守侧倒"。所以这里对原文做一次**只看不动**的扫描：
// 出现删除类动词、直写 cmdlet/重定向、进程控制、提权，就直接放弃降级。
//
// 刻意**不**把 `rm`/`rmdir` 这类裸词放进表：本仓库大量文档与测试**提到**它们
// （`"rm -rf build/"` 是测试用例的字符串），字符扫描会把整份源码判成危险。
// 这里只列 PowerShell 的复合动词（`Remove-Item`、`Out-File`…）与明确的重定向
// 形态 —— 它们在散文里几乎不会以"命令形态"出现，且一旦出现就意味着有写动作。
func permissionReqHasUnsafeText(cmd string) bool {
	// 引号外的反引号 = POSIX 命令替换；`$(` 同理。先用与分类器相同的规则
	// 抹掉转义与引号内的反引号（原文扫描若比对外的规则更严，会把
	// `$c.Replace("a","b")` 这种日常写法判成危险，整条降级链就被它废掉了）。
	if hasUnsafeRedirection(cmd) {
		return true
	}
	lower := strings.ToLower(cmd)
	for _, bad := range unsafeTextTokens {
		if strings.Contains(lower, bad) {
			return true
		}
	}
	return false
}

// unsafeTextTokens 是命令原文里"一出现就放弃降级"的片段（全部小写比较）。
//
// 分组理由见 permissionReqHasUnsafeText 的注释：这里要的是**高置信**的危险形态，
// 宁可漏一些也别把源码文本误判成危险（那会让整条降级链失效）。
var unsafeTextTokens = []string{
	// —— 删除类（L3 语义；AGENTS.md 的硬边界之一，用户 2026-10-21 明确要求保留人工审）——
	"remove-item", "rm -rf", "rm -recurse", "rm -force", "del /", "del/f", "rd /s",
	"rmdir /s", "clear-content", "clear-recyclebin",

	// —— 直写（不经参数、直接落文件）——
	"out-file", "set-content", "add-content", "writealltext", "writeallbytes",
	"new-item", "copy-item", "move-item", "rename-item", "tee-object",
	"start-transcript", "export-csv", "export-clixml", "set-itemproperty",
	" > ", " >> ", ">\\", ">\"/", ">/dev/",

	// —— 进程/服务/系统控制 ——
	"stop-process", "start-process", "kill ", "taskkill", "stop-service",
	"set-service", "restart-computer", "stop-computer", "shutdown",
	"set-executionpolicy", "icacls", "takeown", "reg add", "reg delete",

	// —— 提权 / 包装 ——
	"runas", "start-job", "start-threadjob", "invoke-expression", "iex ",
	"invoke-webrequest", "invoke-restmethod", "start-bitstransfer",

	// —— 内联代码里"能写/能起进程"的 API（ADR-0015）——
	//
	// 为什么放开 "write" 和 "exec" 这两个词：太短、太常见，`Read-Write` 之类的
	// 注释都会误伤。改成**带语境的**具体形态 —— 它们只会在代码里出现：
	//   writefilesync / writestream / writedata / writeline / appendfile
	//   execsync / execcommand / spawnsync / child_process
	// 代价是拼得很妖的写法能绕过；这里的目标是拦住日常会误写的那一类。
	"writefilesync", "writefilessync", "writestream", "writedata", "writeline",
	"writealltext", "appendfile", "createwritestream",
	"execsync", "execcommand", "spawnsync", "child_process", "subprocess.",
	"os.system", "popen", "fs.chmod", "fs.unlink", "fs.rmdir", "fs.rename",
	"mkdtemp", "makedirs", "shutil.",
}

// commandFromRawInput 从 rawInput 取 shell 命令文本。
//
// ACP 的 shell 工具入参形态是 {"command": "...", ...}（qoder/claude 均是）；
// 取不到（非 JSON、无 command 字段、类型不对）返回空串 —— 空串意味着"不降级"，
// 而不是"没有副作用"。
func commandFromRawInput(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	s, _ := m["command"].(string)
	return s
}
