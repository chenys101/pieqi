package core

import (
	"os"
	"testing"
)

// 真实形态（本机 WorkBuddy 会话注入值）
const hostShimOpt = `--require="D:/program_dev/ide/workbuddy/resources/app.asar.unpacked/cli/vendor/shim/node-language-shim.cjs"`

func TestSanitizeInheritedNodeOptions(t *testing.T) {
	cases := []struct {
		name        string
		set         bool
		in          string
		wantRemoved int
		wantAfter   string // 期望清理后的值；"\x00unset" 表示变量应被删除
	}{
		{
			name:        "仅宿主 shim → 整变量删除",
			set:         true,
			in:          hostShimOpt,
			wantRemoved: 1,
			wantAfter:   "\x00unset",
		},
		{
			name:        "shim + 合法选项 → 只删 shim，保留其余",
			set:         true,
			in:          "--max-old-space-size=4096 " + hostShimOpt,
			wantRemoved: 1,
			wantAfter:   "--max-old-space-size=4096",
		},
		{
			name:        "反斜杠路径形态同样识别",
			set:         true,
			in:          `--require="C:\workbuddy\resources\app.asar.unpacked\cli\vendor\shim\node-language-shim.cjs"`,
			wantRemoved: 1,
			wantAfter:   "\x00unset",
		},
		{
			name:        "无 NODE_OPTIONS → 不动",
			set:         false,
			in:          "",
			wantRemoved: 0,
			wantAfter:   "\x00unset",
		},
		{
			name:        "无关的 NODE_OPTIONS → 原样保留",
			set:         true,
			in:          "--max-old-space-size=2048 --enable-source-maps",
			wantRemoved: 0,
			wantAfter:   "--max-old-space-size=2048 --enable-source-maps",
		},
		{
			name:        "用户自己的 --require（非 shim）不能被误删",
			set:         true,
			in:          "--require=./my-local-hooks.cjs",
			wantRemoved: 0,
			wantAfter:   "--require=./my-local-hooks.cjs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("NODE_OPTIONS", tc.in)
			} else {
				t.Setenv("NODE_OPTIONS", "")
				if err := os.Unsetenv("NODE_OPTIONS"); err != nil {
					t.Fatalf("unset: %v", err)
				}
			}

			removed := SanitizeInheritedNodeOptions()
			if len(removed) != tc.wantRemoved {
				t.Fatalf("removed=%d (%q)，期望 %d", len(removed), removed, tc.wantRemoved)
			}

			after, exists := os.LookupEnv("NODE_OPTIONS")
			if tc.wantAfter == "\x00unset" {
				if exists {
					t.Fatalf("期望 NODE_OPTIONS 被删除，实际存在: %q", after)
				}
				return
			}
			if !exists {
				t.Fatalf("期望 NODE_OPTIONS=%q，实际已被删除", tc.wantAfter)
			}
			if after != tc.wantAfter {
				t.Fatalf("NODE_OPTIONS=%q，期望 %q", after, tc.wantAfter)
			}
		})
	}
}

// splitShellTokens 必须保留引号内的空格，否则会把带空格的 shim 路径切碎而识别失败。
func TestSplitShellTokens(t *testing.T) {
	got := splitShellTokens(`--require="C:/a b/c.cjs" --max-old-space-size=4096`)
	want := []string{`--require="C:/a b/c.cjs"`, "--max-old-space-size=4096"}
	if len(got) != len(want) {
		t.Fatalf("tokens=%q，期望 %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token[%d]=%q，期望 %q", i, got[i], want[i])
		}
	}
}
