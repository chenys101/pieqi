package core

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// SelfRestart 让常驻服务在**没有人类介入**的情况下换掉自己的二进制并重启。
//
// 为什么需要它（而不是让使用者双击 start.bat）：
// pieqi 的定位是把 coding agent 接进 IM/PWA，工作流本身就是**远程自迭代** ——
// agent 改完代码、编译、重启、再验证。如果"重启"这一步必须由人坐在机器前完成，
// 整个闭环就断了。所以重启必须是服务自身的能力。
//
// 为什么不能简单地"编译完 taskkill 再起一个新进程"：
// 那条路在工作区里跑 agent 时会被沙箱挡死 —— agent 会话持有的是受限令牌
// （Windows 上是 Low 完整性），它起的任何子进程都继承该令牌，因而写不了
// ~/.pieqi 这类 Medium 目录，也管不了工作区外的进程。而 **pieqi 自己是以
// 正常身份跑在工作区外的**，它天生就有这些权限。于是正确的做法是：
// 让 pieqi 自己做那件 agent 做不到的事 —— 这就是本文件的全部理由。
//
// 流程（Restart）：
//  1. 备份当前二进制 → .old（Windows 上运行中的 exe 不能被覆盖，但可被改名）
//  2. 用新二进制替换自身
//  3. 以脱离父进程的方式重新启动自己
//  4. 返回，由调用方决定何时退出（响应必须先发出去）
type SelfRestart struct {
	execPath string // 当前运行中的可执行文件绝对路径（os.Executable）
	dataRoot string // ~/.pieqi，日志与备份所在
	// stagingDir 是 agent 侧可写的交付目录（项目工作区下的约定目录）。
	// 新二进制由 agent 落到 <stagingDir>/pieqi.new[.exe]，再由本服务搬走 ——
	// 见 stagingPath 的两侧分工说明。
	//
	// 由配置显式给出，**不取 os.Getwd()**：常驻服务从 <dataRoot>/bin 启动，
	// cwd 是那里而不是项目目录，取 cwd 会让交付落点跑回沙箱外（见 stagingPath）。
	stagingDir string
	logf       func(string, ...any)
	// affectedFunc 返回"此刻挂在本进程上、会被重启打断的会话 id"。
	//
	// 由 main 注入而不是让 SelfRestart 自己查 store：SelfRestart 的职责是
	// "换二进制 + 起新进程"，会话归 TaskRunner/store 管。注入让依赖方向保持
	// 单向（core 内部也不产生 SelfRestart → TaskStore 的硬耦合），
	// 未接线时行为退化为空列表（交接单照写，只是没有会话要接续）。
	affectedFunc func() []string
	// pendingInitiator 是**本次重启的发起者会话 id**，由 HTTP 层在收到请求时
	// 通过 SetInitiator 写入，writeHandoff 读取。
	//
	// 为什么要在"收到请求"时就记下来，而不是等退出前现取：
	// 退出前那一刻，发起者自己那一轮早已随 agent 子进程死了、状态也不是
	// running，任何"现取"都拿不到它（这正是它一直被漏掉的根因）。身份只在
	// 请求进来的那一瞬间是新鲜可信的，所以必须当场捕获。
	//
	// 不加锁：它只在"收到请求 → 单次 RestartAndExit"这一条串行路径上写读，
	// 且重启是全进程生命周期只发生一次的动作。引入互斥反而让这段更难读。
	pendingInitiator string
}

// NewSelfRestart 构造自重启器。
//
// execPath 由 main 传入（与 hook 子进程回连共用同一路径）；
// stagingDir 是 agent 能写的交付目录（**显式传入**，不要传 cwd —— 见 stagingPath 注释）。
func NewSelfRestart(execPath, dataRoot, stagingDir string, logf func(string, ...any)) *SelfRestart {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &SelfRestart{execPath: execPath, dataRoot: dataRoot, stagingDir: stagingDir, logf: logf}
}

// SetInitiator 记录本次重启的发起者会话 id（HTTP 层在收到 restart 请求时调用）。
//
// 必须在**收到请求的当下**调用，不能等退出前补 —— 见 pendingInitiator 注释。
// 空字符串是合法输入（无法识别发起者，例如人类用 curl 直接调），此时
// 交接单照写，只是少了"接回发起者"这一项。
func (s *SelfRestart) SetInitiator(taskID string) {
	s.pendingInitiator = taskID
}

