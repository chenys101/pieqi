package agent

import (
	"net/url"
	"strings"
)

// LocalExecuteCommand 判断整条 shell 是否「只含本机回环探活 + 已可 L0/L1 分档的段」。
//
// 见 docs/adr/0020-local-execute-optional-auto-approve.md：
// 打开 Settings.auto_approve_local_execute 后，命中本函数才免审 ——
// 不是给整档 execute 开闸。
func LocalExecuteCommand(command string) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false
	}
	// 只拦写向重定向 / 命令替换。不跑 permissionReqHasUnsafeText：
	// 那张表把 Invoke-WebRequest 整词当危险，会把「本机回环探活」自己废掉（ADR-0020）。
	// 外网 / 删除等靠下面逐段 localExecuteSegment + ClassifyShellCommand 拒绝。
	if hasUnsafeRedirection(cmd) {
		return false
	}
	seen := false
	for _, seg := range splitSegments(cmd) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		seen = true
		if localExecuteSegment(seg) {
			continue
		}
		// 已能证明 read/edit 的段（cd / ls / go test…）不挡整条。
		if kind, ok := ClassifyShellCommand(seg); ok {
			if kindSeverity[kind] < kindSeverity["execute"] {
				continue
			}
		}
		return false
	}
	return seen
}

// localExecuteSegment 单段是否是「只打回环」的 HTTP 客户端。
func localExecuteSegment(seg string) bool {
	words := strings.Fields(stripDataLiterals(seg))
	i := 0
	for i < len(words) {
		w := words[i]
		if strings.HasPrefix(w, "/dev/null") {
			i++
			continue
		}
		if strings.Contains(w, "=") && !strings.HasPrefix(w, "-") {
			i++
			continue
		}
		if _, ok := commandWrappers[baseCommandName(w)]; ok {
			i++
			continue
		}
		break
	}
	if i >= len(words) {
		return false
	}
	name := baseCommandName(words[i])
	args := words[i+1:]
	switch name {
	case "curl", "wget":
		return httpClientOnlyLoopback(args)
	case "invoke-webrequest", "invoke-restmethod":
		return httpClientOnlyLoopback(args)
	default:
		return false
	}
}

// httpClientOnlyLoopback：参数里至少有一个 URL，且**每一个** URL 主机都是回环。
func httpClientOnlyLoopback(args []string) bool {
	sawURL := false
	for _, a := range args {
		host, ok := urlHost(a)
		if !ok {
			// PowerShell: -Uri http://127.0.0.1/...
			if strings.Contains(strings.ToLower(a), "://") {
				if h, ok2 := urlHost(a); ok2 {
					host, ok = h, true
				}
			}
		}
		if !ok {
			continue
		}
		sawURL = true
		if !isLoopbackHost(host) {
			return false
		}
	}
	return sawURL
}

func urlHost(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	// 允许无 scheme 的 127.0.0.1:3000/path —— curl 也常见。
	if !strings.Contains(raw, "://") {
		if strings.HasPrefix(raw, "127.0.0.1") || strings.HasPrefix(raw, "localhost") ||
			strings.HasPrefix(raw, "[::1]") || strings.HasPrefix(raw, "::1") {
			raw = "http://" + raw
		} else {
			return "", false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	host := u.Hostname()
	if host == "" {
		return "", false
	}
	return host, true
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}
