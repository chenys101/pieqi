package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"pieqi/internal/core"
	"pieqi/internal/larkreg"
	"pieqi/internal/model"

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
	// sysPrompt 是本次创建携带的预设提示词（D2：跟随 bot 记录）。
	// 由 /start 的可选 body 传入，poll 成功后写入新建的 Bot。
	sysPrompt string
	// botID 是本次创建落的 bot 记录 id（幂等标记：poll 重复调用只创建一次）。
	botID string
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
	// 可选 body：{"sys_prompt": "..."} —— 预设提示词随本次创建一起带给新机器人。
	// body 缺失 / 非 JSON 都视为「不带提示词」，不阻断扫码流程。
	var body struct {
		SysPrompt string `json:"sys_prompt"`
	}
	_ = c.ShouldBindJSON(&body)

	// 重置状态(只允许同时一个进行中的 flow)
	s.larkRegState.mu.Lock()
	s.larkRegState.done = false
	s.larkRegState.appID = ""
	s.larkRegState.appSecret = ""
	s.larkRegState.err = ""
	s.larkRegState.qrURL = ""
	s.larkRegState.startedAt = time.Now()
	s.larkRegState.sysPrompt = body.SysPrompt
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
	// 多机器人落点（D1/D2）：除上面的单例凭据（渠道 bootstrap 用，行为不变）外，
	// 再落一条 bot 记录 + per-bot 凭据。前端会重复轮询，故用状态里的 botID 做**幂等**。
	botWarn := ""
	if s.bots != nil && s.larkRegState.botID == "" && s.larkRegState.appID != "" {
		bot, err := s.bots.Create(model.Bot{
			Channel:   model.ChannelLark,
			SysPrompt: s.larkRegState.sysPrompt,
			AppID:     s.larkRegState.appID,
			// Name / Role 留空：由 BotStore 按「首个绑定自动 admin」规则推导
		})
		if err != nil {
			// 不阻断：凭据已落盘、渠道可用；bot 记录失败只降级提示。
			botWarn = "机器人记录创建失败: " + err.Error()
		} else {
			s.larkRegState.botID = bot.ID
			// per-bot 凭据文件（含 secret，0600）。写失败只影响该 bot 的独立凭据，
			// 不影响已生效的单例凭据，故不阻断也不升级为错误。
			if cfg, ok := larkreg.LoadConfig(s.larkRegCredPath); ok {
				_ = larkreg.SaveConfig(s.bots.CredsPath(bot.ID), cfg)
			}
			// 新增了一台 → 让渠道控制器建实例。apply 路径稍后也会重建一次
			// （差量重建，第二次是 no-op），这里显式触发是为了不把
			// "新机器人必须真的开始收发"这件事挂在不相关的热应用步骤上。
			s.notifyBotChanged()
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
	if botWarn != "" {
		hint = botWarn
	}
	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"app_id": s.larkRegState.appID,
		"bot_id": s.larkRegState.botID,
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
		SysPrompt   string `json:"sys_prompt"`
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

	// 落 bot 记录（幂等键 = 渠道 + app_id）。
	// 手动配置与扫码**是同一件事的两种达成方式**：都要得到"一台可用的机器人"。
	// 放在这里而不是让前端再发一次 POST /api/bots —— 否则会出现
	// "配置保存成功、机器人却没建出来"的中间态，而用户在界面上看不出来。
	botID, botWarn := "", ""
	if id, created, err := s.upsertBotForApp(cfg, body.SysPrompt); err != nil {
		botWarn = "机器人记录写入失败: " + err.Error()
	} else if id != "" {
		botID = id
		if created {
			// 新增了一台 → 让渠道控制器建实例（否则它只存在于列表里，不收发消息）。
			s.notifyBotChanged()
		}
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
	if botWarn != "" {
		msg = botWarn
	}
	c.JSON(http.StatusOK, gin.H{
		"applied":          true,
		"restart_required": restartRequired,
		"message":          msg,
		"bot_id":           botID,
	})
}

// upsertBotForApp 由「手动配置」路径调用：把一次成功的配置保存落成一台机器人。
//
// 幂等键是 (channel, app_id)：同一个应用反复保存只更新那一台，不新增。
// 这条很关键 —— 这个端点同时承担"新建"和"改凭据"两种用途（合并语义），
// 没有幂等键的话，用户每改一次 secret 就会多出一台机器人。
//
// 凭据写在该机器人**自己**的路径上（BotStore.CredsPath），与其他机器人隔离。
// 新建时若凭据落盘失败，会撤销刚建的记录 —— 不留"有记录、无凭据"的坏状态
// （那种机器人会让控制器每次重建都跳过它，而用户看到它明明在列表里）。
//
// 返回 (botID, created, err)。BotStore 未接线时返回 ("", false, nil)：视为
// "这次只改了渠道级配置"，不是错误。
func (s *Server) upsertBotForApp(cfg larkreg.ChannelConfig, sysPrompt string) (string, bool, error) {
	if s.bots == nil || cfg.AppID == "" {
		return "", false, nil
	}
	if existing, ok := s.bots.FindByAppID(model.ChannelLark, cfg.AppID); ok {
		// 提示词用「非空即覆盖」：与端点其余字段的合并语义一致
		// （空 = 用户没填，保持原值，不当作"清空"）。
		if sysPrompt != "" && sysPrompt != existing.SysPrompt {
			p := sysPrompt
			if _, err := s.bots.Update(existing.ID, core.BotPatch{SysPrompt: &p}); err != nil {
				return "", false, err
			}
		}
		// 凭据也刷新一次（可能只改了 secret / event_mode）。
		if err := larkreg.SaveConfig(s.bots.CredsPath(existing.ID), cfg); err != nil {
			return "", false, err
		}
		return existing.ID, false, nil
	}
	bot, err := s.bots.Create(model.Bot{
		Channel:   model.ChannelLark,
		SysPrompt: sysPrompt,
		AppID:     cfg.AppID,
		// Name / Role 留空：由 BotStore 按「首个绑定自动 admin」规则推导
	})
	if err != nil {
		return "", false, err
	}
	if err := larkreg.SaveConfig(s.bots.CredsPath(bot.ID), cfg); err != nil {
		_ = s.bots.Delete(bot.ID)
		return "", false, err
	}
	return bot.ID, true, nil
}
