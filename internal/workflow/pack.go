// Package workflow 需求开发流程包（pack）：提示词与闸门可外置，引擎原语仍在 core。
package workflow

import (
	"fmt"
	"strings"

	"pieqi/internal/model"
)

// 内置 id；旧需求无 workflow_id 时回落 s2-dag（贴近历史拆分行为）。
const (
	IDSingle     = "s0-single"
	IDPlanSerial = "s1-plan-serial"
	IDDAG        = "s2-dag"
	DefaultVersion = "1"
)

// Pack 一份可版本化的需求 loop 配方。
type Pack struct {
	ID          string   `yaml:"id" json:"id"`
	Version     string   `yaml:"version" json:"version"`
	Label       string   `yaml:"label" json:"label"`
	ShortLabel  string   `yaml:"short_label" json:"short_label,omitempty"`
	Description string   `yaml:"description" json:"description"`
	Tags        []string `yaml:"tags" json:"tags,omitempty"`
	UISteps     []UIStep `yaml:"ui_steps" json:"ui_steps,omitempty"`
	Gates       Gates    `yaml:"gates" json:"gates"`
	// SplitInstructions 写入拆分助手 prompt（哲学/粒度）；JSON schema 仍由引擎固定。
	SplitInstructions string `yaml:"split_instructions" json:"-"`
	BriefHeader       string `yaml:"brief_header" json:"-"`
	DoDImplement      string `yaml:"dod_implement" json:"-"`
	DoDTest           string `yaml:"dod_test" json:"-"`
}

// UIStep 详情页步骤条。
type UIStep struct {
	ID    string `yaml:"id" json:"id"`
	Label string `yaml:"label" json:"label"`
}

// Gates confirm / 关单相关策略（可配；安全硬边界仍在代码）。
type Gates struct {
	MaxNodes                int    `yaml:"max_nodes" json:"max_nodes"`
	MaxParallelImplement    int    `yaml:"max_parallel_implement" json:"max_parallel_implement"`
	ForbidFeBeHardSplit     bool   `yaml:"forbid_fe_be_hard_split" json:"forbid_fe_be_hard_split"`
	RequireACEvidenceToDone bool   `yaml:"require_ac_evidence_to_done" json:"require_ac_evidence_to_done"`
	PathBias                string `yaml:"path_bias" json:"path_bias,omitempty"`
	RequireBriefInPrompt    bool   `yaml:"require_brief_in_prompt" json:"require_brief_in_prompt"`
}

