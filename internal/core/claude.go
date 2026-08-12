package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"claude-bridge/internal/model"
)

// Runner 管理 Claude Code 子进程调用
type Runner struct {
	WorkDir      string
	Model        string
	Effort       string
	Timeout      time.Duration
	SystemPrompt string // 注入到每次 -p 调用中
}

// NewRunner 创建 Claude Runner
func NewRunner(workDir, model, effort string, timeout time.Duration, systemPrompt string) *Runner {
	return &Runner{
		WorkDir:      workDir,
		Model:        model,
		Effort:       effort,
		Timeout:      timeout,
		SystemPrompt: systemPrompt,
	}
}

// Run 执行一次 Claude 对话。
// permissionMode: "plan" | "bypassPermissions" | "" (默认)
func (r *Runner) Run(ctx context.Context, prompt string, sessionID string, permissionMode string) (*model.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	start := time.Now()

	args := []string{
		"-p", prompt,
		"--session-id", sessionID,
		"--model", r.Model,
	}
	if r.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", r.SystemPrompt)
	}
	if permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = r.WorkDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}()

	// 读取 stdout（JSON 输出）
	var output strings.Builder
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		output.WriteString(scanner.Text())
	}

	// 读取 stderr（错误日志）
	var errBuf strings.Builder
	errScanner := bufio.NewScanner(stderr)
	for errScanner.Scan() {
		errBuf.WriteString(errScanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("claude timeout after %v", r.Timeout)
		}
		return nil, fmt.Errorf("claude error: %w\nstdout: %s\nstderr: %s", err, output.String(), errBuf.String())
	}

	duration := time.Since(start).Milliseconds()
	result := &model.Result{
		Output:    strings.TrimSpace(output.String()),
		SessionID: sessionID,
		Duration:  duration,
	}

	// 尝试解析 JSON（claude 的 --output-format json 输出）
	var parsed struct {
		Result string `json:"result"`
		Usage  struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(result.Output), &parsed) == nil && parsed.Result != "" {
		result.Output = parsed.Result
		result.TokensIn = parsed.Usage.InputTokens
		result.TokensOut = parsed.Usage.OutputTokens
	}

	return result, nil
}
