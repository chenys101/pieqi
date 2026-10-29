package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"pieqi/internal/config"

	acp "github.com/coder/acp-go-sdk"
	"go.uber.org/zap"
)

// cursorAuthError 未登录时的固定文案（任务 failed 路径会原样 surface 给用户）。
const cursorAuthError = "Cursor 未登录：请本机执行 agent login，或设置 CURSOR_API_KEY"

// cursorLoginMethodID 是 initialize.authMethods[].id 的实测值（docs/notes/cursor-acp-p0.md）。
const cursorLoginMethodID = "cursor_login"

// isCursorACP 判这份 ACP 配置是否在驱动 Cursor Agent CLI。
//
// 判据：AgentType 为 agent/cursor-agent/cursor，或 spawn 形如 `agent acp` /
// `cursor-agent acp`（Cursor 不用 `--acp`）。
func isCursorACP(cfg config.ACPConfig) bool {
	switch strings.ToLower(strings.TrimSpace(cfg.AgentType)) {
	case "agent", "cursor-agent", "cursor":
		return true
	}
	if len(cfg.SpawnCommand) >= 2 {
		base := cursorSpawnBase(cfg.SpawnCommand[0])
		if (base == "agent" || base == "cursor-agent") && cfg.SpawnCommand[1] == "acp" {
			return true
		}
	}
	return false
}

func cursorSpawnBase(name string) string {
	base := strings.ToLower(filepath.Base(name))
	for _, suf := range []string{".exe", ".cmd", ".bat", ".ps1", ".com"} {
		base = strings.TrimSuffix(base, suf)
	}
	return base
}

// appendCursorEnv 把本机已有的 CURSOR_API_KEY / CURSOR_AUTH_TOKEN 注入子进程 env。
// 有才注入；不造空值，也不在这里做登录。
func appendCursorEnv(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	for _, key := range []string{"CURSOR_API_KEY", "CURSOR_AUTH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env
}

type acpAuthenticator interface {
	Authenticate(ctx context.Context, params acp.AuthenticateRequest) (acp.AuthenticateResponse, error)
}

// authenticateAfterInit Cursor：authMethods 空 ⇒ 已就绪；非空 ⇒ Authenticate(cursor_login)。
// 失败返回固定可读错误，调用方应 failed 任务而不是挂起。
func (a *ACPAgent) authenticateAfterInit(ctx context.Context, initResp acp.InitializeResponse) error {
	if !isCursorACP(a.cfg) {
		return nil
	}
	ids := cursorAuthMethodIDs(initResp.AuthMethods)
	if len(ids) == 0 {
		a.logger.Debug("acp cursor: authMethods empty, treat as ready")
		return nil
	}
	methodID := cursorLoginMethodID
	if !containsString(ids, methodID) {
		methodID = ids[0]
	}
	auth, ok := a.conn.(acpAuthenticator)
	if !ok {
		return fmt.Errorf("%s（连接不支持 Authenticate）", cursorAuthError)
	}
	a.logger.Debug("acp cursor authenticate", zap.String("methodId", methodID), zap.Strings("authMethods", ids))
	if _, err := auth.Authenticate(ctx, acp.AuthenticateRequest{MethodId: methodID}); err != nil {
		a.logger.Warn("acp cursor authenticate failed", zap.Error(err))
		return fmt.Errorf("%s", cursorAuthError)
	}
	return nil
}

func cursorAuthMethodIDs(methods []acp.AuthMethod) []string {
	var ids []string
	for _, m := range methods {
		if m.Agent != nil && m.Agent.Id != "" {
			ids = append(ids, m.Agent.Id)
		}
		if m.EnvVar != nil && m.EnvVar.Id != "" {
			ids = append(ids, m.EnvVar.Id)
		}
		if m.Terminal != nil && m.Terminal.Id != "" {
			ids = append(ids, m.Terminal.Id)
		}
	}
	return ids
}

func containsString(ss []string, x string) bool {
	for _, s := range ss {
		if s == x {
			return true
		}
	}
	return false
}

// cursorLocalAppDataFallbacks 返回 %LOCALAPPDATA%\cursor-agent\ 下的常见包装脚本。
// Cursor 装的是 .cmd/.ps1（不是 .exe），通用 spawnFallbackPaths 的 .exe 后缀对不上。
func cursorLocalAppDataFallbacks(name string) []string {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return nil
	}
	root := filepath.Join(local, "cursor-agent")
	base := cursorSpawnBase(name)
	if base != "agent" && base != "cursor-agent" {
		return nil
	}
	var out []string
	for _, n := range []string{base, "agent", "cursor-agent"} {
		for _, ext := range []string{".cmd", ".ps1", ".exe"} {
			out = append(out, filepath.Join(root, n+ext))
		}
	}
	return out
}

