// model_catalog_cache_test.go：模型清单的两级缓存与单飞。
//
// 守的是什么（2026-10-22 实测）：清单只能靠**起一个真会话**问出来，冷探测 5.7~7.0s。
// 原来的缓存只活在进程内存里，于是每次重启（发版/自重启/崩溃恢复，本项目常态）
// 第一个打开会话页的人都要吃这 7s —— 这就是"model 列表变慢了"的实际来源。
// 这里把三条落点钉住：
//
//	① 内存命中 → 不再探测；
//	② 进程重启（内存清空）→ 磁盘缓存接住，仍不探测；
//	③ 同一 agent 的并发调用 → 只探测一次（单飞），否则快速切 agent 会同时起多个进程。
package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withFakeCatalog 把探测函数与缓存目录换成测试替身，返回探测计数器。
// 用 t.Cleanup 复原全局状态，避免污染同包其它用例。
func withFakeCatalog(t *testing.T, cat ModelCatalog, err error) *int32 {
	t.Helper()
	var calls int32
	probeModelsFn = func(context.Context, string) (ModelCatalog, error) {
		atomic.AddInt32(&calls, 1)
		cp := cat
		cp.Options = append([]ModelOption(nil), cat.Options...)
		return cp, err
	}
	dir := t.TempDir()
	SetModelCatalogDir(dir)
	resetCatalogCache()
	t.Cleanup(func() {
		probeModelsFn = probeAgentModels
		catalogDirFn = defaultCatalogDir
		resetCatalogCache()
	})
	return &calls
}

func resetCatalogCache() {
	modelCatalogMu.Lock()
	modelCatalogCache = map[string]modelCatalogEntry{}
	modelCatalogProbing = map[string]*catalogProbeWait{}
	modelCatalogMu.Unlock()
}

func sampleCatalog() ModelCatalog {
	return ModelCatalog{
		Agent:   "qoder",
		Current: "qfmodel",
		Options: []ModelOption{
			{Value: "qmodel_38max", Name: "Qwen3.8-Max", Description: "0.50x Credit"},
			{Value: "qfmodel", Name: "Qwen3.8-Flash"},
		},
	}
}

// TestModelCatalog_MemoryHitSkipsProbe 同一进程内的第二次调用不再起进程。
func TestModelCatalog_MemoryHitSkipsProbe(t *testing.T) {
	calls := withFakeCatalog(t, sampleCatalog(), nil)

	if _, err := ListAgentModels(context.Background(), "qoder"); err != nil {
		t.Fatalf("首次探测: %v", err)
	}
	cat, err := ListAgentModels(context.Background(), "qoder")
	if err != nil {
		t.Fatalf("第二次: %v", err)
	}
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("探测 %d 次，期望 1 次（内存缓存未生效 = 每次打开会话页都要等几秒）", n)
	}
	if len(cat.Options) != 2 || cat.Current != "qfmodel" {
		t.Fatalf("缓存内容不对: %+v", cat)
	}
}

// TestModelCatalog_SurvivesRestartViaDisk 重启（内存清空）后仍从磁盘档返回清单。
//
// 这是本次修复的主用例：只清内存、不清磁盘目录，模拟进程重启。
func TestModelCatalog_SurvivesRestartViaDisk(t *testing.T) {
	calls := withFakeCatalog(t, sampleCatalog(), nil)

	if _, err := ListAgentModels(context.Background(), "qoder"); err != nil {
		t.Fatalf("首次探测: %v", err)
	}
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("探测 %d 次，期望 1 次", n)
	}

	// 模拟重启：内存全清，磁盘（t.TempDir）保留
	resetCatalogCache()

	cat, err := ListAgentModels(context.Background(), "qoder")
	if err != nil {
		t.Fatalf("重启后读取: %v", err)
	}
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("重启后又探测了（共 %d 次）—— 磁盘缓存没接住，用户仍要等几秒", n)
	}
	if len(cat.Options) != 2 || cat.Options[1].Name != "Qwen3.8-Flash" {
		t.Fatalf("磁盘档内容不对: %+v", cat)
	}
}

// TestModelCatalog_InvalidateDropsBothLayers 失效必须同时清内存与磁盘，
// 否则"建会话失败后摘缓存"只在本进程有效，重启又把坏清单捡回来。
func TestModelCatalog_InvalidateDropsBothLayers(t *testing.T) {
	calls := withFakeCatalog(t, sampleCatalog(), nil)

	if _, err := ListAgentModels(context.Background(), "qoder"); err != nil {
		t.Fatalf("首次探测: %v", err)
	}
	InvalidateAgentModels("qoder")
	resetCatalogCache() // 再清内存：验证磁盘也被删了，而不是被内存掩盖

	if _, err := ListAgentModels(context.Background(), "qoder"); err != nil {
		t.Fatalf("失效后重探: %v", err)
	}
	if n := atomic.LoadInt32(calls); n != 2 {
		t.Fatalf("探测 %d 次，期望 2 次（失效后应重新探测）", n)
	}
}

