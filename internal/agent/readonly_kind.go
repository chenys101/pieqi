package agent

import (
	"encoding/json"
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
func splitSegments(cmd string) []string {
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

// baseCommandName 取命令名并剥掉路径前缀。
func baseCommandName(word string) string {
	// 剥路径：取最后一个 `/` 之后的部分。
	if i := strings.LastIndexByte(word, '/'); i >= 0 {
		word = word[i+1:]
	}
	return word
}

// wrappers 常见的前置 wrapper 命令名：跳过它们再看真正的命令。
// 注意 `sudo` 只读命令仍是只读，所以放行是安全的。
var commandWrappers = map[string]struct{}{
	"command": {},
	"env":     {},
	"sudo":    {},
	"nice":    {},
	"time":    {},
}

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

// DowngradeReadonlyPermission 对一条权限请求应用只读降级，返回（可能被改写的）请求副本。
//
// 命令文本取自 RawInput 的 command 字段（ACP 的 ShellExecute rawInput 形态）；
// 取不到就原样返回 —— 没有可信的命令文本就不降级。
func DowngradeReadonlyPermission(req PermissionRequest) PermissionRequest {
	if req.ToolKind != "execute" {
		return req
	}
	cmd := commandFromRawInput(req.RawInput)
	if cmd == "" {
		return req
	}
	if out := DowngradeReadonlyToolKind(req.ToolKind, cmd); out != req.ToolKind {
		req.ToolKind = out
	}
	return req
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