// HandleExtensionMethod 实现 acp.ExtensionMethodHandler（仅 `_` 前缀会进这里）。
// Cursor 实测发的是无下划线的 `cursor/*`，由 cursorExtBridge 拦截；此处覆盖
// `_cursor/*` 与其它未知 `_` 扩展，保证永不裸奔。
func (a *ACPAgent) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error) {
	return a.handleCursorExtension(ctx, method, params, true)
}

var _ acp.ExtensionMethodHandler = (*ACPAgent)(nil)

func (a *ACPAgent) handleCursorExtension(_ context.Context, method string, params json.RawMessage, asRequest bool) (any, error) {
	norm := strings.TrimPrefix(method, "_")
	toolCallID := peekJSONString(params, "toolCallId")
	a.logger.Debug("acp extension method",
		zap.String("method", method),
		zap.String("toolCallId", toolCallID),
		zap.Bool("request", asRequest),
		zap.String("agent", a.cfg.AgentType),
	)

	switch norm {
	case "cursor/ask_question":
		a.logger.Debug("acp extension default reply",
			zap.String("method", method), zap.String("reply", "skipped/unsupported_by_client"))
		return map[string]any{"outcome": "skipped", "reason": "unsupported_by_client"}, nil
	case "cursor/create_plan":
		a.logger.Debug("acp extension default reply",
			zap.String("method", method), zap.String("reply", "cancelled"))
		return map[string]any{"outcome": "cancelled"}, nil
	case "cursor/generate_image":
		// 生图由 Cursor agent 完成；客户端只做产物门禁：
		// filePath 落在本任务 workDir 内且文件存在 → 合成 GenerateImage 写事件进产物链；
		// 宿主临时目录（会话 assets 等）一律不拷、不登记（过程媒体）。
		a.promoteCursorGenerateImage(params)
		if !asRequest {
			return nil, nil
		}
		a.logger.Debug("acp extension default reply",
			zap.String("method", method), zap.String("reply", "accepted"))
		return map[string]any{"outcome": "accepted"}, nil
	case "cursor/update_todos", "cursor/task", "cursor/list_available_models":
		if !asRequest {
			a.logger.Debug("acp extension notification swallowed", zap.String("method", method))
			return nil, nil
		}
		a.logger.Debug("acp extension default reply",
			zap.String("method", method), zap.String("reply", "accepted"))
		return map[string]any{"outcome": "accepted"}, nil
	default:
		a.logger.Warn("acp unknown extension method", zap.String("method", method))
		return nil, acp.NewMethodNotFound(method)
	}
}

// cursorGenerateImageToolTitle 合成进事件流的工具名；须落在
// core.toolFileOperations 的 GenerateImage 条目上，才能派生 FileChange → 产物。
const cursorGenerateImageToolTitle = "GenerateImage"

// promoteCursorGenerateImage 把 in-worktree 的生图结果接到既有产物链。
//
// 判据（与 ADR-0018 对齐，不另造登记表）：
//   - 有 workDir；
//   - params.filePath（或 filename）解析后落在 workDir 内；
//   - 磁盘上存在且是普通文件；
// 否则只记 debug，不发事件、不拷贝。
func (a *ACPAgent) promoteCursorGenerateImage(params json.RawMessage) {
	if a == nil {
		return
	}
	workDir := strings.TrimSpace(a.workDir)
	rawPath := peekJSONString(params, "filePath")
	if rawPath == "" {
		rawPath = peekJSONString(params, "filename")
	}
	if workDir == "" || rawPath == "" {
		a.logger.Debug("acp generate_image skip promote",
			zap.String("reason", "missing workDir or filePath"),
			zap.Bool("hasWorkDir", workDir != ""),
			zap.String("filePath", rawPath),
		)
		return
	}

	abs := rawPath
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workDir, filepath.FromSlash(rawPath))
	}
	abs = filepath.Clean(abs)
	if !cursorPathWithin(workDir, abs) {
		a.logger.Debug("acp generate_image skip promote",
			zap.String("reason", "outside workDir"),
			zap.String("filePath", abs),
			zap.String("workDir", workDir),
		)
		return
	}
	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() {
		a.logger.Debug("acp generate_image skip promote",
			zap.String("reason", "missing or not a file"),
			zap.String("filePath", abs),
			zap.Error(err),
		)
		return
	}
	rel, err := filepath.Rel(workDir, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		a.logger.Debug("acp generate_image skip promote",
			zap.String("reason", "rel path escape"),
			zap.String("filePath", abs),
			zap.Error(err),
		)
		return
	}
	rel = filepath.ToSlash(rel)

	toolCallID := peekJSONString(params, "toolCallId")
	if toolCallID == "" {
		toolCallID = "generate-image:" + rel
	}
	rawIn, err := json.Marshal(map[string]string{"file_path": rel})
	if err != nil {
		return
	}

	a.cbMu.RLock()
	fn := a.onToolCall
	a.cbMu.RUnlock()
	if fn == nil {
		a.logger.Debug("acp generate_image skip promote",
			zap.String("reason", "no OnToolCallUpdate"),
			zap.String("path", rel),
		)
		return
	}
	a.logger.Debug("acp generate_image promote artifact",
		zap.String("toolCallId", toolCallID),
		zap.String("path", rel),
	)
	fn(ToolCallUpdateInfo{
		ToolCallID: toolCallID,
		Title:      cursorGenerateImageToolTitle,
		Status:     "pending",
		Kind:       "other",
		IsNew:      true,
		RawInput:   rawIn,
	})
	fn(ToolCallUpdateInfo{
		ToolCallID: toolCallID,
		Title:      cursorGenerateImageToolTitle,
		Status:     "completed",
		Kind:       "other",
		IsNew:      false,
		RawInput:   rawIn,
	})
}

