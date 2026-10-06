package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// restartJournalFile 是自重启交接单在数据目录下的文件名。
//
// 为什么需要一张**跨进程**的交接单：自重启会换掉整个进程，而"重启成功"这个
// 事实只有**新进程自己**能判定（它才知道自己起来了、且自己就是那个 staged 二进制）。
// 旧进程无法同步等待这个结论——它的上下文随 exit(0) 一起没了。所以两端只能靠
// 落盘的交接单接力：旧进程写「我要重启了，受影响的会话是这些」，新进程读它、
// 校验、回填、然后清掉。
//
// 文件名带 .json 后缀与 tasks/ 一致，便于人工排查时直接打开看。
const restartJournalFile = "restart-handoff.json"

// RestartJournal 记录一次自重启的交接信息。
//
// 生命周期严格是「旧进程写 → 新进程读 → 新进程清」：
//   - 正常情况下新进程校验通过后立即删除它（见 resolveRestartHandoff）。
//   - 新进程若压根没起来（copy 失败 / 端口占用），文件会残留 —— 这不算坏：
//     下次启动时 hash 校验必然不通过（或 staged 已不在），会被判为「上一轮
//     重启未成功」并同样清掉，不会造成误判。
type RestartJournal struct {
	// StartedAt 是旧进程决定退出、落下交接单的时刻。
	StartedAt time.Time `json:"started_at"`
	// FromPID 是发起重启的旧进程 PID，纯为排查取证（不参与判定）。
	FromPID int `json:"from_pid"`
	// StagedSHA256 是**待切换二进制**的内容摘要。这是判定「重启成功」的唯一依据：
	// 新进程算出自己的 exe 摘要与之相等 ⇒ 切换确实发生了，且跑起来的就是新版本。
	//
	// 为什么用内容摘要而不是「端口通了」：Restart() 的三步里，spawn 成功 ≠ 启动成功。
	// waitForListener 超时时旧进程会**保持存活**（根本不退出），那种情况压根不该有交接单。
	// 换言之，只要新进程读到了这张单子且 hash 对得上，重启就 100% 成功了。
	StagedSHA256 string `json:"staged_sha256"`
	// AffectedTaskIDs 是被这次重启打断的会话。旧进程退出前从 store 里现取，
	// 只收 running / 等待 approval 的（即子进程真的挂在本进程上的那些）。
	AffectedTaskIDs []string `json:"affected_task_ids"`
	// InitiatorTaskID 是**发起这次重启的那个会话**的 id（无法识别时为空）。
	//
	// 为什么必须单独记它：发起者不在 AffectedTaskIDs 里，而原因是时序 ——
	// 它调用 POST /api/admin/restart 之后，AgentedTaskIDs 的收集发生在
	// waitForListener 成功之后，此刻发起者那一轮早已随 agent 子进程死去，
	// 状态不再是 running，于是被 LiveTaskIDs 漏掉。结果是它永远拿不到
	// "重启已完成"这个它本来无法同步获取的信号，也不知道自己触发的动作成了没有。
	//
	// 记下它，让新进程能对它做两件别的会话不需要的事：
	//  1. 即使它的状态看起来不该接续，也把它接回来；
	//  2. 在接续 prompt 里明确告知「重启已成功，勿重复触发」——
	//     否则它会重试那个 POST，把自己再杀一次，形成重启循环。
	//
	// 身份来自 agent 子进程环境变量 PIEQI_TASK_ID（见 InitiateRestart 的说明），
	// 不来自 HTTP 头：头可以伪造/缺失，而环境变量是服务自己注入的，可信。
	InitiatorTaskID string `json:"initiator_task_id,omitempty"`
	// Version 是这次交接的新版本标识（当前二进制的短摘要）。
	// 接续 prompt 要把它讲给发起者听，让它能确认"跑起来的是我以为的那个版本"。
	Version string `json:"version,omitempty"`
}

// exeSHA256 计算可执行文件的内容摘要。
//
// 读整个文件是刻意的：pieqi 二进制在数十 MB 量级，启动时一次性读入算摘要
// （几十毫秒）远便宜于「重启判定错了」的代价。不引入增量/抽样摘要——
// 那会带来"摘要相同但内容不同"的误判面，而这个判定的结果直接决定
// 用户会话是被自动接回、还是被标 failed 等人工处理。
func exeSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// journalPath 返回交接单在数据目录下的完整路径。
func journalPath(dataRoot string) string {
	return filepath.Join(dataRoot, restartJournalFile)
}

// writeRestartJournal 落盘一张交接单（原子写：先临时文件再 rename）。
//
// 必须原子：新进程可能在任何时刻读它，读到半个 JSON 会解析失败，
// 进而把一个成功的重启误判成「未成功」——那正是我们要消灭的不确定性。
//
// 复用 TaskStore.persist 同一套 renameWithRetry：Windows 上刚落盘的目标文件
// 会被杀软/索引器瞬时持有（ERROR_ACCESS_DENIED），不重试就会间歇性写不出交接单，
// 表现为「偶尔有几个会话重启后没被自动接回」——这种偶发性最难排查，所以直接沿用。
func writeRestartJournal(dataRoot string, j *RestartJournal) error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal restart journal: %w", err)
	}
	path := journalPath(dataRoot)
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write restart journal tmp: %w", err)
	}
	if err := renameWithRetry(func() error { return os.Rename(tmp, path) }); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install restart journal: %w", err)
	}
	return nil
}

