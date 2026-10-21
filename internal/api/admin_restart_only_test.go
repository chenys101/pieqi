package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// POST /api/admin/restart-only 的语义（2026-10-21，用户要求：
// "有新编译的产物就走替换并启动流程，没有的话就只是重新启动"）。
//
// 它和 /restart 的区别只有一条：**永远不换二进制**。所以这里要钉住的是
// "它不会因为 staged=false 而拒绝"—— 那正是旧 /restart 做不到、导致
// "服务要重启但手上没有新产物时无路可走"的那个洞。

// TestAdminRestartOnly_AcceptedWithoutStaged 没有暂存产物也必须能重启。
func TestAdminRestartOnly_AcceptedWithoutStaged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	calls := 0
	s := &Server{}
	// 刻意让 hasStaged 恒为 false：/restart 在这种配置下回 409，
	// 而 /restart-only 必须照常受理。
	s.SetSelfRestart(func() bool { return false },
		func(string) { t.Error("restart（换版）不该被调用") },
		func() string { return "/tmp/build" })
	s.SetRestartOnly(func(initiator string) { calls++ })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/admin/restart-only", nil)
	s.postRestartOnly(c)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d, want 202 body=%s（无产物也必须能只重启）", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["mode"] != "restart-only" {
		t.Fatalf("mode=%v, want restart-only（调用方据此知道没有换版）", body["mode"])
	}
	if body["swapped"] != false {
		t.Fatalf("swapped=%v, want false", body["swapped"])
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && calls == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if calls != 1 {
		t.Fatalf("restart-only 调用次数=%d, want 1", calls)
	}
}

// TestAdminRestartOnly_CarriesInitiator 发起者 id 同样必须透传（重启后要接回会话）。
func TestAdminRestartOnly_CarriesInitiator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var got []string
	s := &Server{}
	s.SetRestartOnly(func(initiator string) { got = append(got, initiator) })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/admin/restart-only", nil)
	c.Request.Header.Set("X-Pieqi-Task-Id", "task-xyz")
	s.postRestartOnly(c)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(got) == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if len(got) != 1 || got[0] != "task-xyz" {
		t.Fatalf("initiators=%v, want [task-xyz]", got)
	}
}

// TestAdminRestartOnly_NotWired501 未接线时 501，不假装成功。
func TestAdminRestartOnly_NotWired501(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/admin/restart-only", nil)
	s.postRestartOnly(c)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d, want 501", w.Code)
	}
}
