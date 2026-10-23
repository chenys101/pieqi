// 图片附件取图端点：归属校验 + 形状校验 + 字节回读。
//
// 这里守的最关键一条是**跨任务读图**：附件目录按内容寻址、全局共享，
// 知道别的任务图片 hash 的人不该能借这个端点把图读走。
package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pieqi/internal/core"
	"pieqi/internal/model"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// attachTestPNG 1x1 PNG（与 core / agent 包测试同一张，便于人工核对）。
const attachTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// attachFixture 一个"有图的任务" + 一个"没图的任务" + 一个真实落盘的附件。
type attachFixture struct {
	router  *gin.Engine
	taskID  string
	otherID string
	hash    string
	raw     []byte
}

func newAttachmentFixture(t *testing.T) attachFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	store, err := core.NewTaskStore(filepath.Join(dir, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	att := core.NewAttachmentStore(filepath.Join(dir, "attachments"))
	store.SetAttachments(att, zap.NewNop())

	// 落一张图（走生产同一条 Save）。
	raw, err := base64.StdEncoding.DecodeString(attachTestPNG)
	if err != nil {
		t.Fatal(err)
	}
	hash, size, err := att.Save(attachTestPNG, "image/png", "x.png")
	if err != nil {
		t.Fatal(err)
	}
	if size != len(raw) {
		t.Fatalf("saved size=%d want %d", size, len(raw))
	}

	withImg, err := store.Create(&model.Task{Title: "with image", Events: []model.TaskEvent{
		{Type: model.EventToolResult, ToolName: "read_image", At: time.Now()},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// 把"已落盘"的引用写进事件（前端只认 stored=true，见 normalizer）。
	if _, err := store.Update(withImg.ID, func(tk *model.Task) bool {
		tk.Events[0].Images = []model.TaskImage{{MimeType: "image/png", Bytes: size, Hash: hash, Stored: true}}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	other, err := store.Create(&model.Task{Title: "without image"})
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{store: store}
	r := gin.New()
	r.GET("/api/tasks/:id/attachments/:hash", s.getTaskAttachment)
	return attachFixture{router: r, taskID: withImg.ID, otherID: other.ID, hash: hash, raw: raw}
}

func (f attachFixture) get(t *testing.T, taskID, hash string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/tasks/"+taskID+"/attachments/"+hash, nil))
	return w
}

// TestGetTaskAttachment_ServesBytes 被本任务引用的图能原样取回。
func TestGetTaskAttachment_ServesBytes(t *testing.T) {
	f := newAttachmentFixture(t)
	w := f.get(t, f.taskID, f.hash)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type=%q want image/png", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("缺少 nosniff：浏览器可能把图片按别的类型解释")
	}
	if got := w.Body.Bytes(); string(got) != string(f.raw) {
		t.Errorf("bytes 不一致：got %d want %d", len(got), len(f.raw))
	}
}

// TestGetTaskAttachment_CrossTaskIsNotFound 别的任务引用不到的图 → 404。
//
// 这条是**跨任务读图的唯一闸门**：磁盘上明明有这张图（同 hash、同目录），
// 但当前任务的事件流里没有引用它。
func TestGetTaskAttachment_CrossTaskIsNotFound(t *testing.T) {
	f := newAttachmentFixture(t)
	if w := f.get(t, f.otherID, f.hash); w.Code != http.StatusNotFound {
		t.Fatalf("跨任务取图 status=%d want 404（附件目录是全局共享的，必须按事件引用校验）", w.Code)
	}
}

// TestGetTaskAttachment_BadHashIs400 形状不合法 → 400（与"不存在"分开）。
func TestGetTaskAttachment_BadHashIs400(t *testing.T) {
	f := newAttachmentFixture(t)
	for _, bad := range []string{"..", strings.Repeat("a", 63), strings.Repeat("z", 64)} {
		if w := f.get(t, f.taskID, bad); w.Code != http.StatusBadRequest {
			t.Errorf("hash=%q status=%d want 400", bad, w.Code)
		}
	}
}

// TestGetTaskAttachment_UnknownTask404 任务不存在 → 404。
func TestGetTaskAttachment_UnknownTask404(t *testing.T) {
	f := newAttachmentFixture(t)
	if w := f.get(t, "no-such-task", f.hash); w.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404", w.Code)
	}
}
