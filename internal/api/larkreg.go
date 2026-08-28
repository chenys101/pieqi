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

// larkConfigApplier 由 main.go 注入：把新配置热应用到运行中的飞书渠道。
// 返回 restartRequired=true 表示接入方式切换（webhook↔longconn）需重启生效。
type larkConfigApplier func(larkreg.ChannelConfig) (restartRequired bool, err error)

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

// SetLarkConfigApplier 注入配置热应用回调（main.go 传渠道控制器闭包；测试注入 fake）。
// nil-safe：未注入时配置仅落盘，前端回退"重启生效"提示。
func (s *Server) SetLarkConfigApplier(f larkConfigApplier) {
	s.larkConfigApplier = f
}

// larkRegStatus handles GET /api/larkreg/status — 仅内网（挂 BindOpGateMiddleware）。
// 返回当前飞书应用接入状态：registered + app_id（app_secret 绝不外泄）。
// 前端 boot 时用它决定显示"接入飞书"还是"已接入 xxx"。
func (s *Server) larkRegStatus(c *gin.Context) {
	appID := ""
	if s.larkRegCredPath != "" {
		if id, _, ok := larkreg.LoadCredentials(s.larkRegCredPath); ok {
			appID = id
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"registered": appID != "",
		"app_id":     appID,
	})
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
	// 热应用新凭据（已接线 applier 时即刻生效；否则保持旧"重启生效"提示）
	hint := "restart pieqi to apply new credentials"
	if s.larkConfigApplier != nil {
		if cfg, ok := larkreg.LoadConfig(s.larkRegCredPath); ok {
			if restartRequired, err := s.larkConfigApplier(cfg); err != nil {
				hint = "凭据已保存，但热应用失败: " + err.Error() + "（重启生效）"
			} else if restartRequired {
				hint = "已生效（接入方式切换需重启）"
			} else {
				hint = "已生效"
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"app_id": s.larkRegState.appID,
		"hint":   hint,
	})
}

// larkRegConfig 处理 GET /api/larkreg/config — 仅内网。
// 返回飞书渠道当前生效配置的脱敏视图（app_secret 等绝不外泄）。
// 有效配置 = 凭据文件（运行时覆盖）叠加在 config.yaml 之上。
func (s *Server) larkRegConfig(c *gin.Context) {
	eff := larkreg.ChannelConfig{EventMode: "longconn"}
	if s.cfg != nil {
		eff = larkreg.ChannelConfig{
			AppID:       s.cfg.Channels.Lark.AppID,
			AppSecret:   s.cfg.Channels.Lark.AppSecret,
			VerifyToken: s.cfg.Channels.Lark.VerifyToken,
			EncryptKey:  s.cfg.Channels.Lark.EncryptKey,
			EventMode:   s.cfg.Channels.Lark.EventMode,
		}
		if eff.EventMode == "" {
			eff.EventMode = "longconn"
		}
	}
	if s.larkRegCredPath != "" {
		if fileCfg, ok := larkreg.LoadConfig(s.larkRegCredPath); ok {
			if fileCfg.AppID != "" {
				eff.AppID = fileCfg.AppID
			}
			if fileCfg.AppSecret != "" {
				eff.AppSecret = fileCfg.AppSecret
			}
			if fileCfg.VerifyToken != "" {
				eff.VerifyToken = fileCfg.VerifyToken
			}
			if fileCfg.EncryptKey != "" {
				eff.EncryptKey = fileCfg.EncryptKey
			}
			if fileCfg.EventMode != "" {
				eff.EventMode = fileCfg.EventMode
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"app_id":           eff.AppID,
		"event_mode":       eff.EventMode,
		"registered":       eff.AppID != "",
		"secret_set":       eff.AppSecret != "",
		"verify_token_set": eff.VerifyToken != "",
		"encrypt_key_set":  eff.EncryptKey != "",
	})
}

// larkRegConfigUpdate 处理 POST /api/larkreg/config — 仅内网。
// 手工配置飞书渠道凭据。合并语义：空字段回退保留现有已存值
// （避免每次重输 secret）。保存后经 larkConfigApplier 热应用。
func (s *Server) larkRegConfigUpdate(c *gin.Context) {
	if s.larkRegCredPath == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "lark config path not configured"})
		return
	}
	var body struct {
		AppID       string `json:"app_id"`
		AppSecret   string `json:"app_secret"`
		VerifyToken string `json:"verify_token"`
		EncryptKey  string `json:"encrypt_key"`
		EventMode   string `json:"event_mode"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}

	// 合并语义：空字段回退到现有已存值。
	cfg, _ := larkreg.LoadConfig(s.larkRegCredPath)
	if body.AppID != "" {
		cfg.AppID = body.AppID
	}
	if body.AppSecret != "" {
		cfg.AppSecret = body.AppSecret
	}
	if body.VerifyToken != "" {
		cfg.VerifyToken = body.VerifyToken
	}
	if body.EncryptKey != "" {
		cfg.EncryptKey = body.EncryptKey
	}
	if body.EventMode != "" {
		cfg.EventMode = body.EventMode
	}
	if cfg.AppID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_id is required"})
		return
	}
	if cfg.AppSecret == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_secret is required"})
		return
	}
	if cfg.EventMode == "" {
		cfg.EventMode = "longconn"
	}
	if cfg.EventMode != "longconn" && cfg.EventMode != "webhook" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_mode must be longconn or webhook"})
		return
	}

	if err := larkreg.SaveConfig(s.larkRegCredPath, cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save config: " + err.Error()})
		return
	}

	// 热应用。applier 未接线（旧测试/未注入）时仅落盘，需重启生效。
	restartRequired := true
	msg := "已保存，重启 Pieqi 生效"
	if s.larkConfigApplier != nil {
		var err error
		restartRequired, err = s.larkConfigApplier(cfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":            "apply config: " + err.Error(),
				"saved":            true,
				"applied":          false,
				"restart_required": true,
			})
			return
		}
		if restartRequired {
			msg = "已生效，接入方式切换需重启 Pieqi"
		} else {
			msg = "已生效"
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"applied":          true,
		"restart_required": restartRequired,
		"message":          msg,
	})
}
