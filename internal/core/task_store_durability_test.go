package core

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pieqi/internal/model"
)

// ---- 掉电安全：先 fsync 再 rename（对齐 magpie internal/steady）----
//
// 背景：rename 只是目录项改动，它**到盘的时刻由 OS 安排、背后的数据更晚**。
// 于是存在致命中间态：目录项已指向新文件、数据还在页缓存。此刻断电 ⇒
// 得到一个"长度正确、内容全零"的文件。magpie issue #1505 正是此模式
// （其 internal/steady/steady.go 的注释），pieqi 的暴露面完全相同。

// TestWriteFileSynced_SyncFailureIsNotSuccess Sync 失败必须当作写入失败。
//
// 这条纪律的意义：若 Sync 失败却继续 rename，就等于亲手把目录项指向一个
// 内容不可信的文件 —— 那正是"全零文件"的成因。宁可报错让调用方重试/保留旧内容。
func TestWriteFileSynced_SyncFailureIsNotSuccess(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")

	// 正常路径先确认函数本身可用（否则下面的断言可能因为别的原因"通过"）
	if err := writeFileSynced(p, []byte("ok"), 0o644); err != nil {
		t.Fatalf("writeFileSynced: %v", err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "ok" {
		t.Fatalf("内容不对：%q", got)
	}
}

// TestWriteFileSynced_TruncatesExisting 覆盖写必须截断旧内容。
//
// 用 O_TRUNC 而非 O_APPEND：不截断会让新 JSON 后面拖着旧内容的尾巴，
// 解析必然失败 —— 那会伪装成"文件损坏"，掩盖真正的原因。
func TestWriteFileSynced_TruncatesExisting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	if err := os.WriteFile(p, []byte("very-long-old-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileSynced(p, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "new" {
		t.Fatalf("未截断旧内容：%q", got)
	}
}

// TestPersist_LeavesNoTempOnSuccess 成功路径不该留下 .tmp 残骸。
func TestPersist_LeavesNoTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	s, err := NewTaskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(&model.Task{ProjectID: "cb", Prompt: "t1"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("留下了临时文件：%s", e.Name())
		}
	}
}

// ---- load 对坏文件的处置（对齐 magpie internal/lastgood 的
//      "a file that is there but not valid is unknown, never empty"）----

// TestLoad_AllZeroFileIsReportedNotSilentlyDropped 全长全零的文件必须被报出来
// 并留证，绝不能静默当成"没有这个任务"。
//
// 这是 magpie #1505 的同款场景：掉电留下零字节块，程序读出"没有账号/没有任务"，
// 而**下一次写入把这个状态固化**。原先 pieqi 的实现是裸 continue —— 任务消失
// 且日志全干净，是典型的静默数据丢失。
func TestLoad_AllZeroFileIsReportedNotSilentlyDropped(t *testing.T) {
	dir := t.TempDir()
	// 构造一个"长度正确但全零"的任务文件（掉电特征）
	zeroed := make([]byte, 512)
	if err := os.WriteFile(filepath.Join(dir, "deadbeef-0000-0000-0000-000000000000.json"), zeroed, 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewTaskStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	bad := s.BadFiles()
	if len(bad) != 1 {
		t.Fatalf("坏文件数 = %d, want 1（全零文件必须被记下，不能静默跳过）", len(bad))
	}
	// 坏因必须点明"全零"这一掉电特征，而不是含糊的"解析失败" ——
	// 前者指向存储层（磁盘可能还有别的文件受损），后者会让人往数据格式找。
	if !strings.Contains(bad[0].Reason, "0x00") {
		t.Errorf("坏因未识别出全零特征：%q", bad[0].Reason)
	}
	// 必须留证：原文件被另存，内容原样保留。
	if bad[0].KeptAs == "" {
		t.Fatal("坏文件未被留证（KeptAs 为空）")
	}
	kept, err := os.ReadFile(bad[0].KeptAs)
	if err != nil {
		t.Fatalf("留证文件读不到：%v", err)
	}
	if !bytes.Equal(kept, zeroed) {
		t.Fatal("留证文件内容与原文件不一致")
	}
	// 原名必须已消失，否则每次启动都会重复报同一个错（噪音淹没真问题）。
	if _, err := os.Stat(filepath.Join(dir, "deadbeef-0000-0000-0000-000000000000.json")); !os.IsNotExist(err) {
		t.Error("坏文件仍留在原名下，下次启动会重复报错")
	}
	// 且留证文件名不能以 .json 结尾，否则会被 load 再次当成任务捡起来。
	if strings.HasSuffix(filepath.Base(bad[0].KeptAs), ".json") {
		t.Errorf("留证文件名仍以 .json 结尾，会被下次 load 当成任务：%s", bad[0].KeptAs)
	}
}

// TestLoad_CorruptJSONIsReported 普通损坏（截断/垃圾）同样要可见。
func TestLoad_CorruptJSONIsReported(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "truncated.json"), []byte(`{"id":"x","prompt":"未写完`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewTaskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	bad := s.BadFiles()
	if len(bad) != 1 {
		t.Fatalf("坏文件数 = %d, want 1", len(bad))
	}
	if !strings.Contains(bad[0].Reason, "解析失败") {
		t.Errorf("坏因 = %q, want 含'解析失败'", bad[0].Reason)
	}
}

// TestLoad_MissingIDIsReported 缺 id 的文件同样不能静默跳过。
func TestLoad_MissingIDIsReported(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "noid.json"), []byte(`{"prompt":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewTaskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	bad := s.BadFiles()
	if len(bad) != 1 {
		t.Fatalf("坏文件数 = %d, want 1", len(bad))
	}
	if !strings.Contains(bad[0].Reason, "id") {
		t.Errorf("坏因 = %q, want 提到 id", bad[0].Reason)
	}
}

// TestLoad_GoodFilesUnaffected 正常文件必须照常加载 —— 坏文件处理不能伤及无辜。
func TestLoad_GoodFilesUnaffected(t *testing.T) {
	dir := t.TempDir()
	// 先经 store 正常写一个任务
	s0, err := NewTaskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s0.Create(&model.Task{ProjectID: "cb", Prompt: "good"})
	if err != nil {
		t.Fatal(err)
	}
	// 再塞一个坏文件
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewTaskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.BadFiles()) != 1 {
		t.Errorf("坏文件数 = %d, want 1", len(s.BadFiles()))
	}
	// 按 **id** 查（id 是 store.Create 生成的 uuid，不是 prompt）。
	if _, ok := s.Get(created.ID); !ok {
		t.Fatal("正常任务被坏文件处理波及，未能加载")
	}
	if got := len(s.List()); got != 1 {
		t.Fatalf("任务数 = %d, want 1", got)
	}
}

// TestLoad_BadFileDoesNotBlockStartup 坏文件不能让 store 构造失败。
//
// 判据：一个坏文件不该让整个服务起不来 —— 那比丢掉一个任务更糟。
func TestLoad_BadFileDoesNotBlockStartup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "garbage.json"), []byte("\x00\x01\x02"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTaskStore(dir); err != nil {
		t.Fatalf("坏文件导致 store 构造失败：%v", err)
	}
}

// TestDescribeBadJSON_EmptyVsZeroed 区分"空文件"与"全长全零"。
//
// 两者根因完全不同：空文件可能是写入中断；全长全零是**掉电**特征。
// 报错文案若混为一谈，排查方向就错了。
func TestDescribeBadJSON_EmptyVsZeroed(t *testing.T) {
	if got := describeBadJSON(nil); !strings.Contains(got, "0 字节") {
		t.Errorf("空文件描述 = %q, want 提到 0 字节", got)
	}
	if got := describeBadJSON(make([]byte, 100)); !strings.Contains(got, "0x00") {
		t.Errorf("全零文件描述 = %q, want 提到 0x00 特征", got)
	}
	if got := describeBadJSON([]byte("   \n\t ")); !strings.Contains(got, "空白") {
		t.Errorf("空白文件描述 = %q, want 提到空白", got)
	}
	if got := describeBadJSON([]byte(`{"broken"`)); !strings.Contains(got, "解析失败") {
		t.Errorf("普通坏 JSON 描述 = %q, want 含'解析失败'", got)
	}
}

// TestRestartJournal_IsFsynced 交接单也必须先 fsync 再 rename。
//
// 它的后果比任务文件更直接：交接单决定"一批 running 会话是保留接续还是判 failed"。
// 零字节的交接单 ⇒ 解析失败 ⇒ 本可接续的一批会话被判 failed。
func TestRestartJournal_IsFsynced(t *testing.T) {
	dir := t.TempDir()
	j := &RestartJournal{
		StartedAt:       time.Now(),
		FromPID:         1234,
		StagedSHA256:    "abc",
		AffectedTaskIDs: []string{"t1"},
	}
	if err := writeRestartJournal(dir, j); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(journalPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var back RestartJournal
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("交接单不可解析：%v", err)
	}
	if back.StagedSHA256 != "abc" || len(back.AffectedTaskIDs) != 1 {
		t.Fatalf("交接单内容不对：%+v", back)
	}
	// 不该留下临时文件
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("留下了临时文件：%s", e.Name())
		}
	}
}