// TestModelCatalog_FailureNotPersistedToDisk 失败结果**不写磁盘**。
//
// 判据：失败是**本机此刻的状态**（进程起不来、未登录），不是 agent 的事实。
// 若落盘，一次偶发失败会在重启后继续冒充"这个 agent 没有模型"，
// 且失败档 TTL 短、用户修好配置却仍看到旧错误。
func TestModelCatalog_FailureNotPersistedToDisk(t *testing.T) {
	calls := withFakeCatalog(t, ModelCatalog{Agent: "qoder"}, errors.New("boom"))

	if _, err := ListAgentModels(context.Background(), "qoder"); err == nil {
		t.Fatal("期望返回错误")
	}
	resetCatalogCache() // 模拟重启：内存档已失效，磁盘档若存在就会命中

	if _, err := ListAgentModels(context.Background(), "qoder"); err == nil {
		t.Fatal("重启后仍返回旧错误 —— 失败结果不该落盘")
	}
	if n := atomic.LoadInt32(calls); n != 2 {
		t.Fatalf("探测 %d 次，期望 2 次（失败不该从磁盘复用）", n)
	}
}

// TestModelCatalog_ConcurrentCallsProbeOnce 单飞：并发调用只起一个进程。
//
// 真实触发路径：会话页 watch 到 agent 就调一次，新建任务页切换 agent 又调一次，
// 两者几乎同时 —— 没有单飞就是两个 agent 进程各等 5~7s。
func TestModelCatalog_ConcurrentCallsProbeOnce(t *testing.T) {
	var calls int32
	release := make(chan struct{})
	probeModelsFn = func(context.Context, string) (ModelCatalog, error) {
		atomic.AddInt32(&calls, 1)
		<-release // 卡住探测，让所有并发调用都排进等待队列
		return sampleCatalog(), nil
	}
	SetModelCatalogDir(t.TempDir())
	resetCatalogCache()
	t.Cleanup(func() {
		probeModelsFn = probeAgentModels
		catalogDirFn = defaultCatalogDir
		resetCatalogCache()
	})

	const n = 8
	var wg sync.WaitGroup
	results := make([]ModelCatalog, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			cat, _ := ListAgentModels(context.Background(), "qoder")
			results[i] = cat
		}(i)
	}
	close(start)
	// 给所有 goroutine 时间进入等待，再放行探测
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if c := atomic.LoadInt32(&calls); c != 1 {
		t.Fatalf("并发 %d 次调用触发了 %d 次探测，期望恰好 1 次（单飞失效 = 同时起多个 agent 进程）", n, c)
	}
	for i, r := range results {
		if len(r.Options) != 2 {
			t.Fatalf("第 %d 个调用方拿到空结果（单飞等待方没有拿到结果）: %+v", i, r)
		}
	}
}

// TestModelCatalog_DiskEntryRejectsForeignAgentType agent_type 变了（换了实现/可执行文件）
// 时磁盘档必须作废：清单形态是 agent 实现决定的，不是名字决定的。
func TestModelCatalog_DiskEntryRejectsForeignAgentType(t *testing.T) {
	calls := withFakeCatalog(t, sampleCatalog(), nil)

	if _, err := ListAgentModels(context.Background(), "qoder"); err != nil {
		t.Fatalf("首次探测: %v", err)
	}
	// 手写一份 agent_type 对不上的磁盘档
	path := catalogFile("qoder")
	if path == "" {
		t.Fatal("缓存目录未注入，磁盘档路径为空")
	}
	writeCatalogFileForTest(t, path, catalogCacheFile{
		Agent: "qoder", ProbedAt: time.Now(), AgentType: "__a_different_agent__",
		Options: []ModelOption{{Value: "stale", Name: "Stale"}},
	})
	resetCatalogCache()

	cat, _ := ListAgentModels(context.Background(), "qoder")
	if n := atomic.LoadInt32(calls); n != 2 {
		t.Fatalf("探测 %d 次，期望 2 次（agent_type 不匹配的磁盘档必须作废）", n)
	}
	if len(cat.Options) != 2 || cat.Options[0].Value == "stale" {
		t.Fatalf("用了错的磁盘档: %+v", cat)
	}
}
