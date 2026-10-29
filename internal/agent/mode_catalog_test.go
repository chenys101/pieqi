package agent

import (
	"encoding/json"
	"testing"
)

func TestModeOptionJSONUsesSnakeKeys(t *testing.T) {
	b, err := json.Marshal(ModeOption{ID: "plan", Name: "Plan", Description: "read-only"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	// 前端 ModeOptionDto 读 id/name；若漏 tag 会变成 ID/Name，ModePicker 恒空。
	if !containsAll(s, `"id":"plan"`, `"name":"Plan"`) {
		t.Fatalf("json=%s，期望小写 id/name", s)
	}
	if containsAll(s, `"ID":`) {
		t.Fatalf("json 仍含大写 ID：%s", s)
	}
}

func TestResolveAgentModes_CacheBeatsDefault(t *testing.T) {
	RememberAgentModes("qoder", []ModeOption{{ID: "code", Name: "Code"}})
	t.Cleanup(func() {
		modeCatalogMu.Lock()
		delete(modeCatalogCache, "qoder")
		modeCatalogMu.Unlock()
	})
	got := ResolveAgentModes("qoder", []ModeOption{{ID: "agent", Name: "Agent"}})
	if len(got) != 1 || got[0].ID != "code" {
		t.Fatalf("got=%v，期望缓存优先", got)
	}
	got2 := ResolveAgentModes("cursor", []ModeOption{{ID: "agent", Name: "Agent"}, {ID: "plan", Name: "Plan"}})
	if len(got2) != 2 || got2[1].ID != "plan" {
		t.Fatalf("无缓存应回落 DefaultModes：%v", got2)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !containsStr(s, p) {
			return false
		}
	}
	return true
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})())
}
