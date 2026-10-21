package agent

import "strings"

// 「非 shell 工具」的路径分档（ADR-0015）。
//
// ## 问题
//
// dsh 的 read 归 `read`(L0)、write/edit 归 `edit`(L1)，但有一类工具**既不是
// shell 也不是文件工具**：lsp（代码智能）、job_output / job_kill（后台任务）、
// todo_write、skill…。它们在 dshToolKinds 里刻意留在 other(L2) —— 这个保守取值
// 是对的（`job_kill` 能杀任务、`lsp` 能改代码），代价是**每一次 grep/跳转定义
// 都要人工点**。
//
// ## 判据：不按名字猜，按**参数**证明
//
// lsp 的工具名带参数（`lsp grep`、`<agent>__definition`），按名字放行等于给
// 整个工具发通行证。所以这里只看 rawInput：
//
//   - 参数里出现**写动词**（rename / apply…）⇒ 拒绝；
//   - 参数里只有查询类字段（query / symbol / file / line / pattern…）⇒ read；
//   - 认不出（字段名不在白名单里）⇒ 拒绝。
//
// job_output / todo_write 同理：它们只读运行期状态、不改文件系统。

// nonCommandReadonlyTools 是"参数全是查询字段即可判只读"的工具名集合（小写、整串匹配）。
var nonCommandReadonlyTools = map[string]struct{}{
	// 代码智能（lsp 族）。两种命名都要收：
	//   - 带前缀：`lsp grep` / `lsp_definition`（dsh 风格）；
	//   - 裸工具名：`definition` / `references`（MCP 风格 `gopls__definition`，
	//     前缀由 agent 名占位，剥完只剩工具名）。
	"lsp": {}, "lsp_grep": {}, "lsp_hover": {}, "lsp_definition": {},
	"lsp_references": {}, "lsp_symbols": {}, "lsp_diagnostics": {},
	"lsp_workspace_symbols": {}, "lsp_rename": {}, // rename 由下面的写动词拦下
	"definition": {}, "declaration": {}, "references": {}, "hover": {},
	"symbols": {}, "workspace_symbols": {}, "diagnostics": {}, "document_symbols": {},
	// 后台任务：读输出 / 列状态（`job_kill` 不在表里，它会终止进程）
	"job_output": {}, "job_list": {}, "job_status": {}, "job_wait": {},
	// 会话自述类：只记录"我打算做什么"，不动文件系统
	"todo_write": {}, "write_todos": {}, "todowrite": {},
	// 查询类
	"get_goal": {}, "list_skills": {}, "skill": {},
}

// nonCommandWriteVerbs 是参数里出现即拒绝降级的动词（工具名或参数值）。
//
// `rename` 尤其重要：lsp 的 rename 是**跨文件改写**（一次能改几十个文件），
// 而它往往和 grep 共用同一个工具入口。
//
// ⚠️ 也别把 `write` 列进来：它会误伤 `todo_write` 这种"名字里有 write、实际只
// 记录意图"的工具（ADR-0015 回归用例就是这么把它抓出来的）。判据要落在
// **参数**上（`new_name` / `edits`），而不是工具名里的巧合。
var nonCommandWriteVerbs = []string{
	"rename", "edit", "insert", "replace", "apply", "format",
	"kill", "stop", "cancel", "delete", "remove", "create", "move", "fix",
	"import", "generate", "save",
}

// nonCommandReadonlyFields 是"查询类"参数名白名单（小写）。
//
// 只有**全部**键都落在这里（或值是标量且键不在写动词里）时才判只读。
var nonCommandReadonlyFields = map[string]struct{}{
	"query": {}, "q": {}, "pattern": {}, "symbol": {}, "symbols": {},
	"file": {}, "file_path": {}, "path": {}, "paths": {}, "dir": {}, "directory": {},
	"line": {}, "character": {}, "column": {}, "offset": {}, "limit": {},
	"regex": {}, "case_sensitive": {}, "multiline": {}, "ignore_case": {},
	"name": {}, "kind": {}, "id": {}, "job_id": {}, "since": {}, "tail": {},
	"include": {}, "exclude": {}, "glob": {}, "lang": {}, "language": {},
	"max_results": {}, "maxresults": {}, "context": {}, "depth": {}, "type": {},
	"todos": {}, "status": {}, "text": {}, "content": {}, "uri": {}, "position": {},
	"cwd": {}, "workspace": {}, "root": {}, "project": {},
}

