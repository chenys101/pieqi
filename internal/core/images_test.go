package core

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"pieqi/internal/agent"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

// 图片 prompt 的 core 层测试：验证"图真的传到了 adapter"与"任务文件里绝不出现 base64"。
//
// 后者是本设计的硬约束：Task 要整体落盘并在 task_updated 里全量下发，
// 把 base64 写进去会让单任务文件膨胀到几十 MB（见 model.TaskEvent.Images 的说明）。

// imgB64 一段合法 base64（解码出 5 字节 "pieqi"）。不解析内容，够用即可。
const imgB64 = "cGllcWk="

func imgInputs(n int) []agent.ImageInput {
	out := make([]agent.ImageInput, n)
	for i := range out {
		out[i] = agent.ImageInput{Data: imgB64, MimeType: "image/png"}
	}
	return out
}

// mustJSON 把任务序列化成 JSON（断言"任务文件里没有 base64"用）。
func mustJSON(t *testing.T, tsk *model.Task) string {
	t.Helper()
	b, err := json.Marshal(tsk)
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	return string(b)
}

// waitClaudePathDone 等非 ACP 路径的一轮跑完（任务落终态）。
// 与 waitRunACPDone 同思路，但那边依赖 fake runner，这里没有 —— 直接看终态。
func waitClaudePathDone(t *testing.T, store *TaskStore, taskID string) {
	t.Helper()
	waitFor(t, 2*time.Second, "claude path turn finish (terminal status)", func() bool {
		tt, ok := store.Get(taskID)
		if !ok || tt == nil {
			return false
		}
		return tt.Status == model.TaskCompleted || tt.Status == model.TaskFailed ||
			tt.Status == model.TaskCancelled
	})
}

// TestStartRich_ImagesReachAdapter 验证首轮的图真的到了 adapter。
//
// 判据是"adapter 收到了几张"，而不是"没报错" —— 后者在静默丢图的实现下同样成立，
// 而静默丢图正是本设计要防的事。
func TestStartRich_ImagesReachAdapter(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{}, false)
	task := createACPTestTask(t, store)

	tr.StartRich(t.Context(), task, imgInputs(2))
	a := waitACPAdapter(t, fake, task.ID)
	waitRunACPDone(t, store, task.ID)

	if got := a.imagesReceived(); got != 2 {
		t.Fatalf("adapter received %d images, want 2", got)
	}
}