// stagingPath 返回"待切换的新二进制"约定落点。
//
// **为什么放在项目工作区里而不是 ~/.pieqi/bin/ 旁边**（这是本设计最容易搞错的一点）：
// 交付新二进制的**是 agent**，而 agent 跑在受限令牌下 —— 它写得了工作区，
// 写不了 ~/.pieqi。如果暂存区设在 ~/.pieqi，就等于要求 agent 先突破沙箱才能
// 使用这个功能，整个设计立刻失去意义（本次实现正是为了消灭那个需求）。
//
// 所以分工是：
//   agent  → 编译产物丢进工作区约定路径（它做得到）
//   pieqi  → 读工作区、写 ~/.pieqi、换自己的 exe（它是 Medium，做得到）
//
// **不能取 os.Getwd()**（2026-10-06 实测踩到）：常驻服务从 <dataRoot>/bin 启动
// （见 ~/.pieqi/run-pieqi.cmd），cwd 就是那个目录 —— 于是"工作区落点"落回
// ~/.pieqi 下，正好又是 agent 写不到的地方，整个设计白做。交付目录必须来自
// **配置里的项目路径**，与进程从哪启动无关。
func (s *SelfRestart) stagingPath() string {
	if s.stagingDir != "" {
		return filepath.Join(s.stagingDir, "pieqi.new"+exeSuffix())
	}
	// 没给交付目录（如测试）时退回数据目录，保持行为可预期。
	return filepath.Join(s.dataRoot, "bin", "pieqi.new"+exeSuffix())
}

// StagingHint 返回交付落点的完整路径，供 API 在"没有暂存文件"时给出可操作提示。
func (s *SelfRestart) StagingHint() string { return s.stagingPath() }

// SetAffectedFunc 接线"哪些会话会被这次重启打断"的查询（main 侧注入）。
//
// 与 SetFeedback 等同一模式：不接线则返回空列表，绝不臆造会话 id。
func (s *SelfRestart) SetAffectedFunc(f func() []string) { s.affectedFunc = f }

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// HasStaged 报告是否有待切换的新二进制（便于前端/脚本先询问再动手）。
func (s *SelfRestart) HasStaged() bool {
	st, err := os.Stat(s.stagingPath())
	return err == nil && !st.IsDir() && st.Size() > 0
}

