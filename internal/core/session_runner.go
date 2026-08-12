package core

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// SessionRunner 管理每个 Session 的 Claude 调用。
// 首条消息用 claude -p 创建会话，后续消息用 claude --resume 复用会话上下文。
type SessionRunner struct {
	logger    *zap.Logger
	workDir   string
	model     string
	sysPrompt string
	timeout   time.Duration

	mu       sync.Mutex
	sessions map[string]time.Time
}

func NewSessionRunner(logger *zap.Logger, workDir, model, sysPrompt string, timeout time.Duration) *SessionRunner {
	return &SessionRunner{
		logger:    logger,
		workDir:   workDir,
		model:     model,
		sysPrompt: sysPrompt,
		timeout:   timeout,
		sessions:  make(map[string]time.Time),
	}
}

// Run 执行一次 Claude 对话。
func (sr *SessionRunner) Run(ctx context.Context, prompt, sessionID, permissionMode string) (string, error) {
	sr.mu.Lock()
	_, exists := sr.sessions[sessionID]
	if !exists {
		sr.sessions[sessionID] = time.Now()
	}
	sr.mu.Unlock()

	if !exists {
		return sr.oneShot(ctx, prompt, sessionID, permissionMode)
	}

	output, err := sr.resumeShot(ctx, prompt, sessionID, permissionMode)
	if err != nil {
		return "", err
	}

	sr.mu.Lock()
	sr.sessions[sessionID] = time.Now()
	sr.mu.Unlock()

	return output, nil
}

// oneShot: claude -p 创建会话
func (sr *SessionRunner) oneShot(ctx context.Context, prompt, sessionID, mode string) (string, error) {
	args := []string{"-p", prompt, "--session-id", sessionID, "--model", sr.model}
	if sr.sysPrompt != "" {
		args = append(args, "--append-system-prompt", sr.sysPrompt)
	}
	if mode != "" {
		args = append(args, "--permission-mode", mode)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = sr.workDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("claude error: %w\noutput: %s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// resumeShot: claude --resume + pipe + EOF → 复用会话上下文
func (sr *SessionRunner) resumeShot(ctx context.Context, prompt, sessionID, mode string) (string, error) {
	args := []string{"--resume", sessionID, "--model", sr.model}
	if sr.sysPrompt != "" {
		args = append(args, "--append-system-prompt", sr.sysPrompt)
	}
	if mode != "" {
		args = append(args, "--permission-mode", mode)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = sr.workDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start claude --resume: %w", err)
	}

	fmt.Fprintf(stdin, "%s\n", prompt)
	stdin.Close()

	out, err := io.ReadAll(stdout)
	if err != nil {
		return "", fmt.Errorf("read --resume stdout: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("claude --resume error: %w\noutput: %s", err, string(out))
	}

	return strings.TrimSpace(string(out)), nil
}

func (sr *SessionRunner) Shutdown() {}

func (sr *SessionRunner) ReapIdle(ttl time.Duration) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	cutoff := time.Now().Add(-ttl)
	for id, lastUsed := range sr.sessions {
		if lastUsed.Before(cutoff) {
			delete(sr.sessions, id)
		}
	}
}
