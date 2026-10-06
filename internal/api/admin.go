package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// restartFunc 由 main 注入：负责"换二进制 + 拉起新实例 + 旧实例退出"的编排。
// nil 表示未接线（旧测试 / 未启用），端点返回降级响应而不是假装成功。
//
// initiator 是发起这次重启的会话 taskID（无法识别时为空），由 postRestart
// 从请求里解析后传入。它必须在**收到请求**时就被记下 —— 见
// core.SelfRestart.SetInitiator 的时序说明。
type restartFunc func(initiator string)

// hasStagedFunc 报告是否存在待切换的新二进制。
//
// 单独暴露而不是让前端猜：调用方应当能**先问再动手** —— 「有新版本吗」
// 和「现在就切」是两个动作，混在一起会让一次误触变成一次无谓重启。
type hasStagedFunc func() bool

// stagingHintFunc 返回交付落点路径（用于把 409 的提示写成可操作的）。
type stagingHintFunc func() string

// SetSelfRestart 接线自重启能力（main 侧注入，见 core.SelfRestart）。
// 与 SetFeedback/SetCheckRunner 同一模式：不接线时端点降级，绝不静默假成功。
//
// hint 返回交付落点路径，仅用于把错误信息写得可操作（nil 则省略）。
func (s *Server) SetSelfRestart(hasStaged hasStagedFunc, restart restartFunc, hint func() string) {
	s.hasStaged = hasStaged
	s.selfRestart = restart
	s.stagingHint = hint
}

// getRestartStatus 报告是否已有待切换的新二进制（不影响运行中的服务）。
//
// GET /api/admin/restart
func (s *Server) getRestartStatus(c *gin.Context) {
	if s.hasStaged == nil {
		c.JSON(http.StatusNotImplemented, gin.H{
			"error":     "self-restart not available",
			"staged":    false,
			"available": false,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"staged":    s.hasStaged(),
		"available": true,
	})
}

// postRestart 触发自重启：换掉自己的二进制并以新版本重新起来。
//
// POST /api/admin/restart
//
// 为什么这个端点存在（而不要求人类去双击脚本）：pieqi 的用法就是远程自迭代 ——
// agent 改完代码应当能自己编译并重启验证。这条链路一旦需要人坐在机器前，
// 自动化闭环就断了。
//
// 为什么必须是 pieqi 自己做：工作区里跑的 agent 会话持有受限令牌，
// 它起的子进程继承该令牌，写不了 ~/.pieqi、也管不了工作区外的进程；
// 而 pieqi 自身是以正常身份跑在工作区外的，天生有这些权限。
//
// 安全：**仅内网**。它等价于本机任意代码执行（能把任意二进制装上并以服务身份
// 常驻），因此与 /api/auth/bind 同档：外部请求一律拒绝，即使 token 和身份都有效。
// 这条 gate 在 router 注册时挂上，见 Register 里的 adminGrp。
func (s *Server) postRestart(c *gin.Context) {
	if s.selfRestart == nil || s.hasStaged == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "self-restart not available"})
		return
	}
	if !s.hasStaged() {
		// 明确区分"没东西可切"和"切失败"：前者是调用方的使用问题
		// （忘了先把新二进制放到约定落点），应当给出可操作的指引 ——
		// 带上确切路径，调用方无需去读源码猜。
		hint := ""
		if s.stagingHint != nil {
			hint = s.stagingHint()
		}
		c.JSON(http.StatusConflict, gin.H{
			"error": "no staged binary",
			"hint":  "先编译新二进制并放到 " + hint + "，再调用本接口；服务会用它替换自身并重新启动",
			"path":  hint,
		})
		return
	}

	// 先回响应再退出（由注入的 restart 编排负责时序）。这里同步返回 202：
	// 调用方据此知道"重启已受理"，而不是"连接断了"。
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "restarting": true})
	// 发起者身份必须在**此处**（请求还活着的时候）解析并交给编排 ——
	// 等到进程退出前再取已经晚了，那时发起者那一轮早已随子进程消失。
	go s.selfRestart(initiatorFromRequest(c))
}

// initiatorFromRequest 解析"是哪个会话在请求重启我"。
//
// 来源是 X-Pieqi-Task-Id 这个请求头，由 agent 里跑的 shell 主动带上
// （值取自服务 spawn 时注入的环境变量 PIEQI_TASK_ID，见 agent.SessionConfig.TaskID）。
//
// 为什么用请求头而不是让服务去猜：
//   - 服务端无法从连接反推发起者：ACP 会话与 HTTP 请求之间没有任何现成关联。
//   - 头是**不可伪造**的，因为它的值不在服务端校验——它的可信度来自"只有本进程
//     spawn 的子进程才拿得到那个环境变量"。外部请求即使伪造这个头，也只能影响
//     "重启后要不要尝试接回某个 id"，而该端点本身已被 router 限制为仅内网，
//     且接回动作只会作用于真实存在的任务记录（不存在则忽略）。
//
// 取不到时返回空串：那表示"人类或外部工具直接调的"，此时不接回任何发起者，
// 其余会话不受影响 —— 这是必须保留的正常路径（curl 手动重启是常见操作）。
func initiatorFromRequest(c *gin.Context) string {
	return strings.TrimSpace(c.GetHeader("X-Pieqi-Task-Id"))
}

// exeSuffixForAPI 让错误提示里的文件名与平台一致（Windows 是 .exe）。
func exeSuffixForAPI() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}
