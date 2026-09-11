package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"pieqi/internal/model"
)

// BotStore 管理 IM 机器人绑定记录（复数，D1 定案）。
//
// 元数据与密钥分离：
//
//	<dir>/index.json     —— []model.Bot，只有可下发字段（无 secret）
//	<dir>/<bot_id>.json  —— 该机器人的渠道凭据（含 app_secret，0600）
//
// 凭据文件**不由本包读写** —— 交给 internal/larkreg 的 SaveConfig/LoadConfig，
// 避免 core 反向依赖渠道实现；调用方用 CredsPath(id) 取得路径。
//
// 并发安全（RWMutex），落盘走「写 .tmp 再 rename」原子替换，
// 与 auth.BindingStore / larkreg.SaveConfig 同模式。
type BotStore struct {
	mu   sync.RWMutex
	dir  string
	bots []model.Bot
}

// BotPatch 是 Update 的补丁语义：nil 字段 = 不修改，
// 以区分「显式清空」与「没传」——这是 PATCH 的正确语义。
type BotPatch struct {
	Name      *string
	SysPrompt *string
	Role      *model.BotRole
}

// NewBotStore 打开（或创建）<dir>/index.json。
// 目录不存在则创建（0700）；index 文件不存在视为「还没有机器人」（合法状态）；
// index 存在但损坏则返回 error —— 不静默丢绑定，交由运维决定恢复还是重建。
func NewBotStore(dir string) (*BotStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("bot store dir is required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("mkdir bot dir: %w", err)
	}
	s := &BotStore{dir: dir}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *BotStore) indexPath() string { return filepath.Join(s.dir, "index.json") }

func (s *BotStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 还没有机器人 —— 合法
		}
		return fmt.Errorf("read bot index: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var bots []model.Bot
	if err := json.Unmarshal(data, &bots); err != nil {
		return fmt.Errorf("parse bot index %s: %w", s.indexPath(), err)
	}
	sortBots(bots)
	s.bots = bots
	return nil
}

// sortBots 按创建时间升序（原型按创建顺序展示）；同刻按 id 稳定排序。
func sortBots(bots []model.Bot) {
	sort.SliceStable(bots, func(i, j int) bool {
		if bots[i].CreatedAt.Equal(bots[j].CreatedAt) {
			return bots[i].ID < bots[j].ID
		}
		return bots[i].CreatedAt.Before(bots[j].CreatedAt)
	})
}

// List 返回全部机器人的副本（按创建时间升序）。
func (s *BotStore) List() []model.Bot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Bot, len(s.bots))
	copy(out, s.bots)
	return out
}

// Get 按 id 取一台机器人。
func (s *BotStore) Get(id string) (model.Bot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, b := range s.bots {
		if b.ID == id {
			return b, true
		}
	}
	return model.Bot{}, false
}

// AdminBot 返回管理员机器人（role=admin）。多机器人下至多一台。
func (s *BotStore) AdminBot() (model.Bot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, b := range s.bots {
		if b.Role == model.BotRoleAdmin {
			return b, true
		}
	}
	return model.Bot{}, false
}

// AdminBotID 返回管理员机器人 id，无则空串。
// 实现 Bridge.AdminBotResolver，供 IM 命令按机器人收紧特权（Q2）。
func (s *BotStore) AdminBotID() string {
	if b, ok := s.AdminBot(); ok {
		return b.ID
	}
	return ""
}

