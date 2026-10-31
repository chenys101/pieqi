package agent

import (
	"encoding/json"
	"strings"
)

// enrichSwitchModePresentation 补全 Cursor「Switch Mode: unknown」空壳的展示。
//
// 只改善时间线 title / RawInput；**不**据此改 Task.Mode。
// 切模真相源仍是 session/set_mode 与 current_mode_update（空壳成功 ≠ 已切模）。
func enrichSwitchModePresentation(title, kind string, raw json.RawMessage, contentText string) (string, json.RawMessage) {
	if !isSwitchModeTool(title, kind) {
		return title, raw
	}
	modeID := switchModeIDFromRaw(raw)
	if modeID == "" {
		modeID = switchModeIDFromTitle(title)
	}
	if modeID == "" {
		modeID = switchModeIDFromContent(contentText)
	}
	if modeID == "" {
		return title, raw
	}
	if switchModeIDFromRaw(raw) == "" {
		raw = mergeModeIDIntoRaw(raw, modeID)
	}
	return "Switch Mode: " + modeID, raw
}

func isSwitchModeTool(title, kind string) bool {
	if strings.EqualFold(strings.TrimSpace(kind), "switch_mode") {
		return true
	}
	head := strings.ToLower(strings.TrimSpace(title))
	return strings.HasPrefix(head, "switch mode")
}

func switchModeIDFromRaw(raw json.RawMessage) string {
	if rawInputEffectivelyEmpty(raw) {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return ""
	}
	for _, k := range []string{"modeId", "mode_id", "targetMode", "target_mode", "mode"} {
		if s := stringFromAny(m[k]); s != "" && !strings.EqualFold(s, "unknown") {
			return s
		}
	}
	return ""
}

func switchModeIDFromTitle(title string) string {
	// "Switch Mode: plan" / "Switch Mode: unknown"
	_, after, ok := strings.Cut(title, ":")
	if !ok {
		return ""
	}
	s := strings.TrimSpace(after)
	if s == "" || strings.EqualFold(s, "unknown") {
		return ""
	}
	// 只要首词（防止多余说明）
	return strings.Fields(s)[0]
}

func switchModeIDFromContent(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	// 短文本且像 mode id（agent/plan/ask）
	first := strings.Fields(text)
	if len(first) == 0 {
		return ""
	}
	cand := strings.Trim(first[0], `"'`)
	if cand == "" || strings.EqualFold(cand, "unknown") {
		return ""
	}
	if len(cand) > 32 || strings.ContainsAny(cand, " \t\n") {
		return ""
	}
	switch strings.ToLower(cand) {
	case "agent", "plan", "ask":
		return cand
	default:
		// 其它 SessionModeId：仅当整段 content 就是这一词时才认，避免把方案正文当 mode
		if len(first) == 1 && len(text) == len(cand) {
			return cand
		}
		return ""
	}
}

func mergeModeIDIntoRaw(raw json.RawMessage, modeID string) json.RawMessage {
	obj := map[string]any{}
	if !rawInputEffectivelyEmpty(raw) {
		_ = json.Unmarshal(raw, &obj)
		if obj == nil {
			obj = map[string]any{}
		}
	}
	obj["modeId"] = modeID
	b, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return b
}

func stringFromAny(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	default:
		return ""
	}
}
