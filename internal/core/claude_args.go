package core

// claude_args.go 抽出 SessionRunner 与 TaskRunner 共用的 claude CLI 参数构造逻辑。

// buildOneShotArgs 构造首条消息的 claude -p 参数（创建会话）。
func buildOneShotArgs(prompt, sessionID, model, sysPrompt, permissionMode string) []string {
	args := []string{
		"-p", prompt,
		"--session-id", sessionID,
		"--model", model,
	}
	if sysPrompt != "" {
		args = append(args, "--append-system-prompt", sysPrompt)
	}
	if permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	}
	return args
}
