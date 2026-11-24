package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

// TestDescribeACPError 钉住「JSON-RPC 错误 → 人话」的呈现规则。
//
// 背景：SDK 的 RequestError.Error() 是紧凑 JSON，直接透出会让面板显示
// {"code":-32001,"message":"RetriableError: ..."}。2026-11-20 一周 8 次额度失败
// 在任务错误里就是这个样子。只改呈现，不按 code 分流。
func TestDescribeACPError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "cursor details 字段",
			err:  &acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"details": "Session abc not found"}},
			want: "code -32603: Internal error（Session abc not found）",
		},
		{
			name: "代理 error 字段",
			err:  &acp.RequestError{Code: -32001, Message: "RetriableError: account quota exhausted", Data: map[string]any{"error": "pool_exhausted; next_reset=2026-11-24T15:00:00+08:00"}},
			want: "code -32001: RetriableError: account quota exhausted（pool_exhausted; next_reset=2026-11-24T15:00:00+08:00）",
		},
		{
			name: "Data 是纯字符串",
			err:  &acp.RequestError{Code: -32000, Message: "RetriableError: Connection stalled", Data: "stall"},
			want: "code -32000: RetriableError: Connection stalled（stall）",
		},
		{
			name: "Data 无已知键 → 压成 JSON",
			err:  &acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"k": float64(1)}},
			want: `code -32603: Internal error（{"k":1}）`,
		},
		{
			name: "无 Data → 不加括号",
			err:  &acp.RequestError{Code: -32601, Message: "Method not found"},
			want: "code -32601: Method not found",
		},
		{
			name: "普通 error 原样",
			err:  errors.New("write |1: The pipe is being closed."),
			want: "write |1: The pipe is being closed.",
		},
		{
			name: "被 fmt.Errorf 包过也能认",
			err:  fmt.Errorf("acp: prompt: %w", &acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"error": "session lost"}}),
			want: "code -32603: Internal error（session lost）",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := describeACPError(tc.err); got != tc.want {
				t.Fatalf("describeACPError()=%q\nwant %q", got, tc.want)
			}
		})
	}
	if got := describeACPError(nil); got != "<nil>" {
		t.Fatalf("describeACPError(nil)=%q", got)
	}
}

// TestWrapACPErrText_PreservesChain 可读化**只改呈现**，不许动摇错误链。
//
// 三条判定都靠 errors.Is / 字符串匹配，改坏了就是"取消变失败""session 丢失不重建"：
//   - task_runner.go:1372 errors.Is(runErr, ErrPromptCancelled)
//   - task_runner.go:1382 IsSessionNotFound（对 session lost 放宽后）
//   - 返回给 API 的错误链（errors.As 取 *acp.RequestError）
func TestWrapACPErrText_PreservesChain(t *testing.T) {
	t.Run("ErrorsIs 穿透", func(t *testing.T) {
		err := wrapACPErrText("acp: prompt", fmt.Errorf("%w", ErrPromptCancelled))
		if !errors.Is(err, ErrPromptCancelled) {
			t.Fatalf("ErrPromptCancelled 丢了: %v", err)
		}
	})

	t.Run("context.Canceled 穿透", func(t *testing.T) {
		err := wrapACPPromptErr("acp: prompt", context.Canceled)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("context.Canceled 丢了: %v", err)
		}
	})

	t.Run("As 取回 RequestError", func(t *testing.T) {
		inner := &acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"error": "session lost"}}
		err := wrapACPPromptErr("acp: prompt", inner)
		var re *acp.RequestError
		if !errors.As(err, &re) {
			t.Fatalf("拿不回 *acp.RequestError: %v", err)
		}
		if re.Code != -32603 {
			t.Fatalf("code=%d", re.Code)
		}
	})

	t.Run("session lost 仍可识别", func(t *testing.T) {
		err := wrapACPPromptErr("acp: prompt",
			&acp.RequestError{Code: -32603, Message: "Internal error", Data: map[string]any{"error": "session lost"}})
		if !IsSessionNotFound(err) {
			t.Fatalf("session lost 判不出来: %v", err)
		}
	})
}

// TestWrapACPPromptErr_TextIsReadable 端到端：面板上看到的必须是「码 + 人话」，
// 不能再是裸 JSON。
func TestWrapACPPromptErr_TextIsReadable(t *testing.T) {
	err := wrapACPPromptErr("acp: prompt",
		&acp.RequestError{Code: -32001, Message: "RetriableError: account quota exhausted, retry after account switch"})
	got := err.Error()
	if strings.Contains(got, `"code"`) || strings.Contains(got, `"message"`) {
		t.Fatalf("还是裸 JSON: %s", got)
	}
	if !strings.Contains(got, "-32001") || !strings.Contains(got, "account quota exhausted") {
		t.Fatalf("码或原文丢了: %s", got)
	}
}
