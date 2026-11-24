package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

// acpErrorText 给错误加可读前缀，同时**保留 Unwrap 链**。
//
// 为什么不用 fmt.Errorf("%s: %w")：SDK 的 *acp.RequestError.Error() 是紧凑 JSON，
// 直接 %w 会把 {"code":-32001,"message":...} 原样写进 task.Error 和界面，
// 用户看到一坨机器码，还得自己去猜哪段是要读的。这里只改**呈现**，
// 不改语义：errors.Is / errors.As 一律照旧穿透（ErrPromptCancelled、
// context.Canceled、ErrSessionNotFound 的判定都不受影响）。
type acpErrorText struct {
	prefix string
	err    error
}

func (e *acpErrorText) Error() string { return e.prefix + ": " + describeACPError(e.err) }

func (e *acpErrorText) Unwrap() error { return e.err }

// wrapACPErrText 包装错误为「prefix: 可读文本」，同时保留错误链。
func wrapACPErrText(prefix string, err error) error {
	if err == nil {
		return nil
	}
	return &acpErrorText{prefix: prefix, err: err}
}

// describeACPError 把 JSON-RPC 错误渲染成人类能读的一行。
//
// 形如 `code -32001: RetriableError: account quota exhausted…（context deadline exceeded）`。
// 不是 *acp.RequestError（或只是普通 error）时原样返回，不做任何猜测。
//
// 刻意**不按 code 分流**：错误码是各 agent / 代理自己的方言，pieqi 只负责
// 把它显示清楚，不据此决定重试或等人（那是调用方与 agent 侧的职责边界）。
func describeACPError(err error) string {
	if err == nil {
		return "<nil>"
	}
	var re *acp.RequestError
	if !errors.As(err, &re) || re == nil {
		return err.Error()
	}
	out := fmt.Sprintf("code %d: %s", re.Code, re.Message)
	if detail := requestErrorDetail(re); detail != "" {
		out += "（" + detail + "）"
	}
	return out
}

// requestErrorDetail 从 RequestError.Data 里捞一句人话。
//
// ACP 没有规定 Data 的形状，实际见到两种：
//   - cursor：{"details":"Session <uuid> not found"} / {"error":"session lost"}
//   - 代理（cursor-extra）：额度类会把原因塞进 data.error
//
// 优先取字符串字段，取不到就整体压成一行 JSON —— 宁可长一点，不要丢掉现场。
func requestErrorDetail(re *acp.RequestError) string {
	if re.Data == nil {
		return ""
	}
	if m, ok := re.Data.(map[string]any); ok {
		for _, k := range []string{"details", "detail", "error", "message"} {
			if s, ok := m[k].(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					return s
				}
			}
		}
	}
	if s, ok := re.Data.(string); ok {
		return strings.TrimSpace(s)
	}
	b, err := json.Marshal(re.Data)
	if err != nil {
		return fmt.Sprintf("%v", re.Data)
	}
	return string(b)
}
