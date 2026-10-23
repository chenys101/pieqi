package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// default_project_path（新建任务缺省工作目录）的存储语义测试。
//
// 这里守的三条判据，每一条对应一个真实会踩的坑：
//  1. 用户没配过 → 出厂缺省生效（项目不再是必选项的基础）；
//  2. 用户显式清空 → **保持空**，重启后也不会被缺省填回来（否则清空是个假动作）；
//  3. 非法值（相对路径 / 不存在 / 不是目录）在**写入时**就 400，
//     而不是等到建任务时才失败 —— 那时成因（设置写错）离现象太远。

// TestSettings_DefaultProjectPath_Fallback 没配过时用出厂缺省。
func TestSettings_DefaultProjectPath_Fallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	s, err := NewSettingsStore(path)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if got := s.Get().DefaultProjectPath; got != "" {
		t.Fatalf("注入缺省之前应为空（缺省由 main.go 按 PIEQI_HOME 算），got %q", got)
	}

	ws := filepath.Join(dir, DefaultWorkspaceDirName)
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	s.SetFallbackProjectPath(ws)
	if got := s.Get().DefaultProjectPath; got != ws {
		t.Fatalf("未显式配置时应回落出厂缺省 %q, got %q", ws, got)
	}

	// 重启（重新加载同一份文件）：文件里没有这个键 → 继续用缺省。
	s2, err := NewSettingsStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	s2.SetFallbackProjectPath(ws)
	if got := s2.Get().DefaultProjectPath; got != ws {
		t.Fatalf("重启后仍应用缺省 %q, got %q", ws, got)
	}
}

// TestSettings_DefaultProjectPath_ExplicitClearSticks 显式清空后必须**保持**空。
//
// 这是"出厂缺省"与"用户意图"的分界。若失败：设置页里清空默认目录，重启后它又自己
// 回来了 —— 表现为"删除不生效"，而用户会反复去点那个清除按钮。
func TestSettings_DefaultProjectPath_ExplicitClearSticks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	ws := filepath.Join(dir, DefaultWorkspaceDirName)
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}

	s, err := NewSettingsStore(path)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	s.SetFallbackProjectPath(ws)

	empty := ""
	if _, err := s.Patch(SettingsPatch{DefaultProjectPath: &empty}); err != nil {
		t.Fatalf("清空应合法: %v", err)
	}
	if got := s.Get().DefaultProjectPath; got != "" {
		t.Fatalf("显式清空后应为空, got %q", got)
	}

	// 重启：缺省**不得**回填。
	s2, err := NewSettingsStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	s2.SetFallbackProjectPath(ws)
	if got := s2.Get().DefaultProjectPath; got != "" {
		t.Fatalf("显式清空后重启，缺省不得回填，got %q", got)
	}
}

// TestSettings_DefaultProjectPath_SetAndReject 合法值落盘、非法值被拒且不改内存态。
func TestSettings_DefaultProjectPath_SetAndReject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	s, err := NewSettingsStore(path)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	projectDir := filepath.Join(dir, "proj")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 1) 合法：落盘并可读回。
	if _, err := s.Patch(SettingsPatch{DefaultProjectPath: &projectDir}); err != nil {
		t.Fatalf("set: %v", err)
	}
	s2, err := NewSettingsStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := s2.Get().DefaultProjectPath; got != projectDir {
		t.Fatalf("落盘读回 = %q, want %q", got, projectDir)
	}

	// 2) 非法：相对路径 / 不存在 / 是文件 —— 一律拒绝，且内存态保持上一个合法值。
	filePath := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"relative/dir", filepath.Join(dir, "nope"), filePath} {
		bad := bad
		_, err := s.Patch(SettingsPatch{DefaultProjectPath: &bad})
		if err == nil {
			t.Fatalf("非法值 %q 应被拒绝", bad)
		}
		if got := s.Get().DefaultProjectPath; got != projectDir {
			t.Fatalf("被拒绝后内存态应保持不变，got %q want %q", got, projectDir)
		}
	}

	// 3) 错误文案要指得出是哪个字段 —— 这是设置页弹错误时唯一的信息来源。
	_, err = s.Patch(SettingsPatch{DefaultProjectPath: strPtr("relative/dir")})
	if err == nil || !strings.Contains(err.Error(), "default_project_path") {
		t.Fatalf("错误信息应包含字段名: %v", err)
	}
}

func strPtr(s string) *string { return &s }
