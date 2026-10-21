package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pieqi/internal/model"
)

// TestAPI_BackfillTitles 补标题入口的路由与语义。
//
// 它存在的理由：标题只在创建那一刻生成，失败就补不回来（2026-10-16 起有 4 天
// 全线为空，当时唯一的补救是重建任务）。这条用例钉住三件事：
//   - 端点存在且返回 202 + 排期数；
//   - **只**挑"有 prompt 但空标题"的任务，已有标题的不动；
//   - body 可省略（curl/PWA 不带 body 也要能用）。
//
// ⚠️ 这里刻意不真的等标题生成：本包的测试环境里没人接线 agent，
// 兜底会去 spawn 本机 claude（CI 上可能装也可能没装）。"生成行为本身"
// 由 core 包的 TestBackfillTitles / TestGenerateTitle_* 覆盖。
func TestAPI_BackfillTitles(t *testing.T) {
	_, store, r := setupAPITest(t)

	dir := t.TempDir()
	if _, err := store.Create(&model.Task{ProjectID: "p", Prompt: "需要补标题", ProjectPath: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(&model.Task{ProjectID: "p", Prompt: "已有标题", ProjectPath: dir, Title: "别动我"}); err != nil {
		t.Fatal(err)
	}

	// 不带 body
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/backfill-titles", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Scheduled int  `json:"scheduled"`
		Force     bool `json:"force"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Scheduled != 1 || resp.Force {
		t.Fatalf("resp = %+v, want scheduled=1 force=false（只有空标题的那条）", resp)
	}

	// 显式 force
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/admin/backfill-titles", bytes.NewReader([]byte(`{"force":true}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("force status=%d body=%s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Force {
		t.Fatalf("force 未被识别: %+v", resp)
	}
}
