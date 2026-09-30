//go:build windows

package core

import (
	"errors"
	"syscall"
)

// Windows 系统错误码。只为三个常量把 golang.org/x/sys 从 indirect 提升为直接
// 依赖不划算，就地定义（本文件已按平台分文件，不会污染其他平台）。
const (
	errAccessDenied     = syscall.Errno(5)  // ERROR_ACCESS_DENIED
	errSharingViolation = syscall.Errno(32) // ERROR_SHARING_VIOLATION
	errLockViolation    = syscall.Errno(33) // ERROR_LOCK_VIOLATION
)

// isTransientRenameErr 判断 os.Rename 的失败是否属于「瞬时占用」而非真错误。
//
// Windows 上 MoveFileEx(REPLACE_EXISTING) 会在**没有任何并发写入**的情况下
// 间歇性返回 ERROR_ACCESS_DENIED：目标文件刚落盘就被杀软/索引器（Defender、
// Search Indexer）短暂打开持有。实测单线程纯循环 400 次里稳定失败 3~5 次
// （temp 与非 temp 目录都有），且**每次隔 1ms 重试即成功**（9/9）。
//
// 这三种 errno 与并发撞车（同进程或两个 pieqi 实例共用同一个 tmp）的表现一致，
// 都属于「等一会儿再来」能解决的，值得重试；其余错误（磁盘满、路径不存在、
// 权限真的不对）重试无意义，直接上报。
func isTransientRenameErr(err error) bool {
	var eno syscall.Errno
	if !errors.As(err, &eno) {
		return false
	}
	switch eno {
	case errSharingViolation, // 32：被别的句柄占着（并发 tmp 撞车）
		errAccessDenied,  // 5：被杀软/索引器瞬时持有
		errLockViolation: // 33：文件被锁
		return true
	}
	return false
}
