package core

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"pieqi/internal/model"

	"github.com/google/uuid"
)

const (
	splitSuggestTitlePrefix       = "[split]"
	splitSuggestTitlePrefixLegacy = "[pieqi-split]" // 旧票兼容
	splitSuggestSkill             = "split_suggest"
	splitSuggestSkillLegacy       = "pieqi.split_suggest" // 旧票兼容
)

// ErrIntegrationBranchRequired StartSplitSuggest 入口闸：未登记合入分支。
type ErrIntegrationBranchRequired struct {
	CandidateBranch string
}

func (e *ErrIntegrationBranchRequired) Error() string {
	if e == nil {
		return "integration_branch required"
	}
	if e.CandidateBranch != "" {
		return fmt.Sprintf("integration_branch required (candidate %s)", e.CandidateBranch)
	}
	return "integration_branch required"
}

// IsSplitSuggestTask 是否为「需求自动拆分」会话（完成后解析为 SplitProposal / SplitBlock）。
func IsSplitSuggestTask(t *model.Task) bool {
	if t == nil {
		return false
	}
	if strings.HasPrefix(t.Title, splitSuggestTitlePrefix) ||
		strings.HasPrefix(t.Title, splitSuggestTitlePrefixLegacy) {
		return true
	}
	for _, s := range t.Skills {
		if s == splitSuggestSkill || s == splitSuggestSkillLegacy {
			return true
		}
	}
	return false
}

// CandidateIntegrationBranch 主仓当前 HEAD 短名（供 409 / 打磨区预填）；失败返回空。
func CandidateIntegrationBranch(repoPath string) string {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return ""
	}
	out, err := runGit(context.Background(), repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	ref := strings.TrimSpace(out)
	if ref == "" || ref == "HEAD" {
		return ""
	}
	return ref
}

// StartSplitSuggest 创建并返回拆分建议 Task（调用方负责 Start）。
// agentName / modelName 与建任务同口径：agent 已由调用方 resolve；model 空 = agent 默认。
// 无 integration_branch → ErrIntegrationBranchRequired（不开 Agent）。
// 拆分会话 WorktreePath=ProjectPath（不建 task/ worktree）。
// 跑完后 OnTaskCompleted → 解析正文 → PutProposal 或 PutBlock。
func (s *RequirementStore) StartSplitSuggest(requirementID, agentName, modelName string) (*model.Task, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("requirement store not wired")
	}
	s.mu.Lock()
	ts := s.tasks
	s.mu.Unlock()
	if ts == nil {
		return nil, fmt.Errorf("task store not wired")
	}
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	if r.Status == model.RequirementCancelled || r.Status == model.RequirementDone {
		return nil, fmt.Errorf("requirement is %s", r.Status)
	}
	if strings.TrimSpace(r.IntegrationBranch) == "" {
		return nil, &ErrIntegrationBranchRequired{CandidateBranch: CandidateIntegrationBranch(r.ProjectPath)}
	}
	// 同需求已有进行中的拆分会话则复用（避免连点）。
	for _, t := range tasksInRequirement(ts, requirementID) {
		if !IsSplitSuggestTask(t) {
			continue
		}
		if t.Status == model.TaskPending || t.Status == model.TaskRunning || t.Status == model.TaskWaitingInput {
			return t, nil
		}
	}

	prompt := buildSplitSuggestPrompt(r, s)
	title := splitSuggestTitlePrefix + " " + truncateRunes(r.Title, 40)
	task := &model.Task{
		ID:            uuid.New().String(),
		Source:        model.SourceHTTP,
		Status:        model.TaskPending,
		RequirementID: requirementID,
		ProjectID:     r.ProjectID,
		ProjectPath:   r.ProjectPath,
		WorktreePath:  r.ProjectPath, // 与「当前分支」语义一致；不建 task/ worktree
		Prompt:        prompt,
		Title:         title,
		Role:          model.RoleDesign,
		Skills:        []string{splitSuggestSkill},
		Agent:         strings.TrimSpace(agentName),
		Model:         strings.TrimSpace(modelName),
	}
	got, err := ts.Create(task)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	_, _ = s.Update(requirementID, func(req *model.Requirement) bool {
		appendReqEvent(req, model.ReqEventStatus, "split suggest started "+got.ID, "", now)
		if req.Status == model.RequirementDraft {
			req.Status = model.RequirementActive
		}
		return true
	})
	return got, nil
}

