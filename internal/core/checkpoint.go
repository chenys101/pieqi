// checkpoint.go FeedbackStore：Checkpoint 的物理捕获、before 状态组装与 Rewind 恢复
// （p0-design.md §3.4，ADR-0002：Checkpoint/Rewind 用文件快照，绝不写用户分支）。
//
// 磁盘布局：
//
//	<root>/<taskID>/pre/              ← Task 起始 dirty 文件全量字节快照
//	<root>/<taskID>/turn3/            ← Turn 3 结束态快照：只含 Turn 3 实际碰过的文件
//	<root>/<taskID>/turn3/.pieqi-deleted.json ← Turn 3 中被删除的文件清单（防止 Rewind 误复活）
//
// 物理捕获只发生在 Agent 静止的边界（Task 创建 / 下一 EventUser 到达 / 终态），
// 避免 Turn 中途读盘与 Agent 写入竞态。
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"pieqi/internal/model"

	"go.uber.org/zap"
)

// errInvalidTask Rewind 目标任务不合法（不存在 / 无 worktree）。
var errInvalidTask = errors.New("invalid task for rewind")

// deletedManifestName Turn 快照中的删除清单文件名（Agent 在该 Turn 删除的文件路径列表）。
const deletedManifestName = ".pieqi-deleted.json"

// FeedbackStore 管理 Task 的 baseline 与 checkpoint 快照。
type FeedbackStore struct {
	logger *zap.Logger
	root   string // <dataRoot>/checkpoints
}