// FindByAppID 按「渠道 + 渠道侧应用 id」找机器人。
//
// 它是**手动配置路径的幂等键**：`POST /api/larkreg/config` 既可新建、
// 也可用来改一台已有机器人的凭据（合并语义）。没有这个查找，
// 每次保存都会多出一条记录 —— 而用户以为自己只是在"改配置"。
func (s *BotStore) FindByAppID(ch model.Channel, appID string) (model.Bot, bool) {
	if appID == "" {
		return model.Bot{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, b := range s.bots {
		if b.Channel == ch && b.AppID == appID {
			return b, true
		}
	}
	return model.Bot{}, false
}

// Create 新增一台机器人并落盘。
//
// 角色规则（是规则，不是配置项）：**首个绑定的自动成为管理员**。
// 故 in.Role 为空时按「已有 admin ? member : admin」推导；
// 显式要求 admin 而已存在 admin 时报错 —— 此时至多一台的不变量会被破坏。
func (s *BotStore) Create(in model.Bot) (model.Bot, error) {
	if in.Channel == "" {
		return model.Bot{}, fmt.Errorf("channel is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	hasAdmin := false
	for _, b := range s.bots {
		if b.Role == model.BotRoleAdmin {
			hasAdmin = true
			break
		}
	}

	switch in.Role {
	case "":
		if hasAdmin {
			in.Role = model.BotRoleMember
		} else {
			in.Role = model.BotRoleAdmin
		}
	case model.BotRoleAdmin:
		if hasAdmin {
			return model.Bot{}, fmt.Errorf("admin bot already exists")
		}
	case model.BotRoleMember:
		// 显式 member，允许
	default:
		return model.Bot{}, fmt.Errorf("invalid role %q", in.Role)
	}

	if in.Name == "" {
		in.Name = defaultBotName(in.Channel, in.Role)
	}
	if in.ID == "" {
		in.ID = newBotID()
	}
	if s.hasIDUnlocked(in.ID) {
		return model.Bot{}, fmt.Errorf("bot id %q already exists", in.ID)
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now()
	}

	// 快照回滚：sortBots 会重排，不能用「截掉最后一个」回滚（截掉的会是错的元素）。
	prev := append([]model.Bot(nil), s.bots...)

	s.bots = append(s.bots, in)
	sortBots(s.bots)
	if err := s.persistUnlocked(); err != nil {
		s.bots = prev // 保持内存态与磁盘一致
		return model.Bot{}, err
	}
	return in, nil
}

// Update 按补丁修改一台机器人。nil 字段保持不变。
func (s *BotStore) Update(id string, patch BotPatch) (model.Bot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, b := range s.bots {
		if b.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return model.Bot{}, fmt.Errorf("bot %q not found", id)
	}
	prev := s.bots[idx]
	next := prev

	if patch.Name != nil {
		next.Name = *patch.Name
	}
	if patch.SysPrompt != nil {
		next.SysPrompt = *patch.SysPrompt
	}
	if patch.Role != nil {
		switch *patch.Role {
		case model.BotRoleAdmin:
			for i, b := range s.bots {
				if i != idx && b.Role == model.BotRoleAdmin {
					return model.Bot{}, fmt.Errorf("admin bot already exists")
				}
			}
			next.Role = model.BotRoleAdmin
		case model.BotRoleMember:
			next.Role = model.BotRoleMember
		default:
			return model.Bot{}, fmt.Errorf("invalid role %q", *patch.Role)
		}
	}

	s.bots[idx] = next
	if err := s.persistUnlocked(); err != nil {
		s.bots[idx] = prev // 回滚
		return model.Bot{}, err
	}
	return next, nil
}

// Delete 解绑一台机器人：从索引移除，并尽力删除其凭据文件。
// 删除管理员机器人**不会**自动提升其他机器人 —— 管理员转移未建模，
// 见此状态请显式 PATCH 另一台为 admin（见 IMPLEMENTATION-PLAN §4.2「遗留」）。
func (s *BotStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, b := range s.bots {
		if b.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("bot %q not found", id)
	}
	removed := s.bots[idx]
	s.bots = append(s.bots[:idx], s.bots[idx+1:]...)
	if err := s.persistUnlocked(); err != nil {
		// 回滚：把元素插回原位置
		s.bots = append(s.bots, model.Bot{})
		copy(s.bots[idx+1:], s.bots[idx:])
		s.bots[idx] = removed
		return err
	}
	// 凭据文件尽力删除：文件不存在不算错误。
	_ = os.Remove(s.CredsPath(id))
	return nil
}

// CredsPath 返回该机器人的渠道凭据文件路径。
// 内容由调用方经 internal/larkreg 的 SaveConfig/LoadConfig 读写。
func (s *BotStore) CredsPath(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// Dir 返回存储目录（诊断 / 迁移用）。
func (s *BotStore) Dir() string { return s.dir }

func (s *BotStore) hasIDUnlocked(id string) bool {
	for _, b := range s.bots {
		if b.ID == id {
			return true
		}
	}
	return false
}

func (s *BotStore) persistUnlocked() error {
	data, err := json.MarshalIndent(s.bots, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bot index: %w", err)
	}
	tmp := s.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write bot index tmp: %w", err)
	}
	if err := os.Rename(tmp, s.indexPath()); err != nil {
		return fmt.Errorf("rename bot index: %w", err)
	}
	return nil
}

// defaultBotName 生成显示名（渠道是属性，写在名字前半截）。
func defaultBotName(ch model.Channel, role model.BotRole) string {
	label := channelLabel(ch)
	if role == model.BotRoleAdmin {
		return label + " · 管理员"
	}
	return label + " · 机器人"
}

func channelLabel(ch model.Channel) string {
	switch ch {
	case model.ChannelLark:
		return "飞书"
	case model.ChannelWeCom:
		return "企业微信"
	case model.ChannelWeChat:
		return "微信"
	default:
		return string(ch)
	}
}

// newBotID 生成 bot_ 前缀的随机 id。crypto/rand 失败时退回时间戳（不致命）。
func newBotID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "bot_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "bot_" + hex.EncodeToString(b[:])
}