// cursorPathWithin p 是否落在 root 之内（Clean 后按分隔符比较，防目录穿越）。
func cursorPathWithin(root, p string) bool {
	ar, err1 := filepath.Abs(root)
	ap, err2 := filepath.Abs(p)
	if err1 != nil || err2 != nil {
		return false
	}
	ar, ap = filepath.Clean(ar), filepath.Clean(ap)
	if strings.EqualFold(ar, ap) {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(strings.ToLower(ap), strings.ToLower(ar+sep))
}

func peekJSONString(raw json.RawMessage, key string) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// cursorExtBridge 夹在 agent stdout 与 SDK reader 之间：
// 拦截无 `_` 前缀的 `cursor/*` JSON-RPC **请求**，立刻写 L0 应答到 agent stdin，
// 不让它们掉进 SDK handle() 的 MethodNotFound（虽然 Cursor 对 ask_question 有回退，
// 但主动应答更稳，也覆盖没有回退的扩展）。
//
// 通知（无 id）直接吞掉并记日志。
type cursorExtBridge struct {
	inner   *bufio.Reader
	reply   io.Writer
	replyMu *sync.Mutex
	agent   *ACPAgent

	mu   sync.Mutex
	pass []byte // 已决定放行给 SDK 的字节
}

func newCursorExtBridge(r io.Reader, reply io.Writer, replyMu *sync.Mutex, a *ACPAgent) *cursorExtBridge {
	return &cursorExtBridge{
		inner:   bufio.NewReader(r),
		reply:   reply,
		replyMu: replyMu,
		agent:   a,
	}
}

func (b *cursorExtBridge) Read(p []byte) (int, error) {
	for {
		b.mu.Lock()
		if len(b.pass) > 0 {
			n := copy(p, b.pass)
			b.pass = b.pass[n:]
			b.mu.Unlock()
			return n, nil
		}
		b.mu.Unlock()

		line, err := b.inner.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			return 0, err
		}
		if b.tryIntercept(line) {
			if err != nil {
				return 0, err
			}
			continue
		}
		b.mu.Lock()
		b.pass = append(b.pass, line...)
		b.mu.Unlock()
		if err != nil && len(line) > 0 {
			// 有尾部数据则先交给上层，下次再返回 err
			continue
		}
		if err != nil {
			return 0, err
		}
	}
}

func (b *cursorExtBridge) tryIntercept(line []byte) bool {
	trim := bytes.TrimSpace(line)
	if len(trim) == 0 || trim[0] != '{' {
		return false
	}
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(trim, &msg); err != nil {
		return false
	}
	if msg.Method == "" {
		return false
	}
	if !isCursorExtMethod(msg.Method) {
		return false
	}
	asRequest := len(bytes.TrimSpace(msg.ID)) > 0
	result, herr := b.agent.handleCursorExtension(context.Background(), msg.Method, msg.Params, asRequest)
	if !asRequest {
		return true
	}
	var resp any
	if herr != nil {
		if re, ok := herr.(*acp.RequestError); ok {
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      jsonRawOrNull(msg.ID),
				"error":   re,
			}
		} else {
			resp = map[string]any{
				"jsonrpc": "2.0",
				"id":      jsonRawOrNull(msg.ID),
				"error":   map[string]any{"code": -32603, "message": herr.Error()},
			}
		}
	} else {
		resp = map[string]any{
			"jsonrpc": "2.0",
			"id":      jsonRawOrNull(msg.ID),
			"result":  result,
		}
	}
	payload, err := json.Marshal(resp)
	if err != nil {
		return true
	}
	payload = append(payload, '\n')
	b.replyMu.Lock()
	_, _ = b.reply.Write(payload)
	b.replyMu.Unlock()
	return true
}

func isCursorExtMethod(method string) bool {
	m := strings.TrimPrefix(method, "_")
	return strings.HasPrefix(m, "cursor/")
}

func jsonRawOrNull(id json.RawMessage) any {
	if len(bytes.TrimSpace(id)) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(id, &v); err != nil {
		return nil
	}
	return v
}

// lockedWriter 让 SDK 写 stdin 与 cursorExtBridge 回包共用一把锁，避免交叉写烂一行。
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