// NewFeedbackStore 创建 checkpoint 存储。root 为快照根目录（空则 ~/.pieqi/checkpoints）。
func NewFeedbackStore(logger *zap.Logger, root string) *FeedbackStore {
	if root == "" {
		root = filepath.Join(defaultFeedbackDataRoot(), "checkpoints")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &FeedbackStore{logger: logger, root: root}
}

// defaultFeedbackDataRoot 数据根目录（$PIEQI_HOME 优先，否则 ~/.pieqi）。
func defaultFeedbackDataRoot() string {
	if h := os.Getenv("PIEQI_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".pieqi")
}

// --- Baseline 捕获 ---

// CaptureBaseline 在 Task 开始（Agent 尚未动工）时捕获 baseline：
// 记录 git HEAD + dirty 文件全量快照到 pre/。幂等（已有 pre/ 或 task.Baseline 时跳过）。
// 返回应写入 task.Baseline 的记录（已捕获时返回 nil 供调用方判断）。
func (fs *FeedbackStore) CaptureBaseline(task *model.Task) *model.TaskBaseline {
	if task == nil || task.WorktreePath == "" {
		return nil
	}
	preDir := fs.taskDir(task.ID, "pre")
	if _, err := os.Stat(preDir); err == nil {
		return nil // 已捕获
	}

	head := GitHeadSHA(task.WorktreePath)
	dirty := GitStatusPorcelain(task.WorktreePath)
	// 非 git 项目（git status 失败 → nil）：回退为全量快照文件树。
	// 否则 pre/ 空、HEAD 空 → before 状态不可知 → diff 退化成「整文件当新增」（数据失真）。
	if dirty == nil {
		dirty = walkWorktreeFiles(task.WorktreePath)
	}

	if err := os.MkdirAll(preDir, 0755); err != nil {
		fs.logger.Warn("feedback: mkdir pre", zap.String("task", task.ID), zap.Error(err))
		return nil
	}
	for _, p := range dirty {
		if err := copyFileInto(filepath.Join(task.WorktreePath, filepath.FromSlash(p)), preDir, p); err != nil {
			fs.logger.Warn("feedback: snapshot dirty file",
				zap.String("task", task.ID), zap.String("path", p), zap.Error(err))
		}
	}
	return &model.TaskBaseline{HeadSHA: head, CapturedAt: time.Now(), DirtyPaths: dirty}
}

// baselineNoiseDirs 全量基线快照时跳过的目录（非 git 项目兜底：不把依赖/产物拷进 pre/）。
var baselineNoiseDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "build": true,
	"coverage": true, ".next": true, ".nuxt": true, ".cache": true,
	"__pycache__": true, ".venv": true, "venv": true, "target": true,
}

// walkWorktreeFiles 递归列出 worktree 下全部文件（repo 相对路径、/ 分隔、字母序），
// 跳过噪音目录。非 git 项目的 pre/ 基线快照用它枚举「Task 起始全量文件」。
func walkWorktreeFiles(root string) []string {
	var files []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() != "." && baselineNoiseDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	return files
}

// --- Turn 快照捕获 ---

// CaptureTurnEnd 捕获 Turn N 的结束态：把该 Turn 实际碰过且仍存在的文件拷到 turnN/，
// 被删除的文件写入 deleted manifest。幂等（turnN/ 已存在时跳过）。
func (fs *FeedbackStore) CaptureTurnEnd(task *model.Task, turn int) {
	if task == nil || task.WorktreePath == "" || turn <= 0 {
		return
	}
	turnDir := fs.taskDir(task.ID, "turn"+strconv.Itoa(turn))
	if _, err := os.Stat(turnDir); err == nil {
		return // 已捕获
	}

	changes := DeriveFileChanges(task.Events, task.WorktreePath, nil)
	var touched, deleted []string
	for _, fc := range changes {
		if fc.Turn != turn {
			continue
		}
		if _, exists := ReadWorktreeFile(task.WorktreePath, fc.Path); exists {
			touched = append(touched, fc.Path)
		} else {
			deleted = append(deleted, fc.Path)
		}
	}
	if len(touched) == 0 && len(deleted) == 0 {
		return // 该 Turn 没碰文件（纯对话），不留空目录
	}

	if err := os.MkdirAll(turnDir, 0755); err != nil {
		fs.logger.Warn("feedback: mkdir turn snapshot", zap.String("task", task.ID), zap.Error(err))
		return
	}
	for _, p := range touched {
		if err := copyFileInto(filepath.Join(task.WorktreePath, filepath.FromSlash(p)), turnDir, p); err != nil {
			fs.logger.Warn("feedback: snapshot turn file",
				zap.String("task", task.ID), zap.String("path", p), zap.Error(err))
		}
	}
	if len(deleted) > 0 {
		sort.Strings(deleted)
		data, _ := json.Marshal(deleted)
		if err := os.WriteFile(filepath.Join(turnDir, deletedManifestName), data, 0644); err != nil {
			fs.logger.Warn("feedback: write deleted manifest", zap.String("task", task.ID), zap.Error(err))
		}
	}
}

// --- before 状态组装（单 Turn Diff 的 before 侧 / Rewind 的恢复目标） ---

// assembledBefore 组装「Turn N 开始之前」文件 X 的状态（p0-design.md §3.4）：
//  1. 若 X 在 Turn < N 中被碰过 → 取最近一次 turnK/X（含 deleted manifest 判定）
//  2. 否则若 X 在 pre/ → 取 pre/X（用户 dirty/untracked 原样）
//  3. 否则 → git HEAD 内容；不存在则视为「Task 起始不存在」
func (fs *FeedbackStore) assembledBefore(task *model.Task, turn int, path string) ([]byte, bool) {
	// 快照目录序号倒序扫描，取最近一次碰过该文件的状态
	for k := turn - 1; k >= 1; k-- {
		dir := fs.taskDir(task.ID, "turn"+strconv.Itoa(k))
		if content, ok := readSnapshotFile(dir, path); ok {
			return content, true
		}
		if manifestContains(dir, path) {
			return nil, false // 该 Turn 已删除
		}
	}
	// pre/（用户 Task 起始的 dirty/untracked 原样）
	if content, ok := readSnapshotFile(fs.taskDir(task.ID, "pre"), path); ok {
		return content, true
	}
	// git HEAD（只读参照）；旧任务无 Baseline 时兜底用当前 HEAD（尽力而为）
	head := "HEAD"
	if task.Baseline != nil && task.Baseline.HeadSHA != "" {
		head = task.Baseline.HeadSHA
	}
	return GitCatFileAtHead(task.WorktreePath, head, path)
}

// readSnapshotFile 从快照目录读文件（路径安全拼接，防 ../ 逃逸）。
func readSnapshotFile(dir, path string) ([]byte, bool) {
	if path == "" {
		return nil, false
	}
	data, err := os.ReadFile(safeJoin(dir, path))
	if err != nil {
		return nil, false
	}
	return data, true
}

// manifestContains 判断 turn 快照的 deleted manifest 是否包含 path。
func manifestContains(dir, path string) bool {
	data, err := os.ReadFile(filepath.Join(dir, deletedManifestName))
	if err != nil {
		return false
	}
	var deleted []string
	if json.Unmarshal(data, &deleted) != nil {
		return false
	}
	for _, p := range deleted {
		if p == path {
			return true
		}
	}
	return false
}

// --- Rewind ---

// RewindResult Rewind 的执行结果。
type RewindResult struct {
	ToTurn   int      `json:"to_turn"`
	Restored []string `json:"restored"` // 实际发生写入/删除的路径
}

// RewindToTurn 把 Agent 触碰过的文件恢复到「Turn N 开始之前」的状态。
// 恢复集合 = Turn >= N 的全部 FileChange 路径；用户未被 Agent 碰过的 dirty 文件永不受影响。
// 返回实际恢复的路径列表。
func (fs *FeedbackStore) RewindToTurn(task *model.Task, toTurn int) (*RewindResult, error) {
	if task == nil || task.WorktreePath == "" {
		return nil, errInvalidTask
	}
	changes := DeriveFileChanges(task.Events, task.WorktreePath, nil)

	// 收集 Turn >= toTurn 的触碰路径（有序，保证结果稳定）
	pathSet := map[string]bool{}
	for _, fc := range changes {
		if fc.Turn >= toTurn {
			pathSet[fc.Path] = true
		}
	}
	paths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	restored := make([]string, 0, len(paths))
	for _, p := range paths {
		content, exists := fs.assembledBefore(task, toTurn, p)
		target := safeJoin(task.WorktreePath, p)
		switch {
		case exists:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(target, content, 0644); err != nil {
				return nil, err
			}
			restored = append(restored, p)
		default:
			// Turn 开始前不存在 → 删除当前文件（若在）
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			restored = append(restored, p)
		}
	}
	return &RewindResult{ToTurn: toTurn, Restored: restored}, nil
}

// AssembleBefore 组装「Turn N 开始之前」文件 X 的状态（公开入口，供 Diff/Rewind 复用）。
// 返回 (内容, 是否存在)。
func (fs *FeedbackStore) AssembleBefore(task *model.Task, turn int, path string) ([]byte, bool) {
	return fs.assembledBefore(task, turn, path)
}

// RewindFileToTurn 文件级回退（p2-design.md §7）：只把单个文件恢复到「Turn N 开始之前」。
// 不影响其他文件；返回实际恢复的路径列表（固定 0 或 1 个元素）。
// 路径合法性：必须是 Agent 在 Turn >= N 实际触碰过的文件（防止任意路径恢复）。
func (fs *FeedbackStore) RewindFileToTurn(task *model.Task, toTurn int, path string) (*RewindResult, error) {
	if task == nil || task.WorktreePath == "" {
		return nil, errInvalidTask
	}
	if toTurn < 1 {
		return nil, fmt.Errorf("invalid to_turn: %d", toTurn)
	}
	path = strings.TrimPrefix(path, "./")
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	// 合法性：该文件必须在目标 Turn 及之后被 Agent 触碰过（否则无「Agent 改动」可回退）
	changes := DeriveFileChanges(task.Events, task.WorktreePath, nil)
	touched := false
	for _, fc := range changes {
		if fc.Path == path && fc.Turn >= toTurn {
			touched = true
			break
		}
	}
	if !touched {
		return nil, fmt.Errorf("file not touched at or after turn %d: %s", toTurn, path)
	}

	content, exists := fs.assembledBefore(task, toTurn, path)
	target := safeJoin(task.WorktreePath, path)
	switch {
	case exists:
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(target, content, 0644); err != nil {
			return nil, err
		}
	default:
		// Turn 开始前不存在 → 删除当前文件（若在）
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return &RewindResult{ToTurn: toTurn, Restored: []string{path}}, nil
}

// TurnContent Turn N 结束态内容：turn 快照优先；快照缺失（进行中/捕获失败）回退当前工作文件。
func (fs *FeedbackStore) TurnContent(task *model.Task, turn int, path string) ([]byte, bool) {
	if c, ok := readSnapshotFile(fs.taskDir(task.ID, "turn"+strconv.Itoa(turn)), path); ok {
		return c, true
	}
	return ReadWorktreeFile(task.WorktreePath, path)
}

// SeenBeforeTask 判定路径在 Task 起始之前是否已知存在（Write 的 create/modify 判定用）：
// 在 pre/（用户 dirty/untracked）或 git HEAD 中均可。
func (fs *FeedbackStore) SeenBeforeTask(task *model.Task, path string) bool {
	if _, ok := readSnapshotFile(fs.taskDir(task.ID, "pre"), path); ok {
		return true
	}
	head := "HEAD"
	if task.Baseline != nil && task.Baseline.HeadSHA != "" {
		head = task.Baseline.HeadSHA
	}
	_, ok := GitCatFileAtHead(task.WorktreePath, head, path)
	return ok
}

// Checkpoints 列出该 Task 已捕获的 Turn 号（升序）。
func (fs *FeedbackStore) Checkpoints(taskID string) []int {
	entries, err := os.ReadDir(fs.taskDir(taskID, ""))
	if err != nil {
		return nil
	}
	var turns []int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "turn")); err == nil && n > 0 {
			turns = append(turns, n)
		}
	}
	sort.Ints(turns)
	return turns
}

// Cleanup 删除该 Task 的全部 checkpoint（Task 删除时调用）。幂等。
func (fs *FeedbackStore) Cleanup(taskID string) {
	if err := os.RemoveAll(fs.taskDir(taskID, "")); err != nil {
		fs.logger.Warn("feedback: cleanup checkpoints", zap.String("task", taskID), zap.Error(err))
	}
}

// taskDir 拼快照目录：<root>/<taskID>/<sub>。
func (fs *FeedbackStore) taskDir(taskID, sub string) string {
	return filepath.Join(fs.root, taskID, sub)
}

// copyFileInto 把 src 文件按 repo 相对路径 p 拷进 dstRoot（保留目录结构）。
func copyFileInto(src, dstRoot, p string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	dst := safeJoin(dstRoot, p)
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// safeJoin 安全拼接快照路径：拒绝绝对路径与 .. 逃逸（FileChange 路径来自事件流，不可信输入）。
func safeJoin(root, rel string) string {
	rel = filepath.ToSlash(rel)
	if rel == "" || strings.HasPrefix(rel, "/") || rel == "." {
		return filepath.Join(root, "invalid")
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return filepath.Join(root, "invalid")
		}
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}
