package agent

import "strings"

// Go 工具链分档（2026-10-31）：把本仓库最高频的 L2 噪音（go test / go build）
// 从"一律弹卡"拉进可证明的 read/edit，与 ADR-0015 的 npm 分档同取向。
//
// 判据：只认 `go <subcommand>`，按子命令分档；认不出 / 带危险标志 → 不降级。
// 不按 `go *` 前缀通配——`go run` / `go get` / `go install` 仍是任意代码或联网写。

// goKindFor 判断一段是否是可分档的 `go …` 命令。
//
// 入参 words 从真正的命令名起（已剥 wrapper/赋值）。第二返回值 false =
// 不归本层或证明不了 → 调用方维持 execute。
func goKindFor(words []string) (string, bool) {
	if len(words) == 0 || baseCommandName(words[0]) != "go" {
		return "", false
	}
	sub, rest, ok := goSubcommand(words[1:])
	if !ok {
		return "", false
	}
	switch sub {
	case "test", "vet", "list", "env", "version", "help", "doc":
		// `go test -exec` 会把测试二进制交给任意程序跑，副作用不可证。
		if sub == "test" && hasAnyWord(rest, []string{"-exec"}) {
			return "", false
		}
		return "read", true
	case "build", "generate", "fmt":
		// build/generate 写可重生成产物；fmt 默认就改源文件 → 都归 L1(edit)。
		return "edit", true
	case "mod":
		return goModKindFor(rest)
	default:
		// run / get / install / clean / tool / work / bug … 一律人工。
		return "", false
	}
}

// goSubcommand 跳过 go 的全局旗标（`-C dir`、`-overlay` 等），取出子命令与剩余参数。
func goSubcommand(args []string) (sub string, rest []string, ok bool) {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-C" || a == "-overlay":
			i += 2 // 旗标 + 值；值缺失时 i 越界，下面统一判失败
			continue
		case strings.HasPrefix(a, "-C=") || strings.HasPrefix(a, "-overlay="):
			i++
			continue
		case strings.HasPrefix(a, "-"):
			// 其它全局旗标（少见）；跳过旗标本身，值形态的一律当无值旗标。
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return "", nil, false
	}
	return args[i], args[i+1:], true
}

// goModKindFor 只放行只读 mod 子命令；tidy/download/vendor/edit 写模块图或缓存 → 人工。
func goModKindFor(rest []string) (string, bool) {
	nonFlag := nonFlagWords(rest)
	if len(nonFlag) == 0 {
		return "", false
	}
	switch nonFlag[0] {
	case "graph", "why", "verify":
		return "read", true
	default:
		return "", false
	}
}
