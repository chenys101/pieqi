package core

import (
	"fmt"
	"strings"

	"pieqi/internal/model"
)

// ValidateSplitProposal fail-closed 校验首次拆分提案（ADR-0021 §3.4）。
//
// DependsOn 只能指向本提案内 TempID；有未 deferred 的 AC 时须被本提案覆盖。
func ValidateSplitProposal(req *model.Requirement, p *model.SplitProposal) error {
	return validateProposal(req, p, nil)
}

// ValidateExpandProposal 校验渐进 expand：DependsOn 可指向提案 TempID 或同需求已有 Task ID；
// AC 由「已有 Task.covers_ac ∪ 提案」联合覆盖。
func ValidateExpandProposal(req *model.Requirement, p *model.SplitProposal, existing []*model.Task) error {
	return validateProposal(req, p, existing)
}

func validateProposal(req *model.Requirement, p *model.SplitProposal, existing []*model.Task) error {
	if p == nil {
		return fmt.Errorf("nil proposal")
	}
	if len(p.Nodes) == 0 {
		return fmt.Errorf("proposal has no nodes")
	}
	ids := make(map[string]struct{}, len(p.Nodes))
	for i, n := range p.Nodes {
		tid := strings.TrimSpace(n.TempID)
		if tid == "" {
			return fmt.Errorf("node[%d]: temp_id required", i)
		}
		if _, dup := ids[tid]; dup {
			return fmt.Errorf("duplicate temp_id %q", tid)
		}
		ids[tid] = struct{}{}
		if strings.TrimSpace(n.Prompt) == "" {
			return fmt.Errorf("node %q: prompt required", tid)
		}
	}
	existingIDs := make(map[string]struct{})
	for _, t := range existing {
		if t != nil && t.ID != "" {
			existingIDs[t.ID] = struct{}{}
		}
	}
	for _, n := range p.Nodes {
		for _, dep := range n.DependsOn {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if dep == n.TempID {
				return fmt.Errorf("node %q: self-dependency", n.TempID)
			}
			_, inProposal := ids[dep]
			_, inExisting := existingIDs[dep]
			if !inProposal && !inExisting {
				return fmt.Errorf("node %q: depends_on unknown id %q", n.TempID, dep)
			}
		}
	}
	if err := detectCycle(p.Nodes); err != nil {
		return err
	}
	if err := checkTestDependsOn(p.Nodes); err != nil {
		return err
	}
	if req != nil {
		if err := checkACCoverage(req.Acceptance, p.Nodes, existing); err != nil {
			return err
		}
	}
	return nil
}

func checkACCoverage(acs []model.AcceptanceItem, nodes []model.ProposedTaskNode, existing []*model.Task) error {
	need := make(map[string]struct{})
	for _, ac := range acs {
		if ac.Deferred || strings.TrimSpace(ac.ID) == "" {
			continue
		}
		need[ac.ID] = struct{}{}
	}
	if len(need) == 0 {
		return nil // 零 AC 或全 deferred：允许挂靠
	}
	covered := make(map[string]struct{})
	for _, t := range existing {
		if t == nil {
			continue
		}
		for _, ac := range t.CoversAC {
			ac = strings.TrimSpace(ac)
			if ac != "" {
				covered[ac] = struct{}{}
			}
		}
	}
	for _, n := range nodes {
		for _, ac := range n.CoversAC {
			ac = strings.TrimSpace(ac)
			if ac != "" {
				covered[ac] = struct{}{}
			}
		}
	}
	var missing []string
	for id := range need {
		if _, ok := covered[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("uncovered acceptance: %s", strings.Join(missing, ", "))
	}
	return nil
}

func detectCycle(nodes []model.ProposedTaskNode) error {
	adj := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		// 只在提案内边上看环；依赖已有 Task 的边不参与环检测（已存在节点不会在提案里成环）
		var deps []string
		ids := make(map[string]struct{}, len(nodes))
		for _, x := range nodes {
			ids[x.TempID] = struct{}{}
		}
		for _, d := range n.DependsOn {
			d = strings.TrimSpace(d)
			if _, ok := ids[d]; ok {
				deps = append(deps, d)
			}
		}
		adj[n.TempID] = deps
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(nodes))
	var visit func(string) error
	visit = func(u string) error {
		color[u] = gray
		for _, v := range adj[u] {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			switch color[v] {
			case gray:
				return fmt.Errorf("dependency cycle involving %q", v)
			case white:
				if err := visit(v); err != nil {
					return err
				}
			}
		}
		color[u] = black
		return nil
	}
	for _, n := range nodes {
		if color[n.TempID] == white {
			if err := visit(n.TempID); err != nil {
				return err
			}
		}
	}
	return nil
}