// NonCommandToolKind 返回非 shell 工具按参数求得的档位；第二返回值 false 表示
// "不归本层管或证明不了" —— 调用方应维持原 kind（保守侧）。
//
// 入参 toolName 是 agent 自报的工具名（可能带参数后缀，如 `lsp grep`），
// rawInput 是工具入参 JSON。
func NonCommandToolKind(toolName string, rawInput []byte) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(toolName))
	if name == "" {
		return "", false
	}
	// 名字可能带参数（`lsp grep`）：取首词。
	if i := strings.IndexAny(name, " \t"); i >= 0 {
		name = name[:i]
	}
	// `gopls__definition` / `mcp__lsp__workspace_symbols` 这类带命名空间前缀的形态：
	// 逐个 `__` 切出来的片段都试一遍（最后一段是工具名，前面的段是命名空间，
	// 但 `lsp__workspace_symbols` 这种"命名空间 + 多词工具名"也得认）。
	// 任何一段命中表都算，且**只看名字**（参数仍要过下面的检查）。
	segments := strings.Split(name, "__")
	candidates := make([]string, 0, len(segments))
	for _, s := range segments {
		if s = strings.TrimSpace(s); s != "" {
			candidates = append(candidates, s)
		}
	}
	// 另外把"倒数两段合并"也作为候选：`lsp__workspace_symbols` → `workspace_symbols`。
	if len(segments) >= 2 {
		candidates = append(candidates, segments[len(segments)-1])
	}
	matched := ""
	for _, c := range candidates {
		if _, ok := nonCommandReadonlyTools[c]; ok {
			matched = c
			break
		}
	}
	if matched == "" {
		return "", false
	}
	// 写动词检查对**整串名字**做（`lsp_rename` 的 rename 可能不在任何一段候选里）。
	if containsAnyWord(name, nonCommandWriteVerbs, "_") {
		return "", false
	}
	name = matched

	args, ok := decodeRawInputObject(rawInput)
	if !ok {
		return "", false
	}
	// 空参数（`{}`）：没有任何写意图，也不带路径 —— 只读（工具名里的写动词已在上面拦过）。
	if len(args) == 0 {
		return "read", true
	}
	// ② 参数键名必须是"查询类"：任何不认识的键（可能是 `new_name` / `edits` /
	//    `content` 这种改写参数）都按保守侧处理。
	for k, v := range args {
		if containsAnyWord(k, nonCommandWriteVerbs, "_") {
			return "", false
		}
		if _, known := nonCommandReadonlyFields[strings.ToLower(k)]; !known {
			return "", false
		}
		// 值里出现写动词（`{"command":"rename"}`）同样拒绝。
		if s, isStr := v.(string); isStr && containsAnyWord(strings.ToLower(s), nonCommandWriteVerbs, " ") {
			return "", false
		}
		// 数组元素也看：`{"edits":[...]}` 这类改写负载藏在列表里。
		// 键名未知已在上面拦下，这里兜的是"键名在表里、值却是对象/数组"的漏网形态。
		switch vv := v.(type) {
		case []any:
			for _, el := range vv {
				if m, isMap := el.(map[string]any); isMap {
					// 元素里的键**只要都在只读字段表里**就放行（`{"content","status"}` 是
					// todo 项的标准形状）；出现不认识的键或写动词才拒绝。
					for k2, v2 := range m {
						lk2 := strings.ToLower(k2)
						if containsAnyWord(lk2, nonCommandWriteVerbs, "_") {
							return "", false
						}
						if _, known := nonCommandReadonlyFields[lk2]; !known {
							return "", false
						}
						if s2, isStr := v2.(string); isStr &&
							containsAnyWord(strings.ToLower(s2), nonCommandWriteVerbs, " ") {
							return "", false
						}
					}
					continue
				}
			}
		case map[string]any:
			return "", false // 嵌套对象：形状不明，不猜
		}
	}
	return "read", true
}

// containsAnyWord 判断 s 按 sep 切分（以及整串）后是否命中 verbs 里的任一项。
// sep 传 "_" 时 `lsp_rename` → ["lsp","rename"] 命中 rename。
func containsAnyWord(s string, verbs []string, sep string) bool {
	set := make(map[string]struct{}, len(verbs))
	for _, v := range verbs {
		set[v] = struct{}{}
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '_', '-', ' ', '/':
			return true
		}
		return false
	})
	for _, p := range parts {
		if _, ok := set[strings.ToLower(p)]; ok {
			return true
		}
	}
	return false
}
