package agent

import (
	"strings"
)

// NormalizePermissionKind 把 agent 可能给出的 Kind 变体归一成协议常量。
//
// Cursor 实测 kind 已是 allow_once（下划线），optionId 才是 allow-once（连字符）。
// 这里仍做归一，防止日后 hyphen / CamelCase kind，或其它 ACP agent 漂移。
// 未知串原样返回（小写、连字符已转下划线之后），交给调用方决定是否再按 OptionId 模糊匹配。
func NormalizePermissionKind(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	k = strings.ReplaceAll(k, "-", "_")
	switch k {
	case "allow_once", "allowonce":
		return PermissionOptionAllowOnce
	case "allow_always", "allowalways":
		return PermissionOptionAllowAlways
	case "reject_once", "rejectonce":
		return PermissionOptionRejectOnce
	case "reject_always", "rejectalways":
		return PermissionOptionRejectAlways
	default:
		return k
	}
}

// PickAllowOptionID 选首个 allow 选项（allow_once 优先，次 allow_always），返回其 optionId。
//
// Kind 先经 NormalizePermissionKind。若全部 Kind 为空/未知，再按 OptionId 子串模糊匹配
// allow；再不行返回 false —— 调用方应 Cancelled 并打 Error（宁可不干，也不要错选 reject）。
func PickAllowOptionID(opts []PermissionOption) (string, bool) {
	if id, ok := pickByNormalizedKind(opts, PermissionOptionAllowOnce); ok {
		return id, true
	}
	if id, ok := pickByNormalizedKind(opts, PermissionOptionAllowAlways); ok {
		return id, true
	}
	if allKindsEmpty(opts) {
		return fuzzyAllowOptionID(opts)
	}
	return "", false
}

// PickRejectOptionID 选首个 reject 选项（reject_once 优先，次 reject_always）。
func PickRejectOptionID(opts []PermissionOption) (string, bool) {
	if id, ok := pickByNormalizedKind(opts, PermissionOptionRejectOnce); ok {
		return id, true
	}
	if id, ok := pickByNormalizedKind(opts, PermissionOptionRejectAlways); ok {
		return id, true
	}
	if allKindsEmpty(opts) {
		return fuzzyRejectOptionID(opts)
	}
	return "", false
}

func pickByNormalizedKind(opts []PermissionOption, want string) (string, bool) {
	for _, o := range opts {
		if NormalizePermissionKind(o.Kind) == want {
			return o.ID, true
		}
	}
	return "", false
}

func allKindsEmpty(opts []PermissionOption) bool {
	if len(opts) == 0 {
		return false
	}
	for _, o := range opts {
		if strings.TrimSpace(o.Kind) != "" {
			return false
		}
	}
	return true
}

func fuzzyAllowOptionID(opts []PermissionOption) (string, bool) {
	for _, o := range opts {
		id := strings.ToLower(o.ID)
		if strings.Contains(id, "allow") && !strings.Contains(id, "reject") && !strings.Contains(id, "deny") {
			return o.ID, true
		}
	}
	return "", false
}

func fuzzyRejectOptionID(opts []PermissionOption) (string, bool) {
	for _, o := range opts {
		id := strings.ToLower(o.ID)
		if strings.Contains(id, "reject") || strings.Contains(id, "deny") || strings.Contains(id, "cancel") {
			return o.ID, true
		}
	}
	return "", false
}
