package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// "重启（必要时换版）"决定层的回归（2026-10-21）。
//
// 判据只有一条：**有没有新编译产物，决定走哪条路**。
//   - 有 → 换版并重启（restartWith 拿到"有暂存"的信号）；
//   - 没有 → 只重启（restartWith 拿到空串，即"不换"）。
//
// 这里把重启编排注入成假函数，所以不会真的起进程 —— 这是本文件能安全
// 在 go test 里跑的前提（真 spawn 见 core/selfrestart_test.go 的隔离说明）。

func TestReloadRestart_SwapsWhenStaged(t *testing.T) {
	var gotPath []string
	swapped, served := reloadRestart(1, func(staged string) error {
		gotPath = append(gotPath, staged)
		return nil
	}, func() bool { return true }, func(string, ...any) {})

	if !swapped {
		t.Fatal("有暂存产物时应走换版路径（swapped=true）")
	}
	if len(gotPath) != 1 || gotPath[0] != "" {
		// 空串是"用约定落点"的既有语义（见 SelfRestart.Restart）。
		t.Fatalf("restartWith 收到 %v，want [\"\"]（约定落点）", gotPath)
	}
	if served {
		t.Fatalf("端口 1 上不该探到服务，served 应为 false")
	}
}

// TestReloadRestart_RestartOnlyWhenNotStaged 没有产物时**只重启**。
func TestReloadRestart_RestartOnlyWhenNotStaged(t *testing.T) {
	called := 0
	swapped, _ := reloadRestart(1, func(staged string) error {
		called++
		if staged != "" {
			t.Fatalf("无产物时 restartWith 收到 staged=%q，want 空串（不得换版）", staged)
		}
		return nil
	}, func() bool { return false }, func(string, ...any) {})

	if swapped {
		t.Fatal("无暂存产物时 swapped 必须为 false")
	}
	if called != 1 {
		t.Fatalf("重启编排被调用 %d 次，want 1", called)
	}
}

// TestReloadRestart_FailureKeepsServing 重启编排报错 ⇒ swapped=false，且不 panic。
//
// 语义：失败意味着旧进程仍在服务（core 侧就是这么设计的），调用方不需要做补救。
func TestReloadRestart_FailureKeepsServing(t *testing.T) {
	var logs []string
	swapped, served := reloadRestart(1, func(string) error {
		return errors.New("boom")
	}, func() bool { return true }, func(format string, args ...any) {
		logs = append(logs, format)
	})
	if swapped {
		t.Fatal("编排失败时 swapped 必须为 false")
	}
	if !served {
		t.Fatal("编排失败时 served 应为 true（旧进程仍在服务，不是服务消失）")
	}
	if len(logs) == 0 {
		t.Fatal("失败必须留痕，不能静默")
	}
}

// TestReloadRestart_NotWired 未接线：明确失败，不假装重启。
func TestReloadRestart_NotWired(t *testing.T) {
	swapped, served := reloadRestart(1, nil, func() bool { return true }, func(string, ...any) {})
	if swapped || !served {
		t.Fatalf("未接线时 got swapped=%v served=%v, want false/true", swapped, served)
	}
}

// TestReloadSummary_TellsModeApart 响应体必须能区分"换了版"与"只重启"——
// 这是调用方唯一能拿到确定答案的地方（两边日志都是几行 info）。
func TestReloadSummary_TellsModeApart(t *testing.T) {
	a := reloadSummary(adminReloadRequest{Port: 3000}, true, true)
	if a["mode"] != "swap-and-restart" {
		t.Fatalf("swap 模式=%v", a["mode"])
	}
	b := reloadSummary(adminReloadRequest{Port: 3000}, false, true)
	if b["mode"] != "restart-only" {
		t.Fatalf("restart-only 模式=%v", b["mode"])
	}
}

// TestWaitServing_SeesReady 探活只看 /ready 是否为 200。
func TestWaitServing_SeesReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	port := portOf(t, srv.URL)
	if !waitServing(port, 10_000_000_000) { // 10s
		t.Fatal("waitServing 未能在 /ready 200 时确认服务就绪")
	}
}

// TestWaitServing_TimesOutOnNotFound /ready 回 404 时不能算就绪。
func TestWaitServing_TimesOutOnNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	port := portOf(t, srv.URL)
	if waitServing(port, 3_000_000_000) { // 3s（函数内部先睡 1.5s）
		t.Fatal("/ready 404 不应被判为就绪")
	}
}

// TestEnvInt 只接受纯数字。
func TestEnvInt(t *testing.T) {
	if got := envInt("PIEQI_TEST_NOT_SET_ENV", 7); got != 7 {
		t.Fatalf("未设置时 got %d, want 7", got)
	}
	t.Setenv("PIEQI_TEST_PORT", "3100")
	if got := envInt("PIEQI_TEST_PORT", 7); got != 3100 {
		t.Fatalf("设置时 got %d, want 3100", got)
	}
	t.Setenv("PIEQI_TEST_PORT", "abc")
	if got := envInt("PIEQI_TEST_PORT", 7); got != 7 {
		t.Fatalf("非法值 got %d, want 7", got)
	}
}

// portOf 从 httptest 的 URL 里取端口。
func portOf(t *testing.T, raw string) int {
	t.Helper()
	i := strings.LastIndex(raw, ":")
	if i < 0 {
		t.Fatalf("unexpected url %q", raw)
	}
	p, err := strconv.Atoi(raw[i+1:])
	if err != nil {
		t.Fatalf("port from %q: %v", raw, err)
	}
	return p
}
