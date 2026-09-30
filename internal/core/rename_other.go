//go:build !windows

package core

// isTransientRenameErr 非 Windows 平台恒为 false：POSIX 的 rename(2) 是原子替换，
// 不存在「目标文件被瞬时打开导致改名失败」这回事（见 rename_windows.go 的说明）。
func isTransientRenameErr(error) bool {
	return false
}
