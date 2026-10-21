package main

// 「重启」的两种语义（2026-10-21，用户要求）：
//
//	有新编译产物 → 替换二进制并重新启动（原 POST /api/admin/restart 的语义）
//	没有新产物   → **只重启**，二进制一个字节都不动
//
// 为什么必须分成两条（而不是"没产物就报 409 让用户自己想"）：
//
//   - 旧的 POST /api/admin/restart 在 `staged=false` 时回 409 "no staged binary"。
//     那条语义对"我要上线新版本"是对的（防止一次误触变成无谓重启），但对
//     "服务卡住了，请重启一下"是死路：调用方（面板按钮、飞书、agent 自己）
//     拿不到重启能力，只能去点 build/restart.bat。
//   - 而 build/restart.bat 自己也没能做到这件事：它在没有 pieqi.new.exe 时
//     直接报错退出（"nothing to install"），于是"只重启"在任何入口都做不了。
//
// 所以这里补的是**决定层**：有没有暂存产物，由服务在收到的当下判定一次，
// 然后把结论交给 selfRestart 编排。编排侧（core.SelfRestart）保证：
//   - swap=true 时走"改名备份 → 装上 staged → 拉起新进程 → 让出端口"；
//   - swap=false 时**不碰任何文件**，只做"拉起新进程 → 让出端口"。
//
// 为什么 swap=false 也要"让出端口再等新实例"（而不是原地重连）：
// 这正是 2026-10-13 死锁事故的修法 —— 旧实例自己监听着端口时，"新实例起来了"
// 无法用"端口通不通"判断（同义反复）。所以两条路径共用同一套让位 + 就绪信号
// 编排，区别只在 Swap 之前那一步做不做替换。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// portProbeTimeout 是"重启后探活"的单次等待上限。
//
// 取值依据：新实例要在 bindRetryTimeout(10s) 内完成 bind，再加上
// restartReadyTimeout(25s) 的就绪窗口 —— 给它 30s，足够覆盖最坏情况，
// 又不会让一次失败的重启把调用方挂太久。探活失败**不改变结论**：
// 重启已经受理并触发，探测结果只是"顺带告诉你要不要回头看一眼"。
const portProbeTimeout = 30 * time.Second

// reloadRestart 执行"重启（必要时换版）"，并在返回后触发进程更替。
//
// 调用点：POST /api/admin/reload（见 main.go 的接线）。该 handler 已经先回了
// 202，所以本函数里的等待不会拖住调用方。
//
// 返回的两个 bool：
//   - swapped：这次是否真的替换了二进制；
//   - served：探活是否确认端口重新可用（false 表示"已触发但没探到"）。
func reloadRestart(port int, restartWith func(stagedPath string) error, hasStaged func() bool, logf func(string, ...any)) (swapped, served bool) {
	staged := ""
	if hasStaged != nil && hasStaged() {
		// 空串表示"用约定落点"（core.SelfRestart.stagingPath）—— 服务自己知道它在哪。
		staged = ""
		swapped = true
	}
	kind := "重载二进制并重启"
	if !swapped {
		kind = "仅重启（没有新编译产物，二进制保持不变）"
	}
	logf("reload: %s", kind)

	if restartWith == nil {
		logf("reload: self-restart not wired; refusing to pretend it happened")
		return false, true
	}
	if err := restartWith(staged); err != nil {
		// 起不来 = 服务仍在本进程上服务（core 侧的失败语义就是这样），不是灾难。
		logf("reload: restart failed, staying on the current process: %v", err)
		return false, true
	}

	// 换进程已经触发：旧进程即将让出端口。这里只能等"端口重新可用"作为
	// 可观测证据 —— 但注意它**不是**重启成功的判据（真正判据见 core 的
	// restart-handoff 摘要比对），只是给调用方一个能看见的收敛信号。
	served = waitServing(port, portProbeTimeout)
	if !served {
		logf("reload: port :%d did not come back within %s; check ~/.pieqi/logs", port, portProbeTimeout)
	}
	return swapped, served
}

// waitServing 轮询 /ready 直到它回 200 或超时。
//
// 为什么不只探 TCP：端口握手成功不等于服务能处理请求（gin 起来但存储没就绪
// 时也能 accept）。而 /ready 是服务自己产的 200，至少证明路由栈在跑。
// 失败不 panic、不重试永动机 —— 超时就返回 false。
func waitServing(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	u := fmt.Sprintf("http://127.0.0.1:%d/ready", port)
	// 先等旧进程把端口放出来（它会在 500ms 后开始 swap/让位），再开始探。
	time.Sleep(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		resp, err := client.Get(u)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

// adminReloadRequest 描述一次重启请求的来源，用于把结果写进日志。
//
// 目前只有 port 与「要不要换版」两件事需要记；保留结构体是为了让以后加
// "只重启某个渠道"之类的能力时不必再改签名。
type adminReloadRequest struct {
	Port int
}

// reloadSummary 是写进响应体的用户可见结论（handler 用它回 202）。
//
// 为什么要把 mode 明确写出来：调用方最常问的是"我的新版本上去了吗"，
// 而"重启了但没换版"和"换了版并重启"在日志里都是几行 info —— 必须在
// 响应里给出确定答案，否则面板只能靠猜。
func reloadSummary(r adminReloadRequest, swapped, served bool) map[string]any {
	mode := "restart-only"
	if swapped {
		mode = "swap-and-restart"
	}
	return map[string]any{
		"ok":       true,
		"accepted": true,
		"mode":     mode,
		"swapped":  swapped,
		// served 是"探活看到端口回来了"，不是重启成功的判据；
		// 进程替换的判据是下次启动读交接单时的摘要比对。
		"served": served,
		"port":   r.Port,
	}
}

// quoteURL 把地址转成可读的 URL（日志用）。
func quoteURL(host string, port int) string {
	return (&url.URL{Scheme: "http", Host: fmt.Sprintf("%s:%d", host, port)}).String()
}

// envInt 读一个整数环境变量，未设置或不合法时返回 def。
//
// 只给自测用（PIEQI_SERVER_PORT 已在配置层处理），这里不引入新的配置面。
func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	return n
}

var _ = json.Marshal // 保持 import 占位（响应体由 gin 序列化）
