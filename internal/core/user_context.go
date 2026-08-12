package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"claude-bridge/internal/model"

	"github.com/google/uuid"
)

type UserContext struct {
	mu          sync.Mutex
	usersPath   string
	sessionsDir string
	mappingsDir string
	ttl         time.Duration
	users       model.UserBindings
}

type sessionFileData struct {
	UUID    string `json:"uuid"`
	Preview string `json:"preview"`
}

func NewUserContext(usersPath, sessionsDir, mappingsDir string, ttl time.Duration) (*UserContext, error) {
	uc := &UserContext{
		usersPath:   usersPath,
		sessionsDir: sessionsDir,
		mappingsDir: mappingsDir,
		ttl:         ttl,
	}
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(mappingsDir, 0755); err != nil {
		return nil, err
	}
	uc.loadUsers()
	return uc, nil
}

func (uc *UserContext) Resolve(msg model.Message, sessionIndex string) (identity, sessionUUID string, err error) {
	uc.mu.Lock()
	defer uc.mu.Unlock()

	uc.loadUsers()
	identity = uc.findIdentity(msg.Channel, msg.UserID)
	if identity == "" {
		return "", "", fmt.Errorf("unknown user: %s %s", msg.Channel, msg.UserID)
	}

	if sessionIndex != "" {
		return identity, uc.ensureSession(identity, sessionIndex), nil
	}
	return identity, uc.autoBind(identity, msg.Content), nil
}

func (uc *UserContext) autoBind(identity, preview string) string {
	sm := uc.loadMap(identity)
	_, mtime, ok := sm.latest(uc.sessionsDir)
	if ok && time.Since(mtime) <= uc.ttl {
		uc.touchFile(sm)
		return uc.readUUID(sm)
	}
	return uc.createSession(identity, preview)
}

func (uc *UserContext) touchFile(sm *sessionMap) {
	_, mtime, ok := sm.latest(uc.sessionsDir)
	if !ok {
		return
	}
	now := time.Now()
	fp := filepath.Join(uc.sessionsDir, uc.smLatestFilename(sm))
	os.Chtimes(fp, now, now)
	_ = mtime
}

func (uc *UserContext) smLatestFilename(sm *sessionMap) string {
	e, _, ok := sm.latest(uc.sessionsDir)
	if !ok {
		return ""
	}
	return e.Filename + ".json"
}

func (uc *UserContext) readUUID(sm *sessionMap) string {
	e, _, ok := sm.latest(uc.sessionsDir)
	if !ok {
		return ""
	}
	data, _ := os.ReadFile(filepath.Join(uc.sessionsDir, e.Filename+".json"))
	var sf sessionFileData
	json.Unmarshal(data, &sf)
	return sf.UUID
}

func (uc *UserContext) ensureSession(identity, indexStr string) string {
	idx, err := strconv.Atoi(indexStr)
	if err != nil {
		return uc.createSession(identity, "")
	}

	sm := uc.loadMap(identity)
	fn, ok := sm.resolve(idx)
	if !ok {
		return uc.createSession(identity, "")
	}

	data, err := os.ReadFile(filepath.Join(uc.sessionsDir, fn+".json"))
	if err != nil {
		return uc.createSession(identity, "")
	}
	var sf sessionFileData
	if json.Unmarshal(data, &sf) != nil || sf.UUID == "" {
		return uc.createSession(identity, "")
	}

	// touch file mtime
	now := time.Now()
	os.Chtimes(filepath.Join(uc.sessionsDir, fn+".json"), now, now)
	return sf.UUID
}

func (uc *UserContext) createSession(identity, preview string) string {
	fn := uc.newFilename(identity)
	id := uuid.New().String()

	cleaned := cleanPreview(preview)
	data, _ := json.Marshal(sessionFileData{UUID: id, Preview: cleaned})
	os.WriteFile(filepath.Join(uc.sessionsDir, fn+".json"), data, 0644)

	sm := uc.loadMap(identity)
	_, _ = sm.assign(fn, uc.sessionsDir)
	return id
}

func (uc *UserContext) NewSession(identity string) string {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	return uc.createSession(identity, "")
}

// newFilename 生成日期前缀的文件名: user-{identity}-{yyMMdd}-{seq}
func (uc *UserContext) newFilename(identity string) string {
	today := time.Now().Format("060102")
	prefix := fmt.Sprintf("user-%s-%s-", identity, today)
	max := 0
	entries, _ := os.ReadDir(uc.sessionsDir)
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, prefix) {
			continue
		}
		n = strings.TrimSuffix(strings.TrimPrefix(n, prefix), ".json")
		if v, err := strconv.Atoi(n); err == nil && v > max {
			max = v
		}
	}
	return fmt.Sprintf("user-%s-%s-%d", identity, today, max+1)
}

func (uc *UserContext) ListSessions(identity string) []model.SessionInfo {
	uc.mu.Lock()
	defer uc.mu.Unlock()

	sm := uc.loadMap(identity)
	entries := sm.list(uc.sessionsDir)

	var out []model.SessionInfo
	for _, e := range entries {
		fp := filepath.Join(uc.sessionsDir, e.Filename+".json")
		info, err := os.Stat(fp)
		if err != nil {
			continue
		}
		data, _ := os.ReadFile(fp)
		var sf sessionFileData
		json.Unmarshal(data, &sf)
		out = append(out, model.SessionInfo{
			Index:    e.Index,
			UUID:     sf.UUID,
			Preview:  sf.Preview,
			LastUsed: info.ModTime(),
			Expired:  time.Since(info.ModTime()) > uc.ttl,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

func (uc *UserContext) findIdentity(channel model.Channel, userID string) string {
	for id, ident := range uc.users {
		if b, ok := ident.Bindings[channel]; ok && b.UserID == userID {
			return id
		}
	}
	return ""
}

func (uc *UserContext) loadMap(identity string) *sessionMap {
	return loadSessionMap(filepath.Join(uc.mappingsDir, identity+".json"))
}

func (uc *UserContext) loadUsers() {
	data, err := os.ReadFile(uc.usersPath)
	if err != nil {
		uc.users = make(model.UserBindings)
		return
	}
	json.Unmarshal(data, &uc.users)
}

// cleanPreview 清洗 session 绑定前缀，用于存储 preview
func cleanPreview(content string) string {
	c := strings.TrimSpace(content)
	for _, p := range []string{"@", "会话", "切"} {
		if !strings.HasPrefix(c, p) {
			continue
		}
		rest := strings.TrimPrefix(c, p)
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i > 0 {
			return strings.TrimSpace(rest[i:])
		}
	}
	return c
}
