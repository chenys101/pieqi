package core

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestSkillScanner_Scan(t *testing.T) {
	dir := t.TempDir()
	// skill a: 完整 frontmatter
	dirA := filepath.Join(dir, "skill-a")
	os.MkdirAll(dirA, 0755)
	os.WriteFile(filepath.Join(dirA, "SKILL.md"), []byte("---\nname: skill-a\ndescription: does thing a\n---\n# body\n"), 0644)
	// skill b: 缺 description
	dirB := filepath.Join(dir, "skill-b")
	os.MkdirAll(dirB, 0755)
	os.WriteFile(filepath.Join(dirB, "SKILL.md"), []byte("---\nname: skill-b\n---\n"), 0644)
	// skill c: 无 SKILL.md（用目录名兜底）
	os.MkdirAll(filepath.Join(dir, "skill-c"), 0755)
	// 非目录文件应被跳过
	os.WriteFile(filepath.Join(dir, "loose.md"), []byte("x"), 0644)

	scanner := NewSkillScanner(zap.NewNop(), []string{dir})
	skills := scanner.Scan()
	if len(skills) != 3 {
		t.Fatalf("got %d skills: %+v", len(skills), skills)
	}
	byName := map[string]SkillInfo{}
	for _, s := range skills {
		byName[s.Name] = s
	}
	if byName["skill-a"].Description != "does thing a" {
		t.Errorf("skill-a desc=%q", byName["skill-a"].Description)
	}
	if byName["skill-b"].Description != "" {
		t.Errorf("skill-b desc should be empty, got %q", byName["skill-b"].Description)
	}
	if byName["skill-c"].Name != "skill-c" {
		t.Errorf("skill-c name=%q", byName["skill-c"].Name)
	}
}

func TestSkillScanner_Dedup(t *testing.T) {
	// 两个目录含同名 skill：第一个优先
	dir1 := t.TempDir()
	os.MkdirAll(filepath.Join(dir1, "dup"), 0755)
	os.WriteFile(filepath.Join(dir1, "dup", "SKILL.md"), []byte("---\nname: dup\ndescription: from dir1\n---\n"), 0644)

	dir2 := t.TempDir()
	os.MkdirAll(filepath.Join(dir2, "dup"), 0755)
	os.WriteFile(filepath.Join(dir2, "dup", "SKILL.md"), []byte("---\nname: dup\ndescription: from dir2\n---\n"), 0644)

	scanner := NewSkillScanner(zap.NewNop(), []string{dir1, dir2})
	skills := scanner.Scan()
	if len(skills) != 1 {
		t.Fatalf("got %d, want 1", len(skills))
	}
	if skills[0].Description != "from dir1" {
		t.Fatalf("desc=%q, want from dir1 (first wins)", skills[0].Description)
	}
}

func TestSkillScanner_Symlink(t *testing.T) {
	real := t.TempDir()
	os.MkdirAll(filepath.Join(real, "sym-skill"), 0755)
	os.WriteFile(filepath.Join(real, "sym-skill", "SKILL.md"), []byte("---\nname: sym-skill\ndescription: real\n---\n"), 0644)

	link := t.TempDir()
	// 创建符号链接指向 real（可能需要权限，失败则 skip）
	if err := os.Symlink(real, filepath.Join(link, "linked")); err != nil {
		t.Skip("symlink not supported:", err)
	}
	// 扫描 link/linked（其下有 sym-skill）
	scanner := NewSkillScanner(zap.NewNop(), []string{filepath.Join(link, "linked")})
	skills := scanner.Scan()
	found := false
	for _, s := range skills {
		if s.Name == "sym-skill" {
			found = true
		}
	}
	if !found {
		t.Fatal("symlinked skill not scanned")
	}
}

func TestSkillScanner_MissingDir(t *testing.T) {
	scanner := NewSkillScanner(zap.NewNop(), []string{"/nonexistent/path/xyz"})
	if skills := scanner.Scan(); len(skills) != 0 {
		t.Fatalf("missing dir should yield 0, got %d", len(skills))
	}
}
