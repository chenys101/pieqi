package core

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"pieqi/internal/model"
)

func TestDiscoverPreview_Vite(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "src"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^5.0.0"}}`), 0644)
	_ = os.WriteFile(filepath.Join(dir, "vite.config.ts"), []byte(`export default { server: { port: 5188 } }`), 0644)

	p, err := DiscoverPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Framework != "vite" {
		t.Errorf("framework = %s", p.Framework)
	}
	if p.Port != 5188 {
		t.Errorf("port = %d, want 5188 (from vite.config.ts)", p.Port)
	}
	if len(p.Command) != 3 || p.Command[0] != "npm" {
		t.Errorf("command = %v", p.Command)
	}
}

func TestDiscoverPreview_DefaultPortAndRunner(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"scripts":{"dev":"next dev"},"dependencies":{"next":"14"}}`), 0644)
	_ = os.WriteFile(filepath.Join(dir, "pnpm-lock.yaml"), []byte(""), 0644)

	p, err := DiscoverPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Port != 3000 {
		t.Errorf("next default port = %d", p.Port)
	}
	if p.Command[0] != "pnpm" {
		t.Errorf("runner = %s, want pnpm (lockfile)", p.Command[0])
	}
}

func TestDiscoverPreview_FrontendSubdir(t *testing.T) {
	dir := t.TempDir()
	fe := filepath.Join(dir, "frontend")
	_ = os.MkdirAll(fe, 0755)
	_ = os.WriteFile(filepath.Join(fe, "package.json"),
		[]byte(`{"scripts":{"dev":"nuxt dev"},"devDependencies":{"nuxt":"3"}}`), 0644)

	p, err := DiscoverPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Framework != "nuxt" || p.Cwd != "frontend" {
		t.Errorf("profile wrong: %+v", p)
	}
}

func TestDiscoverPreview_NoDevScript(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"build":"x"}}`), 0644)
	if _, err := DiscoverPreview(dir); err == nil {
		t.Error("should be unavailable without dev script")
	}
}

func TestDiscoverPreview_OverrideFile(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"dev":"vite"}}`), 0644)
	_ = os.MkdirAll(filepath.Join(dir, ".pieqi"), 0755)
	_ = os.WriteFile(filepath.Join(dir, ".pieqi", "preview.json"),
		[]byte(`{"framework":"vite","command":["bun","run","dev"],"port":9999,"cwd":"."}`), 0644)

	p, err := DiscoverPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Command[0] != "bun" || p.Port != 9999 {
		t.Errorf("override not respected: %+v", p)
	}
}

// 静态目录（.pieqi/preview 含 .html）→ framework=static，无命令、无端口。
// 这是 agent 产出的方案对比页/HTML 报告能被手机打开的前提。
func TestDiscoverPreview_StaticDir(t *testing.T) {
	dir := t.TempDir()
	prop := filepath.Join(dir, filepath.FromSlash(PreviewStaticDir))
	if err := os.MkdirAll(prop, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(prop, "index.html"), []byte("<h1>方案 A</h1>"), 0644)

	p, err := DiscoverPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Framework != PreviewStatic {
		t.Fatalf("framework = %s, want static", p.Framework)
	}
	if p.Dir != PreviewStaticDir {
		t.Errorf("dir = %q, want %q", p.Dir, PreviewStaticDir)
	}
	if len(p.Command) != 0 || p.Port != 0 {
		t.Errorf("static 不应带命令/端口: %+v", p)
	}
}

// 约定目录是**显式**意图，必须压过 package.json 的推断（否则仓库有 dev server 时
// agent 放的方案页永远看不到）。
func TestDiscoverPreview_StaticDirBeatsDevServer(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^5.0.0"}}`), 0644)
	prop := filepath.Join(dir, filepath.FromSlash(PreviewStaticDir))
	_ = os.MkdirAll(prop, 0755)
	_ = os.WriteFile(filepath.Join(prop, "index.html"), []byte("x"), 0644)

	p, err := DiscoverPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Framework != PreviewStatic {
		t.Errorf("framework = %s, want static (约定目录优先级更高)", p.Framework)
	}
}

// 空目录不算（避免误判）：必须至少有一个 .html。
func TestDiscoverPreview_StaticDirWithoutHTML(t *testing.T) {
	dir := t.TempDir()
	prop := filepath.Join(dir, filepath.FromSlash(PreviewStaticDir))
	_ = os.MkdirAll(prop, 0755)
	if _, err := DiscoverPreview(dir); err == nil {
		t.Error("空静态目录应仍判 unavailable（无 package.json）")
	}
}