func buildSplitSuggestPrompt(r *model.Requirement, s *RequirementStore) string {
	var b strings.Builder
	b.WriteString("你是需求拆分助手。根据下面的需求，输出**可执行的 Task List 建议**，或在信息不足时输出 blocked。\n\n")
	b.WriteString("## 需求\n")
	b.WriteString("标题：" + r.Title + "\n")
	if strings.TrimSpace(r.Body) != "" {
		b.WriteString("说明：\n" + r.Body + "\n")
	}
	b.WriteString("项目路径（cwd，禁止猜仓库）：" + r.ProjectPath + "\n")
	if br := strings.TrimSpace(r.IntegrationBranch); br != "" {
		b.WriteString("**合入目标（已登记，禁止改猜）**：`" + br + "`\n")
		b.WriteString("禁止把 `task/` 任务 worktree 分支当作合入目标。\n")
	}
	b.WriteString("\n")
	if len(r.Acceptance) > 0 {
		b.WriteString("## 验收标准（AC）\n")
		for _, ac := range r.Acceptance {
			if ac.Deferred {
				b.WriteString(fmt.Sprintf("- %s (deferred): %s\n", ac.ID, ac.Text))
			} else {
				b.WriteString(fmt.Sprintf("- %s: %s\n", ac.ID, ac.Text))
			}
		}
		b.WriteString("\n")
	}
	b.WriteString(`## 输出要求（严格）

1. **只输出一个 JSON 对象**（不要其它解释；可用 markdown 围栏包裹该对象）。合法二选一：
   - proposal：{"kind":"proposal","nodes":[ /* ProposedTaskNode */ ]}
   - blocked：{"kind":"blocked","summary":"一句话为何不能拆","reasons":["…"],"questions":["需要人回答的问题"],"missing":["integration_branch","acceptance","scope"]}
   兼容：若输出**纯 JSON 数组**，视为 kind=proposal 的 nodes。
2. proposal 的 nodes 元素字段：
   - temp_id: 提案内唯一短 id（如 "design"、"fe-1"）
   - title: 短标题
   - prompt: 交给 coding agent 的完整任务说明（中文可）；实现票写清「仅单测自测」；test 票写清「等合入需求分支后隔离起服务做功能测」；每条须含 Done=（可执行检查）
   - role: 可选 design|frontend|backend|test|review|fix|other
   - covers_ac: 字符串数组，覆盖的 AC id（有未 deferred 的 AC 时，全部 AC 必须被至少一个节点覆盖）
   - depends_on: 本数组内其它 temp_id（可空；表达先后）
3. **何时必须 blocked（禁止猜）**：范围 / 合入面 / AC 不清时必须 blocked；禁止猜分支；禁止用 task/ 当合入目标；禁止硬给可落地拆分。
`)
	// Pack 策略段（少拆 / 并行哲学）；缺 Registry 则用内置默认少拆文案。
	instr := defaultSplitInstructions
	if s != nil {
		if p, err := s.PackFor(r); err == nil && p != nil && strings.TrimSpace(p.SplitInstructions) != "" {
			instr = p.SplitInstructions
		}
	}
	b.WriteString(instr)
	b.WriteString(`
示例 proposal：
{"kind":"proposal","nodes":[{"temp_id":"be-1","title":"...","prompt":"... Done= go test ./...","role":"other","covers_ac":["ac1"],"depends_on":[]},{"temp_id":"test-1","title":"...","prompt":"...","role":"test","covers_ac":["ac1"],"depends_on":["be-1"]}]}

示例 blocked：
{"kind":"blocked","summary":"合入面与验收未钉死","reasons":["未写清合入目标"],"questions":["合入哪条分支？"],"missing":["acceptance"]}
`)
	return b.String()
}

// 无 Pack 时的少拆默认（替代旧「能并行的不要串成一条」）。
const defaultSplitInstructions = `
4. **拆分哲学（默认）**：少票。推荐 0～1 design + 1～2 实现（串行 depends_on）+ 0～1 test。
   禁止按 frontend/backend 工种硬拆，除非文件集无交集且各有独立验收。
   同需求实现票默认串行合入；不要为并行而并行。
5. 不要创建空话任务；每条 prompt 必须可独立开跑。
6. **交付与测试纪律（必须）**：
   - 交付面是「需求分支」：实现票合入需求分支才算交付。
   - 实现类自测 = 仅单元/组件测试；禁止起服务当完成条件。
   - test 必须 depends_on 实现票；终验可单独一张。
`

