package core

import (
	"fmt"
	"strings"

	"pieqi/internal/model"
)

// 交付约定正文：拆分建议与 confirm 落地时注入（docs/notes/requirement-delivery.md）。

const deliveryDoDImplement = `

## pieqi 交付纪律（实现票 · 必须遵守）

1. 在本任务 worktree 施工；完成后 **merge 进本需求的集成分支**（需求分支），不要只停在 worktree。
2. **自测只允许单元/组件测试**（定向 go test / vitest）。禁止起 pieqi 服务、禁止完整链路/全包回归当完成本票条件。
3. 沙箱或测试环境失败 ⇒ 不得把任务标为 completed；留下失败证据。
4. 票面 completed 应对齐「已合入需求分支」，不是「会话跑完」。
`

const deliveryDoDTest = `

## pieqi 交付纪律（test 票 · 必须遵守）

1. **等 depends_on 全部完成且改动已合入需求分支** 后再做功能测试；禁止与未完成的实现票并行开跑。
2. 功能测试：检出需求分支 → 编译 → **另起隔离实例**验收（pieqi：独立 PIEQI_HOME + PIEQI_SERVER_PORT，禁止占用现网 :3000 / ~/.pieqi）。
3. 本票不做「只跑单测就报完成」；发现缺陷应让任务 failed。报告须含「总评：FAIL」（系统据此标 failed 并开 Issue），禁止只写 FAIL 却标 completed。
4. 不要用缩短审批超时、自动 approve 冒充修测试失败。
`

// AppendDeliveryDoD 按角色在 prompt 末尾追加交付纪律（已含标记则不重复）。
func AppendDeliveryDoD(role model.TaskRole, prompt string) string {
	return AppendDeliveryDoDFrom(role, prompt, deliveryDoDImplement, deliveryDoDTest)
}

// AppendDeliveryDoDFrom 使用自定义实现/test 文案（来自 Workflow Pack）；空则回落内置。
func AppendDeliveryDoDFrom(role model.TaskRole, prompt, dodImplement, dodTest string) string {
	const markerCN = "pieqi 交付纪律"
	const markerEN = "pieqi delivery"
	if strings.Contains(prompt, markerCN) || strings.Contains(prompt, markerEN) {
		return prompt
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return prompt
	}
	impl := dodImplement
	if strings.TrimSpace(impl) == "" {
		impl = deliveryDoDImplement
	}
	test := dodTest
	if strings.TrimSpace(test) == "" {
		test = deliveryDoDTest
	}
	switch role {
	case model.RoleTest:
		return prompt + test
	case model.RoleDesign, model.RoleReview:
		return prompt
	default:
		return prompt + impl
	}
}

// HasProgressedWork 同需求下是否存在已离开 planned 的工作票（覆盖拆分风险）。
func (s *RequirementStore) HasProgressedWork(requirementID string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	ts := s.tasks
	s.mu.Unlock()
	if ts == nil {
		return false
	}
	for _, id := range s.WorkTaskIDs(requirementID) {
		t, ok := ts.Get(id)
		if !ok || t == nil {
			continue
		}
		switch t.Status {
		case model.TaskPlanned, model.TaskCancelled:
			continue
		default:
			return true
		}
	}
	return false
}

// errReplaceNeedsForce ConfirmReplace 在有已推进工作时的默认拒绝。
var errReplaceNeedsForce = fmt.Errorf("requirement has progressed work tasks; pass force=true to abandon and replace")

// checkTestDependsOn 若提案含实现类节点，则每个 test 节点必须 depends_on 至少一个其它节点。
func checkTestDependsOn(nodes []model.ProposedTaskNode) error {
	hasImplement := false
	for _, n := range nodes {
		if isImplementRole(n.Role) {
			hasImplement = true
			break
		}
	}
	if !hasImplement {
		return nil
	}
	for _, n := range nodes {
		if n.Role != model.RoleTest {
			continue
		}
		if len(nonzeroDeps(n.DependsOn)) == 0 {
			return fmt.Errorf("test node %q must depends_on at least one implement node", n.TempID)
		}
	}
	return nil
}

func isImplementRole(r model.TaskRole) bool {
	switch r {
	case model.RoleFrontend, model.RoleBackend, model.RoleFix, model.RoleOther, "":
		return true
	case model.RoleDesign, model.RoleReview, model.RoleTest:
		return false
	default:
		return true
	}
}

func nonzeroDeps(deps []string) []string {
	var out []string
	for _, d := range deps {
		if strings.TrimSpace(d) != "" {
			out = append(out, d)
		}
	}
	return out
}
