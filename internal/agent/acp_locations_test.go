package agent

import (
	"encoding/json"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestEnrichRawInputWithLocations(t *testing.T) {
	locs := []acp.ToolCallLocation{{Path: `D:\code\go\pieqi\web\src\a.vue`}}

	// 空壳 → 写入 file_path
	got := enrichRawInputWithLocations(json.RawMessage(`{}`), locs)
	var m map[string]string
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["file_path"] != `D:\code\go\pieqi\web\src\a.vue` {
		t.Fatalf("file_path=%q", m["file_path"])
	}

	// 已有 file_path → 不动
	keep := json.RawMessage(`{"file_path":"already.go","x":1}`)
	got = enrichRawInputWithLocations(keep, locs)
	if string(got) != string(keep) {
		t.Fatalf("must keep existing path, got %s", got)
	}

	// 无 locations → 原样
	raw := json.RawMessage(`{}`)
	got = enrichRawInputWithLocations(raw, nil)
	if string(got) != `{}` {
		t.Fatalf("no locs: got %s", got)
	}
}

func TestRememberToolInputFresh_BlankObjectDoesNotWipePath(t *testing.T) {
	a := &ACPAgent{}
	id := "tc-1"
	pathIn, _ := json.Marshal(map[string]string{"file_path": "src/a.go"})
	if !a.rememberToolInputFresh(id, "Edit File", pathIn) {
		t.Fatal("path input should be fresh")
	}
	// 后续空壳 update 不得抹掉
	if a.rememberToolInputFresh(id, "Edit File", json.RawMessage(`{}`)) {
		t.Fatal("blank {} must not be treated as fresh overwrite")
	}
	memo, ok := a.toolInput(id)
	if !ok || !rawInputHasFilePath(memo.RawInput) {
		t.Fatalf("path wiped: ok=%v raw=%s", ok, memo.RawInput)
	}
}

func TestRawInputEffectivelyEmpty(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", "  {}  "} {
		if !rawInputEffectivelyEmpty(json.RawMessage(raw)) {
			t.Errorf("%q should be empty", raw)
		}
	}
	if rawInputEffectivelyEmpty(json.RawMessage(`{"file_path":"a"}`)) {
		t.Error("path-bearing input must not be empty")
	}
}
