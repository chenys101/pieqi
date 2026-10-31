package agent

import "strings"

// ShellSegment 一条复合 shell 命令里的单段风险（给人看，不参与放行）。
//
// 放行仍走 ClassifyShellCommand 的「最严段」+ 免审名单；本结构只为审批卡
// 标出「到底是哪一段在拖后腿」，避免用户对着一整坨 `;` 链猜。
type ShellSegment struct {
	Text string // 段原文（截断后），供 UI 展示
	Kind string // read / edit / execute / delete（与 riskLevelKinds 对齐）
}

// ClassifyShellSegments 按命令边界切段并逐段分档。
//
// 与 ClassifyShellCommand 的差异：
//   - 始终返回分段列表（含单段），不做「最严合并」；
//   - 展示用原文切分（不剥引号内容），分类时再对每段走 segmentKind / 原文危险扫描；
//   - 空命令 → nil。
//
// 调用方（审批卡）通常只在 len>=2 时渲染分段条。
func ClassifyShellSegments(command string) []ShellSegment {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return nil
	}
	rawSegs := splitSegmentsRaw(cmd)
	out := make([]ShellSegment, 0, len(rawSegs))
	for _, raw := range rawSegs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		out = append(out, ShellSegment{
			Text: truncateRunes(raw, 160),
			Kind: segmentDisplayKind(raw),
		})
	}
	return out
}

// segmentDisplayKind 单段给人看的档位：先扫危险原文，再走 segmentKind。
func segmentDisplayKind(seg string) string {
	if permissionReqHasUnsafeText(seg) {
		lower := strings.ToLower(seg)
		for _, del := range []string{
			"remove-item", "rm -rf", "rm -recurse", "rm -force",
			"del /", "rd /s", "rmdir /s", "clear-content",
		} {
			if strings.Contains(lower, del) {
				return "delete"
			}
		}
		return "execute"
	}
	// 分类器用剥过引号的骨架，避免 here-string 散文干扰。
	skel := strings.TrimSpace(stripDataLiterals(seg))
	if skel == "" {
		return "execute"
	}
	return segmentKind(skel)
}

// splitSegmentsRaw 按命令边界切段，**不**剥引号/here-string（保留给人看的原文）。
//
// 引号内的 `;` 会被误切 —— 与 splitSegments 一样偏保守：多切几段只会让 UI
// 多几行，不会把危险段藏起来。
func splitSegmentsRaw(cmd string) []string {
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

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