// Restart 用暂存的新二进制替换当前运行中的二进制，并重新拉起自己。
//
// 返回后调用方**仍需自行退出**（通常是先回 HTTP 响应再 os.Exit）：
// 本函数不退出进程，是为了让 API 能把这个结果告诉调用方 —— 否则请求会
// 以一个断掉的连接收场，调用方无法区分"重启已触发"和"服务崩了"。
//
// stagedPath 为空时使用约定落点 stagingPath()；两者都不存在则报错，绝不静默。
func (s *SelfRestart) Restart(stagedPath string) error {
	if stagedPath == "" {
		stagedPath = s.stagingPath()
	}
	if s.execPath == "" {
		return fmt.Errorf("self-restart: empty executable path")
	}

	st, err := os.Stat(stagedPath)
	if err != nil {
		return fmt.Errorf("self-restart: staged binary not found at %s: %w", stagedPath, err)
	}
	if st.IsDir() || st.Size() == 0 {
		return fmt.Errorf("self-restart: staged binary %s is not a usable file", stagedPath)
	}

	// ① 备份当前二进制。Windows 不允许覆盖正在运行的 exe，但**允许改名** ——
	// 改名后原文件对象仍被进程持有，运行不受影响，而路径腾出来可以放新的。
	// 这也是为什么顺序必须是「先改名、再落新文件」，不能反过来。
	backup := s.execPath + ".old"
	_ = os.Remove(backup) // 上一轮的备份可能还在；删不掉不影响（下面还会再试）
	if err := os.Rename(s.execPath, backup); err != nil {
		return fmt.Errorf("self-restart: backup running binary %s: %w", s.execPath, err)
	}

	// ② 把新二进制放到原路径。用 copy 而非 rename：staged 文件可能位于
	// 另一个卷（E:\ 与 C:\ 之间 rename 会失败），copy+删除更稳。
	if err := copyFile(stagedPath, s.execPath); err != nil {
		// 回滚：把备份改回原名，保证服务仍有一个能跑的二进制。
		// 失败回滚是灾难性的（既没新 exe 也没旧 exe），所以显式报出来。
		if rbErr := os.Rename(backup, s.execPath); rbErr != nil {
			return fmt.Errorf("self-restart: install new binary: %w (AND rollback failed: %v — 需人工恢复 %s)", err, rbErr, backup)
		}
		return fmt.Errorf("self-restart: install new binary: %w", err)
	}
	s.logf("self-restart: binary swapped", "exec", s.execPath, "backup", backup)

	// ②b 消费掉 staged 文件。**必须在这里做，且必须按内容比对**。
	//
	// 为什么需要它：staged 文件此前从不清理，于是 GET /api/admin/restart 永远报
	// staged:true（"有待应用的新版本"），而那个版本其实早就装上了。后果不只是
	// 状态显示不准 —— 它会给下一次重启埋雷：某次构建后忘记重新投递，磁盘上却
	// 还躺着**上一版的同名文件**，一次误触 restart 就把旧版本换了上去（等于回退）。
	//
	// 为什么按内容比对（对齐 magpie 的 RemoveStaleNew）而不是直接删：
	// 只有当 staged 与"刚装上去的 exe"**内容一致**时才说明它确实被消费了，
	// 删它是安全的。若两者不同，说明有第三方正在交付另一个版本（并行会话的
	// agent 刚写好新文件），此时删掉等于替别人丢掉他的构建产物 —— 不能删。
	//
	// 失败只记日志：清理是善后，不该让一次已经成功的二进制切换变成失败。
	if stagedPath != s.execPath {
		s.cleanupStagedIfConsumed(stagedPath)
	}

	// ③ 落交接单。**必须在 spawn 之前** —— 这是本文件最反直觉、也最容易写错的一步。
	//
	// 曾经的顺序是「spawn → waitForListener → writeHandoff」，它有一个必然发生
	// 的竞态（2026-10-06 实测事故）：新进程在 main.go 里**很早就**读交接单
	// （ResolveRestartHandoff，远早于它 bind 端口），而旧进程判定"新实例已就绪"
	// 用的恰恰是**端口能不能连上**（waitForListener → canDial）。端口一通，
	// 说明新进程早就走过读单子那一步了 —— 于是 writeHandoff 的写入**永远**
	// 落在读取之后，单子每次都被写在一个已经读过它的进程背后。
	//
	// 症状极具迷惑性：日志里 handoff 相关记录全空（writeHandoff 成功时刻意
	// 不打日志，只在失败时打），看起来像"这段代码根本没执行"，进而被误判成
	// "跑的是旧二进制"。实际是代码执行了、单子也写成了，只是**没人读它**。
	//
	// 改成 spawn 前写：新进程无论启动多快，单子都已经在盘上了。
	if err := s.writeHandoff(); err != nil {
		// 写不成就**不要 spawn**：新进程会在"读不到单子"的世界里启动，
		// 把这次计划内重启当成进程崩溃，把所有在跑会话标 failed —— 正是
		// 我们要消灭的那种大面积失败。宁可这次重启没发生（旧实例仍在服务），
		// 也不要制造一批假故障。调用方会看到错误并保持服务存活。
		return fmt.Errorf("self-restart: write handoff: %w", err)
	}
	s.logf("self-restart: handoff written, spawning new instance",
		"path", journalPath(s.dataRoot), "affected", len(s.affectedTasks()))

	// ④ 拉起新进程。必须**脱离**当前进程：新实例要活得比旧实例久，
	// 不能作为子进程随旧进程退出而被回收（Windows 上尤其明显）。
	if err := s.spawnDetached(); err != nil {
		// spawn 失败 ⇒ 这次重启不会发生，旧实例继续服务。此时必须把刚写的
		// 交接单**撤回**：留着它会让下一次普通启动读到一张"声称有重启、
		// 但新二进制摘要对不上"的单子，凭空把一批会话判成待接续。
		if rmErr := clearRestartJournal(s.dataRoot); rmErr != nil {
			s.logf("self-restart: spawn failed AND handoff cleanup failed", "err", rmErr.Error())
		}
		return fmt.Errorf("self-restart: spawn new instance: %w", err)
	}
	s.logf("self-restart: new instance spawned", "exec", s.execPath)
	return nil
}