// SuggestParseKind 解析结果类别。
type SuggestParseKind string

const (
	SuggestParseProposal SuggestParseKind = "proposal"
	SuggestParseBlocked  SuggestParseKind = "blocked"
)

// SuggestParseResult ParseSuggestOutcome 产物。
type SuggestParseResult struct {
	Kind  SuggestParseKind
	Nodes []model.ProposedTaskNode
	Block *model.SplitBlock
}

var (
	reJSONFence      = regexp.MustCompile("(?s)```(?:json)?\\s*([\\[{].*?[\\]}])\\s*```")
	reJSONArray      = regexp.MustCompile(`(?s)\[[\s\S]*\]`)
	reJSONObject     = regexp.MustCompile(`(?s)\{[\s\S]*\}`)
)

// ParseSplitSuggestion 兼容旧名：仅接受 proposal（纯数组或 kind=proposal）；blocked 报错。
func ParseSplitSuggestion(text string) ([]model.ProposedTaskNode, error) {
	out, err := ParseSuggestOutcome(text)
	if err != nil {
		return nil, err
	}
	if out.Kind != SuggestParseProposal {
		return nil, fmt.Errorf("suggestion is blocked, not a proposal")
	}
	return out.Nodes, nil
}

// ParseSuggestOutcome 从 agent 正文提取 proposal 或 blocked。
// 旧纯数组 → proposal；非法/空 → error。
func ParseSuggestOutcome(text string) (*SuggestParseResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty suggestion text")
	}
	raw := text
	if m := reJSONFence.FindStringSubmatch(text); len(m) == 2 {
		raw = m[1]
	} else if strings.HasPrefix(strings.TrimSpace(text), "[") {
		if m := reJSONArray.FindString(text); m != "" {
			raw = m
		}
	} else if m := reJSONObject.FindString(text); m != "" {
		raw = m
	} else if m := reJSONArray.FindString(text); m != "" {
		raw = m
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("no json in suggestion text")
	}

	// 纯数组 = 旧 agent 输出
	if strings.HasPrefix(raw, "[") {
		nodes, err := unmarshalProposalNodes(raw)
		if err != nil {
			return nil, err
		}
		return &SuggestParseResult{Kind: SuggestParseProposal, Nodes: nodes}, nil
	}

	var envelope struct {
		Kind      string                   `json:"kind"`
		Nodes     []model.ProposedTaskNode `json:"nodes"`
		Summary   string                   `json:"summary"`
		Reasons   []string                 `json:"reasons"`
		Questions []string                 `json:"questions"`
		Missing   []string                 `json:"missing"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil, fmt.Errorf("parse suggestion json: %w", err)
	}
	kind := strings.ToLower(strings.TrimSpace(envelope.Kind))
	switch kind {
	case "", "proposal":
		if len(envelope.Nodes) == 0 {
			// 无 kind 且无 nodes：可能误把数组包进对象失败；再试整段当数组
			return nil, fmt.Errorf("suggestion has no nodes")
		}
		nodes := normalizeProposalNodes(envelope.Nodes)
		return &SuggestParseResult{Kind: SuggestParseProposal, Nodes: nodes}, nil
	case "blocked":
		return &SuggestParseResult{
			Kind: SuggestParseBlocked,
			Block: &model.SplitBlock{
				Summary:   strings.TrimSpace(envelope.Summary),
				Reasons:   trimStringList(envelope.Reasons),
				Questions: trimStringList(envelope.Questions),
				Missing:   trimStringList(envelope.Missing),
			},
		}, nil
	default:
		return nil, fmt.Errorf("unknown suggestion kind %q", envelope.Kind)
	}
}

func unmarshalProposalNodes(raw string) ([]model.ProposedTaskNode, error) {
	var nodes []model.ProposedTaskNode
	if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
		return nil, fmt.Errorf("parse suggestion json: %w", err)
	}
	nodes = normalizeProposalNodes(nodes)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("suggestion has no nodes")
	}
	return nodes, nil
}

func normalizeProposalNodes(nodes []model.ProposedTaskNode) []model.ProposedTaskNode {
	for i := range nodes {
		nodes[i].TempID = strings.TrimSpace(nodes[i].TempID)
		nodes[i].Prompt = strings.TrimSpace(nodes[i].Prompt)
		if nodes[i].TempID == "" {
			nodes[i].TempID = fmt.Sprintf("n%d", i+1)
		}
	}
	return nodes
}

func trimStringList(in []string) []string {
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// IngestSplitSuggestion 校验（必要时补 covers_ac）并 PutProposal。
func (s *RequirementStore) IngestSplitSuggestion(requirementID string, nodes []model.ProposedTaskNode) (*model.SplitProposal, error) {
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	nodes = ensureProposalCoversAC(r, nodes)
	return s.PutProposal(requirementID, nodes)
}

// IngestSuggestOutcome 按解析结果写入 proposal 或 block。
func (s *RequirementStore) IngestSuggestOutcome(requirementID string, parsed *SuggestParseResult) error {
	if parsed == nil {
		return fmt.Errorf("nil parse result")
	}
	switch parsed.Kind {
	case SuggestParseProposal:
		_, err := s.IngestSplitSuggestion(requirementID, parsed.Nodes)
		return err
	case SuggestParseBlocked:
		_, err := s.PutBlock(requirementID, parsed.Block)
		return err
	default:
		return fmt.Errorf("unknown parse kind %q", parsed.Kind)
	}
}

func filterOutSuggest(tasks []*model.Task) []*model.Task {
	var out []*model.Task
	for _, t := range tasks {
		if t != nil && !IsSplitSuggestTask(t) {
			out = append(out, t)
		}
	}
	return out
}

// tryIngestSplitSuggestFromTask 在任务完成时尝试写入提案或 blocked。
func (s *RequirementStore) tryIngestSplitSuggestFromTask(t *model.Task) {
	if s == nil || t == nil || !IsSplitSuggestTask(t) || t.RequirementID == "" {
		return
	}
	text := t.Output
	if text == "" {
		var b strings.Builder
		for _, ev := range t.Events {
			if ev.Type == model.EventText && ev.Text != "" {
				b.WriteString(ev.Text)
			}
		}
		text = b.String()
	}
	parsed, err := ParseSuggestOutcome(text)
	if err != nil {
		now := time.Now()
		_, _ = s.Update(t.RequirementID, func(req *model.Requirement) bool {
			appendReqEvent(req, model.ReqEventStatus, "split suggest parse failed: "+err.Error(), truncateRunes(text, 500), now)
			return true
		})
		return
	}
	err = s.IngestSuggestOutcome(t.RequirementID, parsed)
	now := time.Now()
	if err != nil {
		_, _ = s.Update(t.RequirementID, func(req *model.Requirement) bool {
			appendReqEvent(req, model.ReqEventStatus, "split suggest rejected: "+err.Error(), "", now)
			return true
		})
		return
	}
	if parsed.Kind == SuggestParseBlocked {
		snap, _ := json.Marshal(parsed.Block)
		_, _ = s.Update(t.RequirementID, func(req *model.Requirement) bool {
			appendReqEvent(req, model.ReqEventStatus, "split suggest blocked: "+parsed.Block.Summary, string(snap), now)
			return true
		})
		return
	}
	p, _ := s.GetProposal(t.RequirementID)
	n := 0
	if p != nil {
		n = len(p.Nodes)
	}
	snap, _ := json.Marshal(p)
	_, _ = s.Update(t.RequirementID, func(req *model.Requirement) bool {
		appendReqEvent(req, model.ReqEventStatus, fmt.Sprintf("split suggest ready (%d nodes)", n), string(snap), now)
		return true
	})
}

// ensureProposalCoversAC 把仍未覆盖的未 deferred AC 挂到最后一个节点（fail-open 补全，避免 agent 漏标导致整单作废）。
func ensureProposalCoversAC(req *model.Requirement, nodes []model.ProposedTaskNode) []model.ProposedTaskNode {
	if req == nil || len(nodes) == 0 {
		return nodes
	}
	covered := map[string]struct{}{}
	for _, n := range nodes {
		for _, ac := range n.CoversAC {
			ac = strings.TrimSpace(ac)
			if ac != "" {
				covered[ac] = struct{}{}
			}
		}
	}
	var missing []string
	for _, ac := range req.Acceptance {
		if ac.Deferred || strings.TrimSpace(ac.ID) == "" {
			continue
		}
		if _, ok := covered[ac.ID]; !ok {
			missing = append(missing, ac.ID)
		}
	}
	if len(missing) == 0 {
		return nodes
	}
	last := &nodes[len(nodes)-1]
	last.CoversAC = append(last.CoversAC, missing...)
	return nodes
}
