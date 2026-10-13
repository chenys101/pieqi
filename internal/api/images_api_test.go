// images_api_test.go 图片入参的**接口层**校验（接口安全）。
//
// 背景：图片校验原本只在 agent 层（buildPromptBlocks → validateImage）做，那是
// **发送时**才发生的。于是永远发不出去的输入会先拿到 201 Created、任务被持久化、
// user 事件里写进假元数据（如 bytes:0 的空图），再以 failed 收场 ——
// 留下需要人工清理的垃圾，且接口对无效输入回了"成功"。
//
// 这组用例钉住的是：**非法图片必须在创建任何任务之前被 400 拒绝**。
// 断言必须同时检查"状态码"与"没建任务"，只看状态码会漏掉"先建后炸"的回归
// （那正是修复前的行为：先 201，再 failed）。
package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pieqi/internal/agent"
	"pieqi/internal/model"
)

// TestAPI_CreateTaskRejectsInvalidImages 非法图片入参 → 400，且**不创建任务**。
func TestAPI_CreateTaskRejectsInvalidImages(t *testing.T) {
	srv, store, r := setupAPITest(t)
	srv.SetAgents([]agent.AgentInfo{
		{Name: agent.AgentClaude, DisplayName: "Claude Code"},
		{Name: agent.AgentQoder, DisplayName: "Qoder CLI"},
	}, agent.AgentClaude)

	repoDir := t.TempDir()
	post := func(t *testing.T, images []imageReq) *httptest.ResponseRecorder {
		t.Helper()
		b, _ := json.Marshal(createTaskReq{ProjectPath: repoDir, Prompt: "看图", Agent: agent.AgentQoder, Images: images})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/tasks", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}

	valid := base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})

	cases := []struct {
		name    string
		images  []imageReq
		wantSub string // 错误信息里应出现的关键片段（给用户看的，必须点明原因）
	}{
		{
			// 空图是最隐蔽的一种：base64 解码得 0 字节，"看起来"合法。
			// 修复前它会 201 通过并把 bytes:0 写进任务文件。
			name:    "空 data",
			images:  []imageReq{{Data: "", MimeType: "image/png"}},
			wantSub: "图片数据为空",
		},
		{
			name:    "非法 base64",
			images:  []imageReq{{Data: "not-base64!", MimeType: "image/png"}},
			wantSub: "不是合法的 base64",
		},
		{
			// data URL 前缀是最常见的误用，agent 层专门为它写了一条错误。
			name:    "带 data URL 前缀",
			images:  []imageReq{{Data: "data:image/png;base64," + valid, MimeType: "image/png"}},
			wantSub: "纯 base64",
		},
		{
			name:    "不支持的 MIME",
			images:  []imageReq{{Data: valid, MimeType: "image/bmp"}},
			wantSub: "不支持的图片类型",
		},
		{
			name:    "空 MIME",
			images:  []imageReq{{Data: valid, MimeType: ""}},
			wantSub: "不支持的图片类型",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(store.List())
			w := post(t, tc.images)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400（非法图片必须在建任务前拒绝，不能先 201 再失败）",
					w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.wantSub) {
				t.Errorf("body=%s 应包含 %q（错误信息要能让用户知道怎么改）", w.Body.String(), tc.wantSub)
			}
			// 核心断言：不得留下任何任务（含脏元数据的失败任务）
			if after := len(store.List()); after != before {
				t.Fatalf("任务数 %d → %d：非法图片绝不能建出任务（旧行为是先落库再 failed）", before, after)
			}
		})
	}
}

// TestAPI_CreateTaskAcceptsValidImage 合法图片仍然照常创建（别把校验做成一刀切）。
func TestAPI_CreateTaskAcceptsValidImage(t *testing.T) {
	srv, _, r := setupAPITest(t)
	srv.SetAgents([]agent.AgentInfo{{Name: agent.AgentQoder, DisplayName: "Qoder CLI"}}, agent.AgentQoder)

	repoDir := t.TempDir()
	raw := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	b, _ := json.Marshal(createTaskReq{
		ProjectPath: repoDir, Prompt: "看图", Agent: agent.AgentQoder,
		Images: []imageReq{{Data: base64.StdEncoding.EncodeToString(raw), MimeType: "image/png"}},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s, want 201（合法图片不该被误拦）", w.Code, w.Body.String())
	}
	var task model.Task
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// 元数据要落：张数/大小/哈希 —— UI 靠它显示"这条消息带了几张图"
	var found bool
	for _, ev := range task.Events {
		if ev.Type == model.EventUser && len(ev.Images) > 0 {
			found = true
			if ev.Images[0].Bytes != len(raw) {
				t.Errorf("bytes=%d want %d", ev.Images[0].Bytes, len(raw))
			}
			if ev.Images[0].Hash == "" {
				t.Error("hash 为空：图片元数据必须可追溯")
			}
		}
	}
	if !found {
		t.Error("user 事件没带图片元数据：前端无法显示这条消息带了图")
	}
}

// TestAPI_CreateTaskRejectsTooManyImages 张数上限在接口层就拦（不等 agent）。
func TestAPI_CreateTaskRejectsTooManyImages(t *testing.T) {
	srv, store, r := setupAPITest(t)
	srv.SetAgents([]agent.AgentInfo{{Name: agent.AgentQoder, DisplayName: "Qoder CLI"}}, agent.AgentQoder)

	valid := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	imgs := make([]imageReq, 9) // 上限是 8，见 agent.maxPromptImages
	for i := range imgs {
		imgs[i] = imageReq{Data: valid, MimeType: "image/png"}
	}

	repoDir := t.TempDir()
	b, _ := json.Marshal(createTaskReq{ProjectPath: repoDir, Prompt: "太多图", Agent: agent.AgentQoder, Images: imgs})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/tasks", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400（超过 8 张要在接口层拒绝）", w.Code)
	}
	if len(store.List()) != 0 {
		t.Error("超限请求不得建出任务")
	}
}
