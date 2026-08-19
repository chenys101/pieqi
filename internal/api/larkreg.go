package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"pieqi/internal/larkreg"

	"github.com/gin-gonic/gin"
)

// larkRegRunner 抽象 *larkreg.Registration 供测试注入 fake。
type larkRegRunner interface {
	Run(ctx context.Context, opts larkreg.Options) (larkreg.Result, error)
}

// larkRegState 一次 Device Flow 的进行中状态。
type larkRegState struct {
	mu        sync.Mutex
	qrURL     string
	qrExpire  int
	done      bool
	appID     string
	appSecret string
	err       string
	startedAt time.Time
}

// SetLarkReg 注入 Device Flow runner 和凭据落盘路径。仅测试与 main.go 调用。
func (s *Server) SetLarkReg(runner larkRegRunner, credPath string) {
	if s.larkRegState == nil {
		s.larkRegState = &larkRegState{}
	}
	s.larkRegRunner = runner
	s.larkRegCredPath = credPath
}

// larkRegStart handles POST /api/larkreg/start — 仅内网(BindOpGateMiddleware 套在路由组上)。
// 启动一个 Device Flow goroutine,立即返回 qr_url;前端用 /poll 查询结果。
func (s *Server) larkRegStart(c *gin.Context) {
	if s.larkRegRunner == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "lark registration not configured"})
		return
	}
	// 重置状态(只允许同时一个进行中的 flow)
	s.larkRegState.mu.Lock()
	s.larkRegState.done = false
	s.larkRegState.appID = ""
	s.larkRegState.appSecret = ""
	s.larkRegState.err = ""
	s.larkRegState.qrURL = ""
	s.larkRegState.startedAt = time.Now()
	s.larkRegState.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		res, err := s.larkRegRunner.Run(ctx, larkreg.Options{
			OnQRCode: func(url string, expireIn int) {
				s.larkRegState.mu.Lock()
				s.larkRegState.qrURL = url
				s.larkRegState.qrExpire = expireIn
				s.larkRegState.mu.Unlock()
			},
			CreateOnly: true,
		})
		s.larkRegState.mu.Lock()
		defer s.larkRegState.mu.Unlock()
		if err != nil {
			s.larkRegState.err = err.Error()
			s.larkRegState.done = true
			return
		}
		s.larkRegState.appID = res.AppID
		s.larkRegState.appSecret = res.AppSecret
		s.larkRegState.done = true
	}()

	// 等 qr_url 出现(最多 3s)
	for i := 0; i < 30; i++ {
		s.larkRegState.mu.Lock()
		url := s.larkRegState.qrURL
		expire := s.larkRegState.qrExpire
		s.larkRegState.mu.Unlock()
		if url != "" {
			c.JSON(http.StatusOK, gin.H{"qr_url": url, "expire_in": expire})
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "pending", "hint": "poll /api/larkreg/poll"})
}

// larkRegPoll handles GET /api/larkreg/poll — 仅内网。
// 返回 device flow 状态:pending / success(带 app_id,不返回 app_secret 给前端)
// / error。成功时把凭据落盘。
func (s *Server) larkRegPoll(c *gin.Context) {
	if s.larkRegRunner == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "lark registration not configured"})
		return
	}
	s.larkRegState.mu.Lock()
	defer s.larkRegState.mu.Unlock()

	if s.larkRegState.err != "" {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": s.larkRegState.err})
		return
	}
	if !s.larkRegState.done {
		c.JSON(http.StatusAccepted, gin.H{"status": "pending"})
		return
	}
	// 成功:落盘 + 返回(只回 app_id,app_secret 不出 HTTP 响应)
	if s.larkRegCredPath != "" && s.larkRegState.appID != "" {
		if err := larkreg.SaveCredentials(s.larkRegCredPath, s.larkRegState.appID, s.larkRegState.appSecret); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "save credentials: " + err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"app_id": s.larkRegState.appID,
		"hint":   "restart pieqi to apply new credentials",
	})
}
