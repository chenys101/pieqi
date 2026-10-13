package main

import (
	"net"
	"strconv"
	"testing"
	"time"

	"go.uber.org/zap"
)

// 本文件守护 listenWithRetry —— 直接对应 2026-10-13 的线上事故：
// 自重启后新实例 EADDRINUSE 直接 FATAL，而旧实例已经退出 ⇒ 服务彻底消失。
//
// 关键点：这个测试必须**真的占住端口再释放**，而不是 mock 掉 net.Listen ——
// 事故的本质就是"旧进程的 LISTEN 还没释放"，只有真 socket 才能复现它。

// freePort 拿一个当前空闲的端口号。
//
// 注意：拿到号之后会立刻关掉 listener，所以它是"刚刚还空闲"而非"保证仍空闲"——
// 对本文件足够：我们随后会自己把它占住。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// TestListenWithRetry_RecoversAfterPortReleased 复刻自重启竞态：
// bind 时端口被占（旧实例还没走干净），几百毫秒后释放 ⇒ 必须**成功**而不是 FATAL。
//
// 这正是修复要买的东西：把"服务消失"降级成"重启慢几百毫秒"。
func TestListenWithRetry_RecoversAfterPortReleased(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)

	// 模拟"旧实例仍持有端口"。
	blocker, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}

	// 200ms 后释放 —— 模拟旧实例退出、LISTEN 释放（实测故障窗口 ~150ms）。
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = blocker.Close()
	}()

	start := time.Now()
	ln, err := listenWithRetry(addr, 5*time.Second, zap.NewNop())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("端口释放后应重试成功，却返回 %v —— 这正是事故现场："+
			"新实例 FATAL 退出而旧实例已走，服务彻底消失", err)
	}
	defer ln.Close()

	if elapsed < 150*time.Millisecond {
		t.Errorf("耗时 %v 过短，疑似没有真的等到端口释放", elapsed)
	}
	t.Logf("端口释放后 %.0fms 绑定成功（修复生效）", elapsed.Seconds()*1000)

	// 拿到的 listener 必须是可用的。
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("返回的 listener 不可连：%v", err)
	}
	_ = c.Close()
}

// TestListenWithRetry_SucceedsImmediatelyWhenFree 无竞争时不应引入延迟。
// 正常启动路径（绝大多数情况）必须和 net.Listen 一样快。
func TestListenWithRetry_SucceedsImmediatelyWhenFree(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)

	start := time.Now()
	ln, err := listenWithRetry(addr, 5*time.Second, zap.NewNop())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("端口空闲时应立即成功：%v", err)
	}
	defer ln.Close()

	// 首次就成功 ⇒ 不该有任何 sleep。
	if elapsed > 50*time.Millisecond {
		t.Errorf("端口空闲却耗时 %v，重试逻辑引入了不必要的延迟", elapsed)
	}
}

// TestListenWithRetry_GivesUpWhenPortStaysBusy 反例守护：
// 端口**真被长期占用**时（配置错、别的程序占着）必须仍然失败，
// 不能变成无限等待 —— 否则服务会静默挂起，比 FATAL 更难排查。
func TestListenWithRetry_GivesUpWhenPortStaysBusy(t *testing.T) {
	port := freePort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)

	blocker, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer blocker.Close() // 全程占着，不释放

	start := time.Now()
	ln, err := listenWithRetry(addr, 600*time.Millisecond, zap.NewNop())
	elapsed := time.Since(start)

	if err == nil {
		ln.Close()
		t.Fatal("端口被长期占用时不应成功")
	}
	// 必须在上限附近放弃，而不是无限等。
	if elapsed > 3*time.Second {
		t.Errorf("耗时 %v，超出上限过多 —— 退避逻辑可能失控", elapsed)
	}
	if elapsed < 500*time.Millisecond {
		t.Errorf("耗时 %v，早于设定的上限就放弃了", elapsed)
	}
}