// cleanupStagedIfConsumed 在 staged 文件与新装的 exe **内容一致**时删掉它。
//
// 判据是内容摘要相等，不是"文件名对得上"：
//   - 相等 ⇒ 它已确实被消费（就是刚装上去那份），删掉是纯善后
//   - 不等 ⇒ 有人正在交付**另一个**版本，不能替他丢掉
//
// 只在重启成功后调用（调用点紧跟 copyFile 成功之后），失败仅记日志。
func (s *SelfRestart) cleanupStagedIfConsumed(stagedPath string) {
	stagedSum, err := exeSHA256(stagedPath)
	if err != nil {
		// 读不到就什么都不做：宁可不清理，也不能误删。
		return
	}
	execSum, err := exeSHA256(s.execPath)
	if err != nil {
		return
	}
	if stagedSum != execSum {
		s.logf("self-restart: staged file differs from installed binary; keeping it",
			"staged", stagedPath)
		return
	}
	if err := os.Remove(stagedPath); err != nil {
		// 删不掉不影响服务：只是下次仍会报 staged:true。
		s.logf("self-restart: could not remove consumed staged file", "err", err.Error())
		return
	}
	s.logf("self-restart: removed consumed staged file", "staged", stagedPath)
}

// copyFile 复制并保留可执行权限。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// 先写临时文件再 rename：避免在目标处留下"复制到一半"的 exe。
	// 这里直接用 dst 的临时兄弟名，保证同卷（rename 原子）。
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// waitForListener 等新实例**报到自己已开始服务**。
//
// ⚠️ 历史（务必先读懂再改，这里踩过一次生产事故）：
//
// 本函数原先的实现是轮询 canDial(addr) —— 探测"端口能不能连上"。那是**同义反复**：
// 旧进程自己就监听着那个端口，于是第一次探测必然返回 true，旧进程立刻 exit(0)，
// 而新实例还要 ~150ms 才 bind，撞上 EADDRINUSE 直接 FATAL。
// 结果「旧的走了、新的死了」，服务彻底消失（2026-07-07 首次记录，
// 2026-10-13 12:31 再次复现，日志里的判据是 spawn 与 is-serving 同一毫秒）。
//
// 根因不是超时太短，而是**信号源错了**：端口通不通区分不了"新实例在监听"与
// "我自己还在监听"。改成由新进程写就绪文件（MarkRestartReady），信号只能来自
// 新进程，同义反复从结构上不可能再发生。
//
// addr 保留在签名里是为了让调用方不必改（且日志里仍有端口信息），但**不再用它判定**。
func (s *SelfRestart) waitForListener(addr string, timeout time.Duration) bool {
	_ = addr // 见上：不再依据端口判定，保留形参仅为调用方兼容
	return awaitRestartReady(s.dataRoot, timeout)
}

// RestartAndExit 是给 HTTP 层用的编排：等**响应发出之后**才退出进程。
//
// 时序是关键 —— 必须让调用方先收到 202，否则重启会把请求连接一起吞掉，
// agent 无从判断是"已触发"还是"崩了"，只能瞎猜重试（这正是本次要实现的东西
// 要消灭的那类不确定性）。
//
// exit 由 main 注入（os.Exit），便于测试替换。
//
// 两条"失败也不退出"的分支是刻意的：旧实例还活着且仍能服务，比退出去什么都
// 不剩要好。宁可重启没成功（调用方可以从端口/日志看出来），也不要服务消失。
//
// 交接单（见 restart_journal.go）：**在 Restart() 里、spawn 新进程之前**就落盘，
// 因为新进程读它的时机远早于"端口能连上"，等到 waitForListener 之后再写必然
// 晚于读取（2026-10-06 实测的竞态，详见 Restart 第 ③ 步注释）。
func (s *SelfRestart) RestartAndExit(addr string, exit func(int)) {
	time.Sleep(500 * time.Millisecond) // 给响应留出 flush 时间

	if err := s.Restart(""); err != nil {
		s.logf("self-restart failed, staying on current binary", "err", err.Error())
		return
	}

	// 等到新实例**自己报到**（写就绪文件）。判据见 waitForListener 的注释：
	// 绝不能用"端口能不能连"——那是同义反复，会让旧进程在新实例 bind 之前就退出。
	//
	// 这里刻意**不**在这里写交接单：它在 Restart() 里、spawn 之前就落盘了
	// （新进程读单子的时机远早于开始服务，写在这里必然晚于读取）。
	if !s.waitForListener(addr, 20*time.Second) {
		// 新实例没能起来（端口被占、配置错、迁移 panic）。此刻二进制**已经换掉**，
		// 若不回滚就是一个"跑不起来的版本 + 一个即将退出的旧进程"= 服务消失。
		//
		// 所以先把旧二进制换回来再继续服务：magpie 的 RelaunchBinary 同样是
		// "起不来就把 old 放回去，并且**继续跑**而不是退出"。
		// 判据与顺序：先回滚文件，再保持自己存活 —— 只要自己还活着，服务就没断。
		if rbErr := s.rollbackBinary(); rbErr != nil {
			s.logf("self-restart: new instance never reported ready AND rollback failed",
				"err", rbErr.Error(), "hint", "需人工恢复 "+s.execPath+".old")
		} else {
			s.logf("self-restart: new instance never reported ready within 20s; rolled back to previous binary, keeping current process alive")
		}
		return
	}

	s.logf("self-restart: new instance is serving, exiting old process")
	if exit != nil {
		exit(0)
	}
}

