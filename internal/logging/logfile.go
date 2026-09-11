// Package logging 提供按天切分的日志文件落盘与「诊断导出」。
//
// 为什么需要它：进程原本只往控制台写日志（zap.NewProduction/Development）。
// 控制台的日志在 systemd / 终端滚动后就没有了，而"排查问题"恰恰发生在事后 ——
// 所以设置页那个「导出诊断日志」按钮背后必须真的有文件可导。
//
// 刻意不用第三方的滚动库（lumberjack 等）：这里要的只是「按天一个文件 + 删旧的」，
// 用标准库写清楚比引入依赖更容易验证，也不会与 zap 的 Sync 语义打架。
package logging

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// FilePrefix 日志文件名的前缀；完整名为 "<prefix>YYYY-MM-DD.log"。
const FilePrefix = "pieqi-"

// DailyFileSink 是 zap 的 WriteSyncer：写入 <dir>/pieqi-YYYY-MM-DD.log，
// 跨天自动换文件。进程内单实例，所有写入串行化（zap 本身会并发调用）。
type DailyFileSink struct {
	mu      sync.Mutex
	dir     string
	day     string
	f       *os.File
	nowFunc func() time.Time // 测试注入
}

// NewDailyFileSink 创建（并确保目录存在）。
func NewDailyFileSink(dir string) (*DailyFileSink, error) {
	if dir == "" {
		return nil, fmt.Errorf("log dir is required")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir log dir: %w", err)
	}
	return &DailyFileSink{dir: dir, nowFunc: time.Now}, nil
}

// Dir 返回日志目录。
func (s *DailyFileSink) Dir() string { return s.dir }

// Write 实现 io.Writer。跨天时先关旧文件再开新的。
func (s *DailyFileSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.rotateLocked(); err != nil {
		return 0, err
	}
	return s.f.Write(p)
}

// Sync 实现 zapcore.WriteSyncer。
// Windows 上对普通文件 Sync 会返回"句柄无效"之类的错误，直接吞掉 ——
// 刷盘失败不该让日志写入整体失败。
func (s *DailyFileSink) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	_ = s.f.Sync()
	return nil
}

// Close 关闭当前文件句柄。
func (s *DailyFileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}

func (s *DailyFileSink) rotateLocked() error {
	day := s.nowFunc().Format("2006-01-02")
	if s.f != nil && s.day == day {
		return nil
	}
	if s.f != nil {
		_ = s.f.Close()
		s.f = nil
	}
	f, err := os.OpenFile(filepath.Join(s.dir, FilePrefix+day+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	s.f = f
	s.day = day
	return nil
}

// NewLogger 构建同时写控制台与按天日志文件的 logger。
//
// 返回的 close 负责 Sync + 关闭文件句柄（main 的 defer 调用）。
// 文件 sink 建不起来（磁盘/权限问题）时**降级为仅控制台**并返回 nil close ——
// 日志落不了盘不该让整个服务起不来。
func NewLogger(mode, logDir string) (*zap.Logger, func(), error) {
	var encCfg zapcore.EncoderConfig
	var enc zapcore.Encoder
	var level zapcore.Level
	if mode == "release" {
		encCfg = zap.NewProductionEncoderConfig()
		enc = zapcore.NewJSONEncoder(encCfg)
		level = zapcore.InfoLevel
	} else {
		encCfg = zap.NewDevelopmentEncoderConfig()
		encCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
		enc = zapcore.NewConsoleEncoder(encCfg)
		level = zapcore.DebugLevel
	}
	console := zapcore.Lock(os.Stderr)
	cores := []zapcore.Core{zapcore.NewCore(enc, console, level)}

	sink, err := NewDailyFileSink(logDir)
	if err != nil {
		return zap.New(zapcore.NewTee(cores...), zap.AddCaller()), func() {}, nil
	}
	// 文件里不带颜色码（控制台那套 EncodeLevel 会把 ANSI 写进文件）。
	fileCfg := encCfg
	fileCfg.EncodeLevel = zapcore.CapitalLevelEncoder
	fileEnc := zapcore.NewJSONEncoder(fileCfg)
	if mode != "release" {
		fileEnc = zapcore.NewConsoleEncoder(fileCfg)
	}
	cores = append(cores, zapcore.NewCore(fileEnc, sink, level))

	logger := zap.New(zapcore.NewTee(cores...), zap.AddCaller(), zap.AddCallerSkip(0))
	closeFn := func() {
		_ = logger.Sync()
		_ = sink.Close()
	}
	return logger, closeFn, nil
}

// LogFileName 返回某一天的日志文件名。
func LogFileName(day time.Time) string { return FilePrefix + day.Format("2006-01-02") + ".log" }

// BuildDiagnosticsZip 把最近 days 天（含今天）的日志打包成一个 zip 写入 w。
//
// 只按**文件名里的日期**挑选，不看 mtime：文件名是这套命名的事实来源，
// 而 mtime 会因为拷贝 / 恢复而集体失真。
//
// 一天都没有时仍生成一个只含 meta 的 zip（而不是报错）—— "没有日志"本身
// 就是排查结论的一部分，用户拿到空包比拿到 500 更有用。
func BuildDiagnosticsZip(w io.Writer, logDir string, now time.Time, days int, meta map[string]any) error {
	zw := zip.NewWriter(w)

	if meta != nil {
		fw, err := zw.Create("meta.json")
		if err != nil {
			return err
		}
		buf, _ := json.MarshalIndent(meta, "", "  ")
		if _, err := fw.Write(buf); err != nil {
			return err
		}
	}

	wanted := map[string]bool{}
	for i := 0; i < days; i++ {
		wanted[LogFileName(now.AddDate(0, 0, -i))] = true
	}

	entries, err := os.ReadDir(logDir)
	if err != nil {
		// 目录不存在 = 还没有日志文件；照常产出（可能只剩 meta）。
		entries = nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if wanted[name] && strings.HasPrefix(name, FilePrefix) && strings.HasSuffix(name, ".log") {
			names = append(names, name)
		}
	}
	sort.Strings(names) // 时间正序，解压后按时间读

	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(logDir, name))
		if err != nil {
			continue // 单个文件读不到就跳过，不为它整包失败
		}
		fw, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := fw.Write(data); err != nil {
			return err
		}
	}
	return zw.Close()
}
