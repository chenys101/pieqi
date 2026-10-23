// 工具结果图片的落盘与事件映射。
//
// 这组用例守三条**不报错就会错**的判据：
//  1. 事件里**只存元数据**、字节在磁盘（否则任务 JSON 被撑到几十 MB，见 model.TaskEvent.Images）；
//  2. 同一张图重复存只落一份（内容寻址）；
//  3. hash 是唯一拼进路径的参数，形状不合法必须拒绝（否则可用 URL 读任意文件）。
package core

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pieqi/internal/agent"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// attachPNG 1x1 PNG（与 agent 包测试同一张图，便于人工核对）。
const attachPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func newTestAttachmentStore(t *testing.T) *AttachmentStore {
	t.Helper()
	return NewAttachmentStore(t.TempDir())
}

// TestAttachmentStore_SaveReadRoundTrip 落盘后能按 hash 原样取回。
func TestAttachmentStore_SaveReadRoundTrip(t *testing.T) {
	s := newTestAttachmentStore(t)
	hash, size, err := s.Save(attachPNG, "image/png", "x.png")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	want, _ := base64.StdEncoding.DecodeString(attachPNG)
	if size != len(want) {
		t.Errorf("size=%d want %d", size, len(want))
	}
	raw, mime, err := s.Read(hash)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if mime != "image/png" {
		t.Errorf("mime=%q want image/png", mime)
	}
	if string(raw) != string(want) {
		t.Errorf("bytes 不一致：got %d want %d", len(raw), len(want))
	}
}

// TestAttachmentStore_SameContentOneFile 同一张图存两次只落一份。
//
// 判据是目录条目数：按内容寻址的意义就在这里（工具调用常重复读同一个文件）。
func TestAttachmentStore_SameContentOneFile(t *testing.T) {
	s := newTestAttachmentStore(t)
	h1, _, err1 := s.Save(attachPNG, "image/png", "a.png")
	h2, _, err2 := s.Save(attachPNG, "image/png", "b.png")
	if err1 != nil || err2 != nil {
		t.Fatalf("Save: %v / %v", err1, err2)
	}
	if h1 != h2 {
		t.Fatalf("同一内容得到两个 hash：%s / %s", h1, h2)
	}
	entries, err := os.ReadDir(s.Root())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("目录里 %d 个文件，want 1（重复内容必须复用同一份）", len(entries))
	}
}

// TestAttachmentStore_RejectsUnsupportedMime 不在白名单的图片类型直接拒绝。
func TestAttachmentStore_RejectsUnsupportedMime(t *testing.T) {
	s := newTestAttachmentStore(t)
	if _, _, err := s.Save(attachPNG, "image/tiff", "x.tiff"); err == nil {
		t.Fatal("Save(image/tiff) = nil error, want rejection")
	}
	if _, _, err := s.Save("not base64!!!", "image/png", "x.png"); err == nil {
		t.Fatal("Save(bad base64) = nil error, want rejection")
	}
}

// TestAttachmentStore_ReadRejectsBadHash hash 形状不合法必须报专门的错误。
//
// ⚠️ 这是**路径安全的唯一一道闸**：hash 被直接拼进文件路径，
// 放行 `..` 或分隔符等于用 URL 读任意文件。
func TestAttachmentStore_ReadRejectsBadHash(t *testing.T) {
	s := newTestAttachmentStore(t)
	for _, bad := range []string{"", "abc", "../../etc/passwd", strings.Repeat("z", 64), strings.Repeat("a", 63)} {
		if _, _, err := s.Read(bad); !errors.Is(err, ErrInvalidAttachmentHash) {
			t.Errorf("Read(%q) err=%v, want ErrInvalidAttachmentHash", bad, err)
		}
	}
	// 形状合法但不存在：既不是错误也不是命中（调用方回 404）。
	raw, mime, err := s.Read(strings.Repeat("a", 64))
	if err != nil || raw != nil || mime != "" {
		t.Errorf("Read(missing) = (%v,%q,%v), want (nil,\"\",nil)", raw, mime, err)
	}
}

// TestTaskStore_SaveToolImagesPersistsMetadataOnly 事件里只出现元数据，字节只在磁盘。
func TestTaskStore_SaveToolImagesPersistsMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	att := NewAttachmentStore(filepath.Join(dir, "attachments"))
	store, err := NewTaskStore(filepath.Join(dir, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	store.SetAttachments(att, zap.NewNop())

	imgs := store.saveToolImages([]agent.ImageInput{{Data: attachPNG, MimeType: "image/png"}})
	if len(imgs) != 1 {
		t.Fatalf("got %d images, want 1", len(imgs))
	}
	got := imgs[0]
	if !got.Stored {
		t.Error("Stored=false：前端据此不渲染缩略图，等于白存")
	}
	if got.Hash == "" || len(got.Hash) != 64 {
		t.Errorf("hash=%q，want 64 位 sha256", got.Hash)
	}
	if got.Bytes == 0 {
		t.Error("bytes=0：展示层会显示成空图")
	}
	// base64 绝不能出现在元数据里（那就是"把图塞进任务 JSON"的老路）
	if strings.Contains(got.Hash, "iVBOR") {
		t.Error("元数据里混入了图片内容")
	}
}

// TestTaskStore_SaveToolImagesWithoutStoreIsNil 未接线时返回 nil（不影响事件追加）。
func TestTaskStore_SaveToolImagesWithoutStoreIsNil(t *testing.T) {
	dir := t.TempDir()
	store, err := NewTaskStore(filepath.Join(dir, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	if got := store.saveToolImages([]agent.ImageInput{{Data: attachPNG, MimeType: "image/png"}}); got != nil {
		t.Errorf("got %v, want nil（未接线应静默降级）", got)
	}
}

// TestApplyToolCallUpdate_AttachesImagesToToolResult 工具结果的图片落在那条 tool_result 上。
//
// 映射目标是**结果事件**而不是 tool_use：图片是"工具产出的东西"，
// 挂在开工事件上会让时间线在调用开始时就显示一张还没跑出来的图。
func TestApplyToolCallUpdate_AttachesImagesToToolResult(t *testing.T) {
	dir := t.TempDir()
	store, err := NewTaskStore(filepath.Join(dir, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	store.SetAttachments(NewAttachmentStore(filepath.Join(dir, "attachments")), zap.NewNop())

	task := &model.Task{ID: "t1"}
	_ = applyToolCallUpdate(store, task, agent.ToolCallUpdateInfo{
		ToolCallID: "tc-1", Title: "read_image", IsNew: true,
	})
	_ = applyToolCallUpdate(store, task, agent.ToolCallUpdateInfo{
		ToolCallID: "tc-1", Status: "completed",
		Images: []agent.ImageInput{{Data: attachPNG, MimeType: "image/png"}},
	})

	var results, uses int
	for _, ev := range task.Events {
		switch ev.Type {
		case model.EventToolResult:
			results++
			if len(ev.Images) != 1 || !ev.Images[0].Stored {
				t.Errorf("tool_result.Images=%+v，want 1 张已落盘的图", ev.Images)
			}
		case model.EventToolUse:
			uses++
			if len(ev.Images) != 0 {
				t.Errorf("tool_use 不该带图，got %d", len(ev.Images))
			}
		}
	}
	if results != 1 || uses != 1 {
		t.Fatalf("events: %d tool_use / %d tool_result，want 1/1", uses, results)
	}
}
