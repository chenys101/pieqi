package core

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/zap"
)

// SkillInfo 一个 Claude skill 的摘要。
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Dir         string `json:"dir"`
}

// SkillScanner 扫描 Claude Code 的 .claude/skills/ 目录，读 SKILL.md frontmatter。
// 单一真相源：移动端与 Agent 调用的同一套 skill。
type SkillScanner struct {
	logger    *zap.Logger
	skillsDirs []string
}

// NewSkillScanner 创建扫描器。skillsDirs 空则用默认：$CLAUDE_CONFIG_DIR/.claude/skills + ~/.claude/skills。
func NewSkillScanner(logger *zap.Logger, skillsDirs []string) *SkillScanner {
	if len(skillsDirs) == 0 {
		skillsDirs = defaultClaudeDirs("skills")
	}
	return &SkillScanner{logger: logger, skillsDirs: skillsDirs}
}

// Scan 扫描所有 skillsDirs，返回去重后的 skill 列表（按 name 排序）。
// 解析符号链接，读每个子目录的 SKILL.md frontmatter。
func (s *SkillScanner) Scan() []SkillInfo {
	seen := map[string]SkillInfo{}
	for _, dir := range s.skillsDirs {
		s.scanDir(dir, seen)
	}
	out := make([]SkillInfo, 0, len(seen))
	for _, sk := range seen {
		out = append(out, sk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *SkillScanner) scanDir(dir string, seen map[string]SkillInfo) {
	// 解符号链接
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillDir := filepath.Join(dir, e.Name())
		skillMD := filepath.Join(skillDir, "SKILL.md")
		info, err := parseSkillMD(skillMD)
		if err != nil || info.Name == "" {
			// SKILL.md 缺失或无 name：用目录名兜底
			info.Name = e.Name()
		}
		info.Dir = skillDir
		// 去重：同名 skill 后扫到的覆盖前者（保持顺序优先）
		if _, exists := seen[info.Name]; !exists {
			seen[info.Name] = info
		}
	}
}

// parseSkillMD 读 SKILL.md 的 YAML frontmatter，提取 name 与 description。
func parseSkillMD(path string) (SkillInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillInfo{}, err
	}
	content := string(data)
	if !strings.HasPrefix(content, "---") {
		return SkillInfo{Name: filepath.Base(filepath.Dir(path))}, nil
	}
	// 提取第一个 --- 与第二个 --- 之间的 frontmatter
	rest := strings.TrimPrefix(content, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return SkillInfo{Name: filepath.Base(filepath.Dir(path))}, nil
	}
	fm := rest[:end]
	info := SkillInfo{}
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			info.Name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			// 去引号
			info.Name = strings.Trim(info.Name, `"'`)
		} else if strings.HasPrefix(line, "description:") {
			info.Description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			info.Description = strings.Trim(info.Description, `"'`)
		}
	}
	return info, nil
}
