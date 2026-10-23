// attachments.go 工具结果图片的**内容寻址**存储。
//
//	<dataRoot>/attachments/<sha256>.<ext>
//
// 为什么按内容寻址而不是"任务目录 + 序号"：
//   - 同一张图被两次 read_image 读到只存一份（工具调用常常重复读同一个文件）；
//   - 任务被打断、重启、甚至任务 JSON 被重写，附件都不动 —— 事实源（TaskEvent）
//     里只留引用，重写事件流不会碰到字节；
//   - 回滚/删除任务**不删附件**：删了会让仍在页面上的旧事件变成破图。
//     清理属于独立的运维动作（按 mtime 归档），不塞进任务生命周期里做。
//
// 与 model.TaskImage 的关系：事件里存的是元数据（含 Hash 与 Stored），
// 字节只在磁盘。这一点与"绝不把 base64 写进 TaskEvent"是同一条纪律
// （理由见 model.TaskEvent.Images 的说明：会把任务 JSON 撑到几十 MB）。
package core

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AttachmentStore 图片附件存储。零值不可用，用 NewAttachmentStore 构造。
type AttachmentStore struct {
	root string // <dataRoot>/attachments
}

// NewAttachmentStore 创建附件存储。root 为空时退到 <dataRoot>/attachments。
func NewAttachmentStore(root string) *AttachmentStore {
	if root == "" {
		root = filepath.Join(defaultFeedbackDataRoot(), "attachments")
	}
	return &AttachmentStore{root: root}
}

// Root 返回附件根目录（诊断/测试用）。
func (s *AttachmentStore) Root() string { return s.root }

// ErrInvalidAttachmentHash 附件 hash 形状不合法（不是 64 位 hex）。
//
// 单独一个哨兵：它是**调用方传错**（路径参数被注入）而不是"文件不存在"，
// HTTP 层要回 400 而不是 404 —— 两者混在一起会让"有人在探测路径"看起来像"图没了"。
var ErrInvalidAttachmentHash = errors.New("invalid attachment hash")

// attachmentExtByMime 允许落盘的图片类型 → 扩展名。
//
// 与 agent.imageMimeWhitelist（出站图片）保持一致：能发给 agent 的类型
// 就是能展示的类型，多一种或少一种都会形成"发得出去但看不回来"的落差。
var attachmentExtByMime = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// Save 写入一张 base64 图片，返回元数据（Hash 必然非空且 Stored=true）。
//
// 已存在同 hash 的文件时**不重复写**（同一张图重复读只占一份磁盘）。
//
// 原子写：先写 .tmp 再 rename。半截文件会让 <img> 一直加载失败，而界面上
// 只看得到"图坏了"，没有任何线索指向磁盘 —— 这类故障最难排查。
func (s *AttachmentStore) Save(dataB64, mimeType, name string) (hash string, bytes int, err error) {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	ext, ok := attachmentExtByMime[mimeType]
	if !ok {
		return "", 0, fmt.Errorf("unsupported attachment mime %q", mimeType)
	}
	raw, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return "", 0, fmt.Errorf("decode attachment: %w", err)
	}
	if len(raw) == 0 {
		return "", 0, errors.New("empty attachment")
	}
	sum := sha256.Sum256(raw)
	hash = hex.EncodeToString(sum[:])

	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return "", 0, fmt.Errorf("mkdir attachments: %w", err)
	}
	dst := filepath.Join(s.root, hash+ext)
	if st, statErr := os.Stat(dst); statErr == nil && st.Size() == int64(len(raw)) {
		return hash, len(raw), nil // 已有完整的一份
	}
	tmp, err := os.CreateTemp(s.root, hash+".*.tmp")
	if err != nil {
		return "", 0, fmt.Errorf("create temp attachment: %w", err)
	}
	tmpName := tmp.Name()
	// 失败路径一律清掉临时文件：留下的一堆 .tmp 会被当成"附件"扫进任何按目录枚举的逻辑。
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err = tmp.Write(raw); err != nil {
		return "", 0, fmt.Errorf("write attachment: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return "", 0, fmt.Errorf("close attachment: %w", err)
	}
	if err = os.Rename(tmpName, dst); err != nil {
		return "", 0, fmt.Errorf("rename attachment: %w", err)
	}
	return hash, len(raw), nil
}

// Read 按 hash 取回图片字节与其类型。
//
// 返回 (nil, "", nil) 表示**没有这张图**（调用方回 404）；
// (nil, "", ErrInvalidAttachmentHash) 表示 hash 形状不合法（调用方回 400）。
//
// 不按扩展名猜路径：hash 与扩展名一起进路径会让"同一张图存成两种扩展名"
// 变成两个文件。这里按 hash 前缀扫一遍目录 —— 目录条目数量级是"上千"，
// 一次 ReadDir 的代价远小于维护一张索引表。
func (s *AttachmentStore) Read(hash string) ([]byte, string, error) {
	if !validAttachmentHash(hash) {
		return nil, "", ErrInvalidAttachmentHash
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("read attachments dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasPrefix(n, hash) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(n))
		mime := ""
		for m, x := range attachmentExtByMime {
			if x == ext {
				mime = m
				break
			}
		}
		if mime == "" {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(s.root, n))
		if readErr != nil {
			return nil, "", fmt.Errorf("read attachment: %w", readErr)
		}
		return raw, mime, nil
	}
	return nil, "", nil
}

// ValidAttachmentHash 校验 sha256 hex（64 位）。大小写都接受，调用方应先用
// NormalizeAttachmentHash 规整。导出供 API 层在拼路径之前判形状。
func ValidAttachmentHash(hash string) bool { return validAttachmentHash(hash) }

// validAttachmentHash 校验 sha256 hex（64 位小写）。大小写都接受，内部统一小写。
//
// 这条校验是**路径安全的唯一依据**：hash 会被直接拼进文件路径，
// 放行 `..` 或分隔符等于让人用 URL 读任意文件。
func validAttachmentHash(hash string) bool {
	if len(hash) != sha256.Size*2 {
		return false
	}
	for _, c := range hash {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// NormalizeAttachmentHash 把 hash 规整成小写（不合法时原样返回，由调用方判错）。
func NormalizeAttachmentHash(hash string) string {
	return strings.ToLower(strings.TrimSpace(hash))
}