// createACPTestTaskWithUserEvent 建一个带首条 user 事件的 task，镜像**生产**的建任务路径。
//
// 为什么要带 user 事件：生产里 API 的 createTask 先预置一条 user 事件再交给 runner
// （见 api/tasks.go），图片元数据就落在那条上。测试若省掉它，就测不到"元数据有没有
// 记在用户消息上"这件事 —— 而那正是前端显示"这条带了图"的依据。
func createACPTestTaskWithUserEvent(t *testing.T, store *TaskStore, text string) *model.Task {
	t.Helper()
	task := createACPTestTask(t, store)
	if _, err := store.Update(task.ID, func(t *model.Task) bool {
		t.Prompt = text
		store.AppendEvent(t, model.TaskEvent{Type: model.EventUser, Text: text})
		return true
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(task.ID)
	return got
}

// TestStartRich_ImagesNotInTaskFile 验证图片**本体**绝不写进任务，但元数据要落。
//
// 这是本设计最重要的一条负向断言：只要 base64 进了 Task，任务文件与 WS 推送
// 都会被它撑爆。断言方式是把整个任务序列化后搜 base64 子串。
func TestStartRich_ImagesNotInTaskFile(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{}, false)
	task := createACPTestTaskWithUserEvent(t, store, "看看这张图")

	tr.StartRich(t.Context(), task, imgInputs(1))
	waitACPAdapter(t, fake, task.ID)
	waitRunACPDone(t, store, task.ID)

	got, ok := store.Get(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	// 落盘的那份 JSON 里不得出现图片数据。
	if raw := mustJSON(t, got); strings.Contains(raw, imgB64) {
		t.Fatal("task JSON contains image base64; images must never be persisted")
	}
	// 但元数据要在（前端要据此显示"这条带了图"）。
	var found bool
	for _, ev := range got.Events {
		if ev.Type == model.EventUser && len(ev.Images) > 0 {
			found = true
			if ev.Images[0].MimeType != "image/png" {
				t.Errorf("image mime=%q want image/png", ev.Images[0].MimeType)
			}
			if ev.Images[0].Bytes != 5 {
				t.Errorf("image bytes=%d want 5 (decoded length)", ev.Images[0].Bytes)
			}
			if ev.Images[0].Hash == "" {
				t.Error("image hash is empty; want a content hash for traceability")
			}
		}
	}
	if !found {
		t.Error("no user event carries image metadata; UI cannot show that images were attached")
	}
}

// TestTakePendingImages_ConsumedOnce 验证待发图片**取后即清**。
//
// 判据：残留会让下一轮（续问）莫名其妙又发一遍同样的图 —— 而用户这轮压根没传图。
func TestTakePendingImages_ConsumedOnce(t *testing.T) {
	tr, store, _, _ := newACPTestRunner(t, fakeScript{}, false)
	task := createACPTestTask(t, store)

	tr.setPendingImages(task.ID, imgInputs(3))
	if got := tr.takePendingImages(task.ID); len(got) != 3 {
		t.Fatalf("first take = %d images, want 3", len(got))
	}
	if got := tr.takePendingImages(task.ID); len(got) != 0 {
		t.Fatalf("second take = %d images, want 0 (must be consumed once)", len(got))
	}
}

// TestResumeRich_RejectsImagesOnClaudePath 验证 claude -p 路径**明确拒绝**图片。
//
// 关键点：必须在写 user 事件**之前**拒绝。否则任务里会留下一条"带了图"的记录，
// 而图根本没发出去 —— 那条记录就成了假事实。
func TestResumeRich_RejectsImagesOnClaudePath(t *testing.T) {
	store, _ := NewTaskStore(t.TempDir())
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	bus := NewEventBus()
	hooks := NewHookService(5 * time.Second)
	tr := NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")
	// 不注入 agentMgr：走 claude -p 路径。

	wt := t.TempDir()
	task, err := store.Create(&model.Task{
		ProjectID: "p", ProjectPath: wt, WorktreePath: wt,
		Prompt: "hi", ClaudeSessionID: "sess-x",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Create 一律把状态置成 pending（见 task_store.go），故终态要在这之后设 ——
	// 否则本用例会先撞上 Resumable 守卫，测不到"图片被拒"这条。
	if _, err := store.Update(task.ID, func(t *model.Task) bool {
		t.Status = model.TaskCompleted
		return true
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Get(task.ID)

	err = tr.ResumeRich(task.ID, "看图", "", imgInputs(1))
	if err == nil {
		t.Fatal("ResumeRich with images on claude path = nil error, want rejection")
	}
	if !strings.Contains(err.Error(), "不支持图片") {
		t.Errorf("err=%q should say images are unsupported on this path", err)
	}

	got, _ := store.Get(task.ID)
	if len(got.Events) != len(before.Events) {
		t.Fatalf("events grew from %d to %d; a rejected request must not leave a user event "+
			"claiming images were sent", len(before.Events), len(got.Events))
	}
}

// TestRunClaudePath_RejectsImages 验证首轮在 claude -p 路径带图时任务明确失败。
//
// 与续问同一口径：不静默丢图，而是让任务以可读的原因失败。
func TestRunClaudePath_RejectsImages(t *testing.T) {
	store, _ := NewTaskStore(t.TempDir())
	wm := NewWorktreeManager(zap.NewNop(), t.TempDir())
	bus := NewEventBus()
	hooks := NewHookService(5 * time.Second)
	tr := NewTaskRunner(zap.NewNop(), store, wm, bus, hooks, "", "bypassPermissions", false, "", 0, nil, 0, 0, "main")

	wt := t.TempDir()
	task, err := store.Create(&model.Task{
		ProjectID: "p", ProjectPath: wt, WorktreePath: wt, Prompt: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	tr.StartRich(t.Context(), task, imgInputs(1))
	waitClaudePathDone(t, store, task.ID)

	got, _ := store.Get(task.ID)
	if got.Status != model.TaskFailed {
		t.Fatalf("status=%s want failed", got.Status)
	}
	if !strings.Contains(got.Error, "不支持图片") {
		t.Errorf("error=%q should explain images are unsupported", got.Error)
	}
}

// TestResumeRich_ImagesReachAdapter 验证续问路径的图也能到 adapter。
func TestResumeRich_ImagesReachAdapter(t *testing.T) {
	tr, store, _, fake := newACPTestRunner(t, fakeScript{}, false)
	task := createACPTestTask(t, store)

	// 先跑一轮把会话立起来（图片走续问，不走首轮）。
	tr.Start(t.Context(), task)
	waitACPAdapter(t, fake, task.ID)
	waitRunACPDone(t, store, task.ID)

	if err := tr.ResumeRich(task.ID, "再看这张", "", imgInputs(1)); err != nil {
		t.Fatalf("ResumeRich: %v", err)
	}
	waitFor(t, 2*time.Second, "second turn reaches adapter", func() bool {
		return fake.lastImages() == 1
	})
}

// TestImageMeta_ComputesHashAndSize 直接钉住元数据计算。
func TestImageMeta_ComputesHashAndSize(t *testing.T) {
	meta := ImageMeta(imgInputs(1))
	if len(meta) != 1 {
		t.Fatalf("got %d entries, want 1", len(meta))
	}
	if meta[0].Bytes != 5 {
		t.Errorf("Bytes=%d want 5", meta[0].Bytes)
	}
	if meta[0].Hash == "" {
		t.Fatal("Hash empty")
	}
	// 64 个 hex 字符 = SHA-256。钉住形状是为了确保算的是**解码后**内容
	// 而不是 base64 字符串本身（算错对象的话，同一张图换种编码就变成"另一张"）。
	if len(meta[0].Hash) != 64 {
		t.Errorf("Hash=%q len=%d want 64 hex chars", meta[0].Hash, len(meta[0].Hash))
	}

	// 同一内容两次算出的哈希必须一致（否则"同一张图"无法判定）。
	if meta2 := ImageMeta(imgInputs(1)); meta2[0].Hash != meta[0].Hash {
		t.Error("hash is not deterministic for identical content")
	}
	// 不同内容必须不同。
	other := []agent.ImageInput{{Data: base64.StdEncoding.EncodeToString([]byte("x")), MimeType: "image/png"}}
	if got := ImageMeta(other); got[0].Hash == meta[0].Hash {
		t.Error("different content produced the same hash")
	}
}

func TestImageMeta_EmptyReturnsNil(t *testing.T) {
	if got := ImageMeta(nil); got != nil {
		t.Errorf("ImageMeta(nil)=%+v want nil (JSON omitempty must elide it)", got)
	}
}