// Summary API 列表用（不含长文案）。
type Summary struct {
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	Label       string   `json:"label"`
	ShortLabel  string   `json:"short_label,omitempty"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	UISteps     []UIStep `json:"ui_steps,omitempty"`
	Gates       Gates    `json:"gates"`
	Builtin     bool     `json:"builtin"`
	IsDefault   bool     `json:"is_default,omitempty"`
}

// Detail 只读查看用（含模板正文）。
type Detail struct {
	Summary
	SplitInstructions string `json:"split_instructions,omitempty"`
	BriefHeader       string `json:"brief_header,omitempty"`
	DoDImplement      string `json:"dod_implement,omitempty"`
	DoDTest           string `json:"dod_test,omitempty"`
}

// SummaryOf 转列表项。
func (p *Pack) Summary(builtin bool) Summary {
	if p == nil {
		return Summary{}
	}
	return Summary{
		ID:          p.ID,
		Version:     p.EffectiveVersion(),
		Label:       p.Label,
		ShortLabel:  p.ShortLabel,
		Description: p.Description,
		Tags:        append([]string(nil), p.Tags...),
		UISteps:     append([]UIStep(nil), p.UISteps...),
		Gates:       p.Gates,
		Builtin:     builtin,
	}
}

// DetailOf 含模板。
func (p *Pack) DetailOf(builtin bool) Detail {
	s := p.Summary(builtin)
	return Detail{
		Summary:           s,
		SplitInstructions: p.SplitInstructions,
		BriefHeader:       p.BriefHeader,
		DoDImplement:      p.DoDImplement,
		DoDTest:           p.DoDTest,
	}
}

// EffectiveVersion 空则 DefaultVersion。
func (p *Pack) EffectiveVersion() string {
	if p == nil {
		return DefaultVersion
	}
	v := strings.TrimSpace(p.Version)
	if v == "" {
		return DefaultVersion
	}
	return v
}

// ValidatePack 结构校验。
func ValidatePack(p *Pack) error {
	if p == nil {
		return fmt.Errorf("nil pack")
	}
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return fmt.Errorf("pack id required")
	}
	p.ID = id
	if strings.TrimSpace(p.Label) == "" {
		return fmt.Errorf("pack %s: label required", id)
	}
	if p.Gates.MaxNodes < 0 {
		return fmt.Errorf("pack %s: max_nodes < 0", id)
	}
	if p.Gates.MaxParallelImplement < 0 {
		return fmt.Errorf("pack %s: max_parallel_implement < 0", id)
	}
	if strings.TrimSpace(p.Version) == "" {
		p.Version = DefaultVersion
	}
	if p.ShortLabel == "" {
		// 从 id 粗取短标：s1-plan-serial → S1
		parts := strings.SplitN(p.ID, "-", 2)
		if len(parts) > 0 && parts[0] != "" {
			p.ShortLabel = strings.ToUpper(parts[0])
		}
	}
	return nil
}

// ValidateProposalGates 按 pack 闸门校验拆分提案（confirm 前）。
// skipMaxNodes：超节点警告场景下由调用方 force 跳过硬拦。
func ValidateProposalGates(p *Pack, nodes []model.ProposedTaskNode) error {
	return ValidateProposalGatesOpts(p, nodes, false)
}

// ValidateProposalGatesOpts forceSkipMaxNodes=true 时不拦 max_nodes（仍拦 FE/BE 与并行）。
func ValidateProposalGatesOpts(p *Pack, nodes []model.ProposedTaskNode, forceSkipMaxNodes bool) error {
	if p == nil {
		return nil
	}
	if !forceSkipMaxNodes && p.Gates.MaxNodes > 0 && len(nodes) > p.Gates.MaxNodes {
		return fmt.Errorf("workflow %s: too many nodes (%d > max_nodes %d); pass force=true to confirm anyway", p.ID, len(nodes), p.Gates.MaxNodes)
	}
	if p.Gates.ForbidFeBeHardSplit {
		hasFE, hasBE := false, false
		for _, n := range nodes {
			switch n.Role {
			case model.RoleFrontend:
				hasFE = true
			case model.RoleBackend:
				hasBE = true
			}
		}
		if hasFE && hasBE {
			return fmt.Errorf("workflow %s: forbid FE/BE hard split — merge into implement tickets or pick s2-dag", p.ID)
		}
	}
	if lim := p.Gates.MaxParallelImplement; lim > 0 {
		wave := 0
		for _, n := range nodes {
			if !isImplementRole(n.Role) {
				continue
			}
			if implementDependsOnlyOnNonImplement(n, nodes) {
				wave++
			}
		}
		if wave > lim {
			return fmt.Errorf("workflow %s: first-wave implement tickets %d > max_parallel_implement %d", p.ID, wave, lim)
		}
	}
	return nil
}

// NodesExceedMax 是否超过建议节点上限（警告用；硬拦由 ValidateProposalGates 在 force 路径外决定）。
func NodesExceedMax(p *Pack, n int) bool {
	if p == nil || p.Gates.MaxNodes <= 0 {
		return false
	}
	return n > p.Gates.MaxNodes
}

func isImplementRole(r model.TaskRole) bool {
	switch r {
	case model.RoleDesign, model.RoleReview, model.RoleTest:
		return false
	default:
		return true
	}
}

// IsImplementRole 导出给 core 调度用。
func IsImplementRole(r model.TaskRole) bool { return isImplementRole(r) }

// implementDependsOnlyOnNonImplement：首波可并行实现票 = 不依赖其它实现票。
func implementDependsOnlyOnNonImplement(n model.ProposedTaskNode, all []model.ProposedTaskNode) bool {
	byID := make(map[string]model.ProposedTaskNode, len(all))
	for _, x := range all {
		byID[x.TempID] = x
	}
	for _, d := range n.DependsOn {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		dep, ok := byID[d]
		if !ok {
			continue
		}
		if isImplementRole(dep.Role) {
			return false
		}
	}
	return true
}