// rollbackBinary 把 <execPath>.old 换回 execPath（新实例没起来时的退路）。
//
// 只改文件、不重启：调用方仍在运行（旧二进制的内容还在它自己的内存/句柄里，
// 把路径上的文件换回去不影响当前进程继续服务），下次重启自然用的就是旧版本。
//
// 三步都幂等、且失败会明确报出来：这条路上"静默半成功"比失败更危险 ——
// 会留下一个路径与预期不符的 exe，而没人知道。
func (s *SelfRestart) rollbackBinary() error {
	backup := s.execPath + ".old"
	if _, err := os.Stat(backup); err != nil {
		return fmt.Errorf("backup %s not available: %w", backup, err)
	}
	// 当前路径上是要被丢弃的新版本：先挪开而不是直接覆盖，
	// 保留它便于事后排查"为什么这个版本起不来"。
	failed := s.execPath + ".failed"
	_ = os.Remove(failed)
	if err := os.Rename(s.execPath, failed); err != nil {
		return fmt.Errorf("move failed build aside: %w", err)
	}
	if err := os.Rename(backup, s.execPath); err != nil {
		// 回滚失败：尽量把现状还原成"路径上有个 exe"，避免路径空缺。
		if rbErr := os.Rename(failed, s.execPath); rbErr != nil {
			return fmt.Errorf("restore backup: %w (AND re-restore failed: %v)", err, rbErr)
		}
		return fmt.Errorf("restore backup: %w", err)
	}
	return nil
}

// writeHandoff 落盘交接单，交给即将接管端口的新进程。
//
// 摘要取自**staged 文件**而不是 execPath：execPath 此刻已是新二进制的内容
// （Restart 里 copy 过了），但 staged 文件是"交付方声称要装的东西"，
// 拿它当基准能同时验证「装上了」和「装的就是交付的那个」。
// staged 已被消费掉时退回用 execPath —— 两者内容此时本就相同。
func (s *SelfRestart) writeHandoff() error {
	sum, err := s.stagedOrSelfSHA256()
	if err != nil {
		return err
	}
	affected := s.affectedTasks()
	// 发起者必须同时出现在 AffectedTaskIDs 里：新进程是按这个列表决定
	// "哪些任务保留 running（可接续）"的（见 NewTaskStore 的 interrupted 形参）。
	// 只记 InitiatorTaskID 而不并入列表，会让接续逻辑压根看不到它 —— 状态在
	// load 时就被判 failed 了。去重是必须的：它可能本来也在列表里。
	if s.pendingInitiator != "" && !contains(affected, s.pendingInitiator) {
		affected = append(affected, s.pendingInitiator)
	}
	j := &RestartJournal{
		StartedAt:       time.Now(),
		FromPID:         os.Getpid(),
		StagedSHA256:    sum,
		AffectedTaskIDs: affected,
		InitiatorTaskID: s.pendingInitiator,
		Version:         shortSHA(sum),
	}
	return writeRestartJournal(s.dataRoot, j)
}

// contains 判断列表里是否已有该 id（列表极小，线性扫即可）。
func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// stagedOrSelfSHA256 返回交接用的内容摘要（优先 staged 文件，其次当前 exe）。
func (s *SelfRestart) stagedOrSelfSHA256() (string, error) {
	if st, err := os.Stat(s.stagingPath()); err == nil && !st.IsDir() && st.Size() > 0 {
		if sum, err := exeSHA256(s.stagingPath()); err == nil {
			return sum, nil
		}
	}
	return exeSHA256(s.execPath)
}

// affectedTasks 收集被本次重启打断的会话 id（未接线 affectedFunc 时为空）。
func (s *SelfRestart) affectedTasks() []string {
	if s.affectedFunc == nil {
		return nil
	}
	return s.affectedFunc()
}
