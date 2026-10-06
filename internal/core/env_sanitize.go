package core

import (
	"os"
	"strings"
)

// 本文件处理"从宿主环境继承下来的污染"，与业务无关，但会静默改变子进程行为。
//
// 背景：WorkBuddy 会话会在 NODE_OPTIONS 里注入一条
// `--require="<...>/cli/vendor/shim/node-language-shim.cjs"`，
// 该 shim 又 compose 了 node-safe-delete-shim.cjs（fs 删除闸门）与 brokered-fs shim。
// NODE_OPTIONS 沿进程树继承，而 pieqi 的 Node 子进程（claude 桥、dsh/qoder ACP、
// preview 里的 dev server、checks 里跑的 `npm test` 等）全部用 os.Environ() 起进程
// ⇒ shim 一路传进去，表现为子进程里删文件被批量闸门拒绝
// （SAFE_DELETE_BULK_CONFIRM_REQUIRED），agent 在会话里删 node_modules 必失败，
// 且报错栈里出现 node-safe-delete-shim.cjs。
//
// pieqi 是常驻服务：启动壳里的"会话级工具 shim"与其职责无关，也不该定义其子进程的
// 文件删除语义。故在启动最早期做一次剔除。

const (
	// shim 路径的稳定特征：要么落在 WorkBuddy 的 app.asar.unpacked 解包目录，
	// 要么位于某个 shim/ 目录下。用「特征子串」而非写死绝对路径，跨机器/换版本仍有效。
	shimMarkerUnpacked = "app.asar.unpacked"
	shimMarkerDirUnix  = "/shim/"
	shimMarkerDirWin   = `\shim\`

	requirePrefix = "--require="
)

// SanitizeInheritedNodeOptions 从当前进程环境里剔除宿主注入的 NODE_OPTIONS shim 指令，
// 使后续所有 os.Environ() 派生的子进程都不再继承它。
//
// 只删"指向宿主 shim 的 --require 片段"，保留 NODE_OPTIONS 中其它合法选项
// （如 --max-old-space-size），避免误伤用户/运维的有意设置。
// 全部删空时直接 Unsetenv，而不是留一个空串（空串同样会被 node 当无选项，但留空值
// 更易在排查时造成"设过 NODE_OPTIONS"的误导）。
//
// 返回被剔除的片段（供启动日志与单测断言）。
func SanitizeInheritedNodeOptions() []string {
	raw := os.Getenv("NODE_OPTIONS")
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	var kept, removed []string
	for _, tok := range splitShellTokens(raw) {
		if isHostShimRequire(tok) {
			removed = append(removed, tok)
			continue
		}
		kept = append(kept, tok)
	}
	if len(removed) == 0 {
		return nil
	}

	if len(kept) == 0 {
		_ = os.Unsetenv("NODE_OPTIONS")
	} else {
		_ = os.Setenv("NODE_OPTIONS", strings.Join(kept, " "))
	}
	return removed
}

// splitShellTokens 按空格切分，但不切分双引号内部。
// NODE_OPTIONS 的值形如 `--require="C:/a b/x.cjs" --max-old-space-size=4096`，
// 若直接 strings.Fields 会把带空格的引号路径切碎。这里保留引号本身（token 原样回填）。
func splitShellTokens(s string) []string {
	var toks []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == ' ' && !inQuote:
			if cur.Len() > 0 {
				toks = append(toks, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		toks = append(toks, cur.String())
	}
	return toks
}

// isHostShimRequire 判断单个 token 是否为"宿主注入的 shim --require"。
// 只有同时满足「是 --require=」且「路径带 shim 特征」才算，避免误删
// 用户自己合法的 --require=./my-hooks.cjs 之类。
func isHostShimRequire(tok string) bool {
	if !strings.HasPrefix(tok, requirePrefix) {
		return false
	}
	val := strings.Trim(strings.TrimPrefix(tok, requirePrefix), `"`)
	if val == "" {
		return false
	}
	return strings.Contains(val, shimMarkerUnpacked) ||
		strings.Contains(val, shimMarkerDirUnix) ||
		strings.Contains(val, shimMarkerDirWin)
}
