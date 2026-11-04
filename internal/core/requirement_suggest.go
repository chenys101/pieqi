package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"pieqi/internal/model"

	"github.com/google/uuid"
)

const (
	splitSuggestTitlePrefix = "[pieqi-split]"
	splitSuggestSkill       = "pieqi.split_suggest"
)

// IsSplitSuggestTask 是否为「需求自动拆分」会话（完成后解析为 SplitProposal）。
func IsSplitSuggestTask(t *model.Task) bool {
	if t == nil {
		return false
	}
	if strings.HasPrefix(t.Title, splitSuggestTitlePrefix) {
		return true
	}
	for _, s := range t.Skills {
		if s == splitSuggestSkill {
			return true
		}
	}
	return false
}

// StartSplitSuggest 创建并返回拆分建议 Task（调用方负责 Start）。
// agentName / modelName 与建任务同口径：agent 已由调用方 resolve；model 空 = agent 默认。
// 跑完后 OnTaskCompleted → 解析正文 → PutProposal，等人 confirm。
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
	// 同需求已有进行中的拆分会话则复用（避免连点）。
	for _, t := range tasksInRequirement(ts, requirementID) {
		if !IsSplitSuggestTask(t) {
			continue
		}
		if t.Status == model.TaskPending || t.Status == model.TaskRunning || t.Status == model.TaskWaitingInput {
			return t, nil
		}
	}

	prompt := buildSplitSuggestPrompt(r)
	title := splitSuggestTitlePrefix + " " + truncateRunes(r.Title, 40)
	task := &model.Task{
		ID:            uuid.New().String(),
		Source:        model.SourceHTTP,
		Status:        model.TaskPending,
		RequirementID: requirementID,
		ProjectID:     r.ProjectID,
		ProjectPath:   r.ProjectPath,
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

func buildSplitSuggestPrompt(r *model.Requirement) string {
	var b strings.Builder
	b.WriteString("你是需求拆分助手。根据下面的需求，输出**可执行的 Task List 建议**。\n\n")
	b.WriteString("## 需求\n")
	b.WriteString("标题：" + r.Title + "\n")
	if strings.TrimSpace(r.Body) != "" {
		b.WriteString("说明：\n" + r.Body + "\n")
	}
	b.WriteString("项目路径（cwd，禁止猜仓库）：" + r.ProjectPath + "\n\n")
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

1. **只输出一个 JSON 数组**，不要其它解释、不要 markdown 围栏外的闲聊。
2. 数组元素字段：
   - temp_id: 提案内唯一短 id（如 "design"、"fe-1"）
   - title: 短标题
   - prompt: 交给 coding agent 的完整任务说明（中文可）；实现票写清「仅单测自测」；test 票写清「等合入需求分支后隔离起服务做功能测」
   - role: 可选 design|frontend|backend|test|review|fix|other
   - covers_ac: 字符串数组，覆盖的 AC id（有未 deferred 的 AC 时，全部 AC 必须被至少一个节点覆盖）
   - depends_on: 本数组内其它 temp_id（可空；表达先后）
3. 拆成合理粒度：能并行的不要串成一条；明显先后的用 depends_on。
4. 不要创建"空话任务"；每条 prompt 必须可独立开跑。
5. **交付与测试纪律（必须）**：
   - 交付面是「需求分支」：实现票在 worktree 改完后合入需求分支才算交付；不要假设票 completed 即已进 main。
   - 实现类（frontend/backend/fix/…）自测 = **仅单元/组件测试**；禁止把起服务/完整链路当实现票完成条件。
   - test 票按波次拆：每个 test 必须 depends_on 本波全部相关实现票；禁止无依赖的 test 与实现并行。
   - test 票做功能测试：基于已合入的需求分支编译，**另起隔离进程**（pieqi 用独立 PIEQI_HOME + 端口，勿占现网服务）。
   - 不要给每个实现票复制一张空 test；终验可单独一张，depends_on 齐所有实现。

示例形态：
[{"temp_id":"be-1","title":"...","prompt":"...","role":"backend","covers_ac":["ac1"],"depends_on":[]},{"temp_id":"test-1","title":"...","prompt":"...","role":"test","covers_ac":["ac1"],"depends_on":["be-1"]}]
`)
	return b.String()
}

var (
	reJSONFence = regexp.MustCompile("(?s)```(?:json)?\\s*(\\[.*?\\])\\s*```")
	reJSONArray = regexp.MustCompile(`(?s)\[[\s\S]*\]`)
)

// ParseSplitSuggestion 从 agent 正文提取 ProposedTaskNode 列表。
func ParseSplitSuggestion(text string) ([]model.ProposedTaskNode, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("empty suggestion text")
	}
	raw := text
	if m := reJSONFence.FindStringSubmatch(text); len(m) == 2 {
		raw = m[1]
	} else if m := reJSONArray.FindString(text); m != "" {
		raw = m
	}
	var nodes []model.ProposedTaskNode
	if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
		return nil, fmt.Errorf("parse suggestion json: %w", err)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("suggestion has no nodes")
	}
	for i := range nodes {
		nodes[i].TempID = strings.TrimSpace(nodes[i].TempID)
		nodes[i].Prompt = strings.TrimSpace(nodes[i].Prompt)
		if nodes[i].TempID == "" {
			nodes[i].TempID = fmt.Sprintf("n%d", i+1)
		}
	}
	return nodes, nil
}

// IngestSplitSuggestion 校验（必要时补 covers_ac）并 PutProposal。
func (s *RequirementStore) IngestSplitSuggestion(requirementID string, nodes []model.ProposedTaskNode) (*model.SplitProposal, error) {
	r, ok := s.Get(requirementID)
	if !ok {
		return nil, fmt.Errorf("requirement not found: %s", requirementID)
	}
	nodes = ensureProposalCoversAC(r, nodes)
	// 重新拆分按「覆盖」语义：提案自洽（ValidateSplit），确认时再废弃旧工作任务。
	// 不再走 expand（避免新提案挂旧 Task id，与覆盖确认矛盾）。
	return s.PutProposal(requirementID, nodes)
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

// ensureProposalCoversAC 把仍未覆盖的未 deferred AC 挂到最后一个节点（fail-open 补全，避免 agent 漏标导致整单作废）。
func ensureProposalCoversAC(req *model.Requirement, nodes []model.ProposedTaskNode) []model.ProposedTaskNode {
	if req == nil || len(nodes) == 0 {
		return nodes
	}
	need := map[string]struct{}{}
	for _, ac := range req.Acceptance {
		if ac.Deferred || strings.TrimSpace(ac.ID) == "" {
			continue
		}
		need[ac.ID] = struct{}{}
	}
	if len(need) == 0 {
		return nodes
	}
	covered := map[string]struct{}{}
	for _, n := range nodes {
		for _, id := range n.CoversAC {
			covered[id] = struct{}{}
		}
	}
	var missing []string
	for id := range need {
		if _, ok := covered[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nodes
	}
	last := len(nodes) - 1
	nodes[last].CoversAC = append(nodes[last].CoversAC, missing...)
	return nodes
}

// tryIngestSplitSuggestFromTask 在任务完成时尝试写入提案。
func (s *RequirementStore) tryIngestSplitSuggestFromTask(t *model.Task) {
	if s == nil || t == nil || !IsSplitSuggestTask(t) || t.RequirementID == "" {
		return
	}
	text := t.Output
	if text == "" {
		// 从事件拼最后一轮助手文本（兜底）
		var b strings.Builder
		for _, ev := range t.Events {
			if ev.Type == model.EventText && ev.Text != "" {
				b.WriteString(ev.Text)
			}
		}
		text = b.String()
	}
	nodes, err := ParseSplitSuggestion(text)
	if err != nil {
		now := time.Now()
		_, _ = s.Update(t.RequirementID, func(req *model.Requirement) bool {
			appendReqEvent(req, model.ReqEventStatus, "split suggest parse failed: "+err.Error(), truncateRunes(text, 500), now)
			return true
		})
		return
	}
	p, err := s.IngestSplitSuggestion(t.RequirementID, nodes)
	now := time.Now()
	if err != nil {
		_, _ = s.Update(t.RequirementID, func(req *model.Requirement) bool {
			appendReqEvent(req, model.ReqEventStatus, "split suggest rejected: "+err.Error(), "", now)
			return true
		})
		return
	}
	snap, _ := json.Marshal(p)
	_, _ = s.Update(t.RequirementID, func(req *model.Requirement) bool {
		appendReqEvent(req, model.ReqEventStatus, fmt.Sprintf("split suggest ready (%d nodes)", len(p.Nodes)), string(snap), now)
		return true
	})
}
