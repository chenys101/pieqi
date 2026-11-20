package core

import (
	"strings"

	"pieqi/internal/model"
)

const (
	continueAnchorHead = "继续执行当前任务，从上次中断的地方接着做。" +
		"不要查找其它会话、历史记录、agent-transcripts 或工作区日志，也不要切换到别的任务。"
	continueTaskCap   = 500
	continueRecentCap = 800
)

// continuePromptForFreshSession 在 ACP 对端历史不可用时，把用户续问文案钉到本任务上。
//
// 热保活 / session/load 成功时不要调用：那时模型已有对话历史，原样转发即可。
// 无历史时若只发「继续」，agent 会去翻别的 transcript/记忆猜任务（实测串台）。
func continuePromptForFreshSession(t *model.Task, userText string) string {
	userText = strings.TrimSpace(userText)
	if t == nil {
		return userText
	}
	var b strings.Builder
	b.WriteString(continueAnchorHead)
	if task := strings.TrimSpace(t.Prompt); task != "" {
		b.WriteString("\n原任务：「")
		b.WriteString(truncateRunes(task, continueTaskCap))
		b.WriteString("」")
	}
	if recent := recentTaskContext(t, continueRecentCap); recent != "" {
		b.WriteString("\n本会话近期要点：\n")
		b.WriteString(recent)
	}
	if userText != "" {
		b.WriteString("\n当前请求：")
		b.WriteString(userText)
	}
	return b.String()
}

// recentTaskContext 从 pieqi 事件账本抽一小段近期正文，供无对端历史时锚定。
func recentTaskContext(t *model.Task, budget int) string {
	if t == nil || budget <= 0 || len(t.Events) == 0 {
		return ""
	}
	var parts []string
	used := 0
	for i := len(t.Events) - 1; i >= 0 && used < budget; i-- {
		ev := t.Events[i]
		var who, text string
		switch ev.Type {
		case model.EventUser:
			who, text = "用户", strings.TrimSpace(ev.Text)
		case model.EventText:
			who, text = "助手", strings.TrimSpace(ev.Text)
		default:
			continue
		}
		if text == "" {
			continue
		}
		// 跳过纯续问口头禅，避免「继续」自我引用占预算。
		if who == "用户" && isBareContinue(text) {
			continue
		}
		line := who + "：" + truncateRunes(text, 200)
		if used+len(line) > budget && len(parts) > 0 {
			break
		}
		parts = append([]string{line}, parts...)
		used += len(line) + 1
		if len(parts) >= 4 {
			break
		}
	}
	return strings.Join(parts, "\n")
}

func isBareContinue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "继续", "继续。", "继续!", "继续！", "continue", "continue.", "接着做", "接着干":
		return true
	}
	return false
}
