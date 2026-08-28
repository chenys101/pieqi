package claude

import (
	"context"
	"errors"

	"pieqi/internal/agent"
	"pieqi/internal/agent/claude/bridge"
	"pieqi/internal/config"

	"go.uber.org/zap"
)

// Config claude agent 的配置（multi-agent.md §4.2）。
//
//	agents:
//	  claude:
//	    transport: sdk-bridge      # 默认主力
//	    bridge:
//	      base_url: "http://127.0.0.1:18790"
//	      token: ""                # 桥鉴权 token（与 bridge BRIDGE_TOKEN 对应）
//	    fallback:
//	      transport: print          # bridge 连续失败时
//	    print:
//	      command: "claude"
type Config struct {
	Transport string            // "sdk-bridge"（默认）| "print"；print 时跳过桥直接走 claude -p 回退
	BaseURL   string            // bridge 地址；空用默认 http://127.0.0.1:18790
	Token     string            // bridge 鉴权 token；空 = 不鉴权
	Print     agent.PrintConfig // bridge 不可用时的 print 回退配置
	Logger    *zap.Logger
}

// ConfigFromAgents 把 config.AgentsConfig 的 claude 节转成 provider 配置（main.go 接线用）。
func ConfigFromAgents(a config.AgentsConfig) Config {
	return Config{
		Transport: a.Claude.Transport,
		BaseURL:   a.Claude.Bridge.BaseURL,
		Token:     a.Claude.Bridge.Token,
		Print: agent.PrintConfig{
			Binary:         a.Claude.Print.Command,
			PermissionMode: a.Claude.Print.PermissionMode,
			Model:          a.Claude.Print.Model,
			SysPrompt:      a.Claude.Print.SysPrompt,
		},
	}
}

var defaultConfig = Config{BaseURL: bridge.DefaultBaseURL, Transport: "sdk-bridge"}

// Configure 设置 claude agent 的默认配置（app 启动时调用；可多次覆盖）。
// 只覆盖非零字段，保留先前设置。
func Configure(c Config) {
	if c.Transport != "" {
		defaultConfig.Transport = c.Transport
	}
	if c.BaseURL != "" {
		defaultConfig.BaseURL = c.BaseURL
	}
	if c.Token != "" {
		defaultConfig.Token = c.Token
	}
	if c.Print.Binary != "" {
		defaultConfig.Print.Binary = c.Print.Binary
	}
	if c.Print.PermissionMode != "" {
		defaultConfig.Print.PermissionMode = c.Print.PermissionMode
	}
	if c.Print.Model != "" {
		defaultConfig.Print.Model = c.Print.Model
	}
	if c.Print.SysPrompt != "" {
		defaultConfig.Print.SysPrompt = c.Print.SysPrompt
	}
	if c.Logger != nil {
		defaultConfig.Logger = c.Logger
	}
}

// init 注册 "claude" 的 AgentSession 工厂：业务层 agent.Open(Agent:"claude") 即可。
// provider 在每次 Open 时读当前 defaultConfig，因此 Configure 可在启动期任意时刻调用。
func init() {
	agent.RegisterSessionProvider("claude", func(ctx context.Context, p agent.OpenParams) (agent.AgentSession, error) {
		return openSession(ctx, defaultConfig, p)
	})
}

// openSession 创建 claude AgentSession：按 transport 路由。
//   - sdk-bridge（默认）：优先 bridge；桥不可达/建会话失败回退 print。
//   - print：直接走 claude -p 回退，不碰桥。
func openSession(ctx context.Context, cfg Config, p agent.OpenParams) (agent.AgentSession, error) {
	if cfg.Transport == "print" {
		return openPrintFallback(ctx, cfg.Print, p)
	}
	client := bridge.NewClientWithToken(cfg.BaseURL, cfg.Token)
	if err := client.Health(ctx); err != nil {
		if cfg.Logger != nil {
			cfg.Logger.Warn("claude: bridge health failed, falling back to print",
				zap.String("base_url", cfg.BaseURL), zap.Error(err))
		}
		return openPrintFallback(ctx, cfg.Print, p)
	}
	if cfg.Logger != nil {
		cfg.Logger.Debug("claude: creating bridge session", zap.String("cwd", p.Cwd), zap.String("resume_from", p.ResumeFrom))
	}
	id, err := client.CreateSession(ctx, bridge.CreateSessionRequest{
		Cwd:                p.Cwd,
		ResumeSdkSessionID: p.ResumeFrom,
	})
	if err != nil {
		return openPrintFallback(ctx, cfg.Print, p)
	}
	sess := newSession(client, id, p.Cwd, cfg.Logger)
	go sess.runEventLoop()
	return sess, nil
}

// openPrintFallback 桥不可用时的 print 兜底（multi-agent.md §7）。
// 用现有 PrintAgent + sessionAdapter 桥接成 AgentSession，接口不变。
func openPrintFallback(ctx context.Context, pc agent.PrintConfig, p agent.OpenParams) (agent.AgentSession, error) {
	pa := agent.NewPrintAgent(pc, nil)
	sid, err := pa.NewSession(ctx, agent.SessionConfig{Cwd: p.Cwd, ResumeFrom: p.ResumeFrom})
	if err != nil {
		return nil, err
	}
	return agent.NewSessionAdapter(pa, sid, agent.Caps{
		MultiTurnPersistent: false,
		ResumeSupported:     true,
		Streaming:           false,
	}), nil
}

// ErrNoBridge 桥不可达且无 print 回退可用的错误（诊断用）。
var ErrNoBridge = errors.New("claude: bridge unreachable")
