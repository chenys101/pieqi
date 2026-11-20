package core

import (
	"fmt"
	"strings"

	"pieqi/internal/model"
)

func CanPublish(d *model.PlanDraft) error {
	if d == nil {
		return fmt.Errorf("plan draft is nil")
	}
	if errs := PublishBlockers(d); len(errs) > 0 {
		return fmt.Errorf("publish blocked: %s", strings.Join(errs, "; "))
	}
	return nil
}

func PublishBlockers(d *model.PlanDraft) []string {
	if d == nil {
		return []string{"plan draft is nil"}
	}
	var errs []string
	for _, a := range d.Assertions {
		kind := strings.TrimSpace(a.Kind)
		text := strings.TrimSpace(a.Text)
		label := text
		if label == "" {
			label = a.ID
		}
		switch kind {
		case model.AssertOpenQuestion:
			if a.BlocksPublish || !a.AcceptedRisk {
				errs = append(errs, "open_question blocks publish: "+label)
			}
		case model.AssertAssumption:
			if a.BlocksPublish && !a.AcceptedRisk {
				errs = append(errs, "assumption needs accepted_risk: "+label)
			}
		}
	}
	if len(d.Nodes) == 0 {
		errs = append(errs, "at least one node required")
		return errs
	}
	ids := make(map[string]struct{}, len(d.Nodes))
	for _, n := range d.Nodes {
		id := strings.TrimSpace(n.NodeID)
		if id == "" {
			errs = append(errs, "node missing node_id")
			continue
		}
		if _, dup := ids[id]; dup {
			errs = append(errs, "duplicate node_id: "+id)
		}
		ids[id] = struct{}{}
		if strings.TrimSpace(n.Title) == "" && strings.TrimSpace(n.Prompt) == "" {
			errs = append(errs, "node "+id+": title or prompt required")
		}
		if strings.TrimSpace(string(n.Role)) == "" {
			errs = append(errs, "node "+id+": role required")
		}
	}
	for _, n := range d.Nodes {
		id := strings.TrimSpace(n.NodeID)
		for _, dep := range n.DependsOnNodes {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if _, ok := ids[dep]; !ok {
				errs = append(errs, "node "+id+": depends_on unknown node "+dep)
			}
		}
	}
	return errs
}