// preview.json 覆盖成 static + 自定义 dir。
func TestDiscoverPreview_StaticOverride(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".pieqi"), 0755)
	_ = os.MkdirAll(filepath.Join(dir, "out", "proposals"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "out", "proposals", "index.html"), []byte("x"), 0644)
	_ = os.WriteFile(filepath.Join(dir, ".pieqi", "preview.json"),
		[]byte(`{"framework":"static","dir":"out/proposals"}`), 0644)

	p, err := DiscoverPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Framework != PreviewStatic || p.Dir != "out/proposals" {
		t.Errorf("profile = %+v", p)
	}
}

// static 实例：Start 即 running、无端口、StaticDir 可用；终态不回收；Stop 后不可 serve。
func TestPreviewManager_StaticLifecycle(t *testing.T) {
	wt := t.TempDir()
	prop := filepath.Join(wt, filepath.FromSlash(PreviewStaticDir))
	_ = os.MkdirAll(prop, 0755)
	_ = os.WriteFile(filepath.Join(prop, "index.html"), []byte("x"), 0644)

	pm := NewPreviewManager(nil)
	task := &model.Task{ID: "t-static", WorktreePath: wt}
	if err := pm.Start(task); err != nil {
		t.Fatalf("start: %v", err)
	}

	if st := pm.Status(task); st.State != PreviewRunning || st.Framework != PreviewStatic {
		t.Fatalf("status = %+v, want running/static", st)
	}
	if pm.RunningPort("t-static") != 0 {
		t.Error("static 不应有端口")
	}
	if pm.StaticDir("t-static") == "" {
		t.Fatal("running 时 StaticDir 应可用")
	}
	if !pm.Running("t-static") {
		t.Error("Running 应为 true（Attach 前置条件按状态判定，不能按端口）")
	}

	// 任务终态：静态预览不回收（方案页任务完成后仍要能看）
	pm.stopIfProcess("t-static")
	if !pm.Running("t-static") {
		t.Error("静态预览不应随任务终态回收")
	}

	pm.Stop("t-static")
	if st := pm.Status(task); st.State != PreviewStopped {
		t.Errorf("stop 后 state = %s, want stopped", st.State)
	}
	if pm.StaticDir("t-static") != "" {
		t.Error("stopped 后不应再 serve")
	}

	// 删除任务 → 登记也清掉，避免 map 线性泄漏。
	// 注意：Forget 之后 Status 会**重新认出并自动登记**（静态产物默认可访问，
	// 见 Status 里的说明）—— 所以这里断言的是"清掉不影响可访问性"，
	// 而不是"清掉后变回 available"。
	pm.Forget("t-static")
	if st := pm.Status(task); st.State != PreviewRunning {
		t.Errorf("forget 后 state = %s, want running（静态产物默认可访问）", st.State)
	}
}

func TestAppendScriptArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"npm run dev", []string{"npm", "run", "dev"}, []string{"npm", "run", "dev", "--", "--port", "4000"}},
		{"pnpm run dev", []string{"pnpm", "run", "dev"}, []string{"pnpm", "run", "dev", "--", "--port", "4000"}},
		{"yarn run dev", []string{"yarn", "run", "dev"}, []string{"yarn", "run", "dev", "--", "--port", "4000"}},
		{"override custom runner", []string{"bun", "run", "dev"}, []string{"bun", "run", "dev", "--", "--port", "4000"}},
		{"already has separator", []string{"npm", "run", "dev", "--", "--host", "0.0.0.0"}, []string{"npm", "run", "dev", "--", "--host", "0.0.0.0", "--port", "4000"}},
		{"non-run command", []string{"bun", "dev"}, []string{"bun", "dev", "--port", "4000"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := appendScriptArgs(c.args, "--port", "4000"); !reflect.DeepEqual(got, c.want) {
				t.Errorf("appendScriptArgs(%v) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

func TestPreviewEnv_StripsSecrets(t *testing.T) {
	t.Setenv("TUNNEL_TOKEN", "secret")
	t.Setenv("PIEQI_HOME", "/x")
	t.Setenv("BRIDGE_TOKEN", "y")
	env := previewEnv()
	for _, kv := range env {
		if kv == "TUNNEL_TOKEN=secret" || kv == "PIEQI_HOME=/x" || kv == "BRIDGE_TOKEN=y" {
			t.Errorf("secret leaked into preview env: %s", kv)
		}
	}
}

// Windows 关键回归：本机 PATH 实际写作 "Path"，匹配若大小写敏感会整条丢弃
// （npm/node 全找不到）；且 TEMP/SystemRoot 等 Windows 基础变量此前不在白名单里。
func TestFilterPreviewEnv_WindowsCaseInsensitive(t *testing.T) {
	in := []string{
		`Path=C:\Windows\system32;C:\node`,
		`TEMP=C:\Users\x\AppData\Local\Temp`,
		`SystemRoot=C:\Windows`,
		`PATHEXT=.COM;.EXE`,
		`ComSpec=C:\Windows\system32\cmd.exe`,
		`NODE_ENV=production`,
		`TUNNEL_TOKEN=secret`,
		`npm_token=secret2`,
		`PIEQI_HOME=C:\x`,
		`bridge_port=1`,
		`UNRELATED=drop`,
	}
	out := filterPreviewEnv(in)

	if !envHasKey(out, "PATH") {
		t.Fatal("Windows 形态的 Path 被丢弃（大小写敏感匹配回归）")
	}
	for _, k := range []string{"TEMP", "SYSTEMROOT", "PATHEXT", "COMSPEC", "NODE_ENV"} {
		if !envHasKey(out, k) {
			t.Errorf("应保留 %s，实际被丢弃", k)
		}
	}
	for _, k := range []string{"TUNNEL_TOKEN", "NPM_TOKEN", "PIEQI_HOME", "BRIDGE_PORT", "UNRELATED"} {
		if envHasKey(out, k) {
			t.Errorf("%s 不应出现在 preview 环境里", k)
		}
	}
}

// 环境里完全没有 PATH 时才补兜底项。
func TestFilterPreviewEnv_NoPathFallback(t *testing.T) {
	out := filterPreviewEnv([]string{"NODE_ENV=dev"})
	if !envHasKey(out, "PATH") {
		t.Fatal("无 PATH 时应兜底补一条，否则 dev server 找不到可执行文件")
	}
}

// Unix 形态不得被 Windows 补丁改坏。
func TestFilterPreviewEnv_UnixStillWorks(t *testing.T) {
	out := filterPreviewEnv([]string{
		`PATH=/usr/local/bin:/usr/bin`,
		`HOME=/root`,
		`TMPDIR=/tmp`,
		`XDG_CONFIG_HOME=/root/.config`,
		`TUNNEL_TOKEN=secret`,
	})
	for _, k := range []string{"PATH", "HOME", "TMPDIR", "XDG_CONFIG_HOME"} {
		if !envHasKey(out, k) {
			t.Errorf("Unix 形态应保留 %s", k)
		}
	}
	if envHasKey(out, "TUNNEL_TOKEN") {
		t.Error("TUNNEL_TOKEN 泄漏")
	}
}

// envHasKey 在一份 "K=V" 快照里按大小写不敏感查找 key。
func envHasKey(environ []string, key string) bool {
	for _, kv := range environ {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

// --- 位置容错（agent 把方案页写到了别处） ---
//
// 这一组钉住的核心约定：**预览能否打开不该取决于 agent 是否记住了魔法路径**。
// 发现层认领候选目录；认不出时 Status 必须给出可行动的指路信息（权威路径 +
// 已发现的疑似页面），而不是静默 unavailable。

// TestDiscoverPreview_StaticDirCandidates 常见"写偏了"的目录都要认。
func TestDiscoverPreview_StaticDirCandidates(t *testing.T) {
	cases := []struct {
		name string
		rel  string // 相对 worktree 的 html 路径
		want string // 期望被认领的目录
	}{
		{"权威目录", ".pieqi/preview/index.html", ".pieqi/preview"},
		{"复数 pieqi", ".pieqi/previews/index.html", ".pieqi/previews"},
		{"pieqi 下的 proposals", ".pieqi/proposals/a.html", ".pieqi/proposals"},
		{"根 proposals", "proposals/index.html", "proposals"},
		{"根 previews", "previews/index.html", "previews"},
		{"docs 下", "docs/proposals/v1.html", "docs/proposals"},
		{"前端 public 下", "web/public/proposals/index.html", "web/public/proposals"},
		// 一层子目录（多方案写成 variant-a/index.html）也要认
		{"嵌套 variant", ".pieqi/preview/variant-a/index.html", ".pieqi/preview"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			full := filepath.Join(dir, filepath.FromSlash(c.rel))
			if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("<h1>x</h1>"), 0644); err != nil {
				t.Fatal(err)
			}
			p, err := DiscoverPreview(dir)
			if err != nil {
				t.Fatalf("应识别为 static: %v", err)
			}
			if p.Framework != PreviewStatic || p.Dir != c.want {
				t.Errorf("profile = %+v, want static dir=%s", p, c.want)
			}
		})
	}
}

// TestDiscoverPreview_StaticCandidatePriority 多个候选同时存在时按优先级取权威目录，
// 避免"哪个先被扫到就用哪个"导致同一 worktree 两次 discovery 结果不同。
func TestDiscoverPreview_StaticCandidatePriority(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{".pieqi/preview/index.html", "proposals/index.html"} {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, []byte("x"), 0644)
	}
	p, err := DiscoverPreview(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != PreviewStaticDir {
		t.Errorf("dir = %q, want %q（权威目录优先）", p.Dir, PreviewStaticDir)
	}
}

// TestPreviewStatus_UnavailableGivesHint 认不出时必须指路：权威目录 + 疑似页面。
func TestPreviewStatus_UnavailableGivesHint(t *testing.T) {
	wt := t.TempDir()
	// agent 把对比页丢在 docs 下，且不在任何候选目录里
	loose := filepath.Join(wt, "docs", "ui", "proposal-draft.html")
	_ = os.MkdirAll(filepath.Dir(loose), 0755)
	_ = os.WriteFile(loose, []byte("x"), 0644)

	task := &model.Task{ID: "t-hint", WorktreePath: wt}
	st := NewPreviewManager(nil).Status(task)

	if st.State != PreviewUnavailable {
		t.Fatalf("state = %s, want unavailable", st.State)
	}
	if st.Convention != PreviewStaticDir {
		t.Errorf("convention = %q, want %q", st.Convention, PreviewStaticDir)
	}
	if !strings.Contains(st.Hint, PreviewStaticDir) {
		t.Errorf("hint 必须点名权威目录，否则用户不知道往哪放: %q", st.Hint)
	}
	if len(st.Suspected) == 0 || !strings.Contains(strings.Join(st.Suspected, ","), "proposal-draft.html") {
		t.Errorf("应报告疑似方案页（相对路径）: %+v", st.Suspected)
	}
}

// TestSuspectedPreviewPages_SkipsNoise 依赖目录/构建产物里的同名文件不能当成提示，
// 否则每个 node_modules 都会让 hint 变成噪声。
func TestSuspectedPreviewPages_SkipsNoise(t *testing.T) {
	wt := t.TempDir()
	for _, rel := range []string{
		"node_modules/pkg/proposal.html",
		"dist/preview.html",
		".git/preview-compare.html",
	} {
		full := filepath.Join(wt, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, []byte("x"), 0644)
	}
	if got := SuspectedPreviewPages(wt); len(got) != 0 {
		t.Errorf("噪声目录不应被报告: %+v", got)
	}
}

// TestEnsureStatic 静态产物"默认可访问"的唯一入口：
//   - 从未启动过 → 就地登记并返回目录（面板一打开就是 running）；
//   - 用户显式 Stop 过 → 不自动拉起（否则「停止」按钮看起来没反应）；
//   - 没有静态产物的 task → 返回 ""（不能凭空造一个实例）。
func TestEnsureStatic(t *testing.T) {
	wt := t.TempDir()
	prop := filepath.Join(wt, filepath.FromSlash(PreviewStaticDir))
	_ = os.MkdirAll(prop, 0755)
	_ = os.WriteFile(filepath.Join(prop, "index.html"), []byte("x"), 0644)

	pm := NewPreviewManager(nil)
	task := &model.Task{ID: "t-ensure", WorktreePath: wt}

	if dir := pm.EnsureStatic(task); dir == "" {
		t.Fatal("有静态产物目录时应就地登记并返回目录")
	}
	if !pm.Running("t-ensure") {
		t.Error("EnsureStatic 之后应为 running")
	}
	// 幂等：再调一次不应改变状态/重复登记
	if dir := pm.EnsureStatic(task); dir == "" || !pm.Running("t-ensure") {
		t.Error("EnsureStatic 应幂等")
	}

	// 显式停止后不自动拉起
	pm.Stop("t-ensure")
	if dir := pm.EnsureStatic(task); dir != "" {
		t.Errorf("用户显式停止后不应自动拉起, got %q", dir)
	}

	// 没有静态产物的 task：不造实例
	bare := t.TempDir()
	if dir := pm.EnsureStatic(&model.Task{ID: "t-bare", WorktreePath: bare}); dir != "" {
		t.Errorf("无静态产物应返回空串, got %q", dir)
	}
	if pm.Running("t-bare") {
		t.Error("无静态产物不应登记实例")
	}
}