// readRestartJournal 读取交接单；不存在时返回 (nil, nil)。
//
// 不存在是**正常路径**（绝大多数启动都不是自重启），所以不当作错误 ——
// 让调用方用 nil 判断"这只是一次普通启动"。
func readRestartJournal(dataRoot string) (*RestartJournal, error) {
	data, err := os.ReadFile(journalPath(dataRoot))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var j RestartJournal
	if err := json.Unmarshal(data, &j); err != nil {
		// 损坏的交接单不能当成"没有"：那会静默丢掉一批等待接回的会话。
		// 上报给调用方，由它决定记日志后清理。
		return nil, fmt.Errorf("parse restart journal: %w", err)
	}
	return &j, nil
}

// clearRestartJournal 删除交接单。已不存在时不算错误（幂等）。
func clearRestartJournal(dataRoot string) error {
	if err := os.Remove(journalPath(dataRoot)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// RestartOutcome 描述「上一轮自重启到底成没成」，供启动流程决定恢复策略。
type RestartOutcome struct {
	// WasSelfRestart 表示本次启动读到了交接单（即上一轮确实是自重启）。
	WasSelfRestart bool
	// Succeeded 表示 hash 校验通过：跑起来的就是那个 staged 二进制。
	Succeeded bool
	// Journal 是读到的交接单原文（WasSelfRestart 为 false 时为 nil）。
	Journal *RestartJournal
	// Reason 是判定说明，用于日志与系统事件措辞。
	Reason string
}

// ResolveRestartHandoff 在启动时解析交接单并判定上一轮自重启的结果。
//
// execPath 是**当前**运行的二进制路径，用它算出的摘要与单子上的
// StagedSHA256 比对。equal ⇒ 切换成功。
//
// 无论判定结果如何都会清掉交接单：它的语义是「一次性交接」，读完即失效。
// 留着它会让下次普通启动又读到一张旧单子，凭空"恢复"一批早已被处理过的会话。
func ResolveRestartHandoff(dataRoot, execPath string) RestartOutcome {
	return ResolveRestartHandoffWith(dataRoot, execPath, handoffReadAttempts, handoffReadInterval)
}

// handoffReadAttempts / handoffReadInterval 控制"单子还没出现"时的重试窗口。
//
// 为什么需要重试：修复后单子在 spawn 前就写好，竞态已消失；但**旧版本的进程**
// 可能正在发起重启 —— 它按老顺序（spawn → 等端口 → 写单子）落盘，新进程启动时
// 可能读到空。这个窗口给老版本留一条兼容路径，代价仅是单子不存在时最多多等
// 这一小段（正常情况下第一次读就有结果，不会睡）。
//
// 窗口取 3 秒：老顺序下"端口就绪 → 写单子"实测相差约 40ms（2026-10-06 数据），
// 3 秒是它的 70 余倍，足以覆盖杀软延迟与磁盘抖动；又不至于让普通启动明显变慢。
const (
	handoffReadAttempts = 30
	handoffReadInterval = 100 * time.Millisecond
)

// ResolveRestartHandoffWith 是 ResolveRestartHandoff 的可注入版本（便于测试）。
//
// attempts 为 1 时等价于"只读一次"，不做等待。
func ResolveRestartHandoffWith(dataRoot, execPath string, attempts int, interval time.Duration) RestartOutcome {
	if attempts < 1 {
		attempts = 1
	}

	var j *RestartJournal
	var readErr error
	for i := 0; i < attempts; i++ {
		j, readErr = readRestartJournal(dataRoot)
		// 读到单子、或读到损坏，都立即返回：损坏不是"还没写出来"，等下去不会变好。
		if j != nil || readErr != nil {
			break
		}
		if i < attempts-1 {
			time.Sleep(interval)
		}
	}

	if readErr != nil {
		// 单子损坏：清理并当作"非自重启启动"，宁可少接回也不能据此误接。
		_ = clearRestartJournal(dataRoot)
		return RestartOutcome{Reason: fmt.Sprintf("交接单损坏，按普通启动处理：%v", readErr)}
	}
	if j == nil {
		return RestartOutcome{Reason: "无自重启交接单（普通启动）"}
	}
	defer func() { _ = clearRestartJournal(dataRoot) }()

	out := RestartOutcome{WasSelfRestart: true, Journal: j}
	cur, err := exeSHA256(execPath)
	if err != nil {
		out.Reason = fmt.Sprintf("无法计算当前二进制摘要，按重启未成功处理：%v", err)
		return out
	}
	if j.StagedSHA256 == "" {
		out.Reason = "交接单缺少 staged 摘要，按重启未成功处理"
		return out
	}
	if cur == j.StagedSHA256 {
		out.Succeeded = true
		out.Reason = fmt.Sprintf("二进制摘要一致（%s），自重启成功", shortSHA(cur))
		return out
	}
	out.Reason = fmt.Sprintf("二进制摘要不一致（当前 %s / 交接 %s），自重启未成功",
		shortSHA(cur), shortSHA(j.StagedSHA256))
	return out
}

// shortSHA 把摘要截成前 12 位，仅用于日志与界面展示。
func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
