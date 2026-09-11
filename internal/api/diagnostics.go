package api

import (
	"bytes"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"pieqi/internal/logging"

	"github.com/gin-gonic/gin"
)

// diagnosticsDays 导出窗口：最近 7 天。
// 与设置页那句"导出最近 7 天日志用于排查问题"是同一个数字 ——
// 文案与行为必须由同一个常量说话，否则改了这边忘了那边。
const diagnosticsDays = 7

// exportDiagnostics 处理 GET /api/diagnostics/export —— 打包最近 7 天日志为 zip。
//
// 鉴权与 /api 主组一致（内网放行 / 外网需有效 token）：日志里可能含项目路径、
// 任务标题这类私有信息，不该对无凭据的外网请求开放。
func (s *Server) exportDiagnostics(c *gin.Context) {
	if s.logDir == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "diagnostics not configured"})
		return
	}
	now := time.Now()
	meta := map[string]any{
		"generated_at": now.Format(time.RFC3339),
		"window_days":  diagnosticsDays,
		"go_version":   runtime.Version(),
		"os":           runtime.GOOS,
		"arch":         runtime.GOARCH,
	}
	if s.cfg != nil {
		meta["server_mode"] = s.cfg.Server.Mode
	}
	if !s.startedAt.IsZero() {
		meta["server_started_at"] = s.startedAt.Format(time.RFC3339)
		meta["uptime_seconds"] = int(now.Sub(s.startedAt).Seconds())
	}
	if s.store != nil {
		meta["tasks_total"] = len(s.store.List())
	}
	if s.bots != nil {
		meta["bots_total"] = len(s.bots.List())
	}

	// 先打进内存再回写：这样"打不出来"能回 500，"没有日志"仍然是一份可用的 zip
	// （后者本身就是一个排查结论，比让用户对着一个 HTTP 错误猜要强）。
	var buf bytes.Buffer
	if err := logging.BuildDiagnosticsZip(&buf, s.logDir, now, diagnosticsDays, meta); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "build diagnostics archive: " + err.Error()})
		return
	}

	name := fmt.Sprintf("pieqi-diagnostics-%s.zip", now.Format("20060102-150405"))
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "application/zip", buf.Bytes())
}
