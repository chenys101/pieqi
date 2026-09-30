//go:build windows

package core

import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestIsTransientRenameErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"access denied", &os.LinkError{Op: "rename", Err: errAccessDenied}, true},
		{"sharing violation", &os.LinkError{Op: "rename", Err: errSharingViolation}, true},
		{"lock violation", &os.LinkError{Op: "rename", Err: errLockViolation}, true},
		// ERROR_PATH_NOT_FOUND：路径没了，重试多少次都一样，必须立刻上报。
		{"path not found", &os.LinkError{Op: "rename", Err: syscall.Errno(3)}, false},
		{"non errno", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := isTransientRenameErr(c.err); got != c.want {
			t.Errorf("%s: isTransientRenameErr = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestRenameWithRetry 确定性覆盖重试策略（不依赖真实时序/杀软）。
func TestRenameWithRetry(t *testing.T) {
	t.Run("瞬时失败被重试救回", func(t *testing.T) {
		calls := 0
		err := renameWithRetry(func() error {
			calls++
			if calls <= 2 {
				return &os.LinkError{Op: "rename", Err: errAccessDenied}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if calls != 3 {
			t.Fatalf("calls = %d, want 3（前两次瞬时失败后应重试）", calls)
		}
	})

	t.Run("非瞬时错误不重试", func(t *testing.T) {
		calls := 0
		want := &os.LinkError{Op: "rename", Err: syscall.Errno(3)}
		err := renameWithRetry(func() error { calls++; return want })
		if !errors.Is(err, want) {
			t.Fatalf("err = %v, want %v", err, want)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1（非瞬时错误必须立刻返回）", calls)
		}
	})

	t.Run("持续被占用则用尽次数并报错", func(t *testing.T) {
		calls := 0
		err := renameWithRetry(func() error {
			calls++
			return &os.LinkError{Op: "rename", Err: errSharingViolation}
		})
		if err == nil {
			t.Fatal("err = nil, want 最后一次错误")
		}
		if !isTransientRenameErr(err) {
			t.Fatalf("err = %v, want 原始瞬时错误（不能被包装成别的）", err)
		}
		if calls != renameAttempts {
			t.Fatalf("calls = %d, want %d", calls, renameAttempts)
		}
	})
}
