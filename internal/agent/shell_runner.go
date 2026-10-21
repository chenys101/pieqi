package agent

import "strings"

// 测试/构建/类型检查类命令的分档（2026-10-21，见 ADR-0015）。
//
// ## 为什么要单独一层
//
// ADR-0008 的只读白名单只认"读取类"命令，package runner（npm/npx/pnpm/yarn）
// 与 `node`/`python` 一律不在表里 ⇒ 落 L2(execute) ⇒ 每次都要人工点。
//
// 实测代价（2026-10-20~21 的 dsh 会话，101 次审批）：**67 次是纯测试/构建**
// （`npm run test` / `npm run typecheck` / `npm run build` / `npx vitest run …` /
// `npx playwright test --project=…`），占 66%。其中 12 次因为"要跑一条测试就得
// 申请全盘提权"而被取消、每条卡满 1800s 超时 —— 一整个下午做不了验收。
//
// ## 判据：只看**实际会跑什么**，不按前缀放行
//
// `npm run <x>` 跑的是 `package.json` 的 scripts 里那一行；`npx <x>` 可能先联网装包；
// `npm run test -- --watch` 会挂住不动。所以这里：
//   - 脚本名必须在**固定**的分档表里（test / typecheck / build …），
//     不认 `npm run 任意脚本` —— 那等于把 package.json 变成免审白名单；
//   - 参数里出现 `install`/`add`/`-g`/`--watch`/`audit`/`publish` 等动作即拒绝；
//   - 认不出来就不降级（维持 execute/L2），与 ADR-0008 同一取向。
//
// ## 档位
//
// 返回值是 kind，必须落在 `riskLevelKinds` 认识的集合里（否则静默落 L2）：
//
//	read     只读地跑（`npm test` 不写工作区……实际上 vite/vitest 会写 node_modules/.vite
//	         缓存与测试报告，但那都在被忽略的构建产物里，且可重生成 ⇒ 归 L1 更诚实）
//	edit     "本地可逆"（与 git add/commit 借用同一个 L1 kind 的理由相同，见 git_kind.go）
//	execute  维持人工审批
//
// 测试与类型检查归 **read**（不改任何源文件，失败也没有副作用）；
// `npm run build` 归 **edit**（写 `web/dist` 等构建产物 —— 可重生成，但确实是"写了东西"）。

// scriptKinds 是 `npm run <script>` / `npm run-script <script>` 的脚本分档表。
//
// 只收"跑一次只会读源码 + 写构建产物/缓存"的名字。刻意**不收**：
//   - 裸的 `start`/`dev`/`serve`：它们起常驻 dev server，会一直占着端口/进程；
//   - `deploy`/`publish`/`release`/`push`：影响外部；
//   - `lint:fix`/`format`/`fmt` 这类会**改写源文件**的（要审）。
var scriptKinds = map[string]string{
	// ---- 纯验证：不改源码，失败也没有副作用 ----
	"test":      "read",
	"test:unit": "read",
	"test:all":  "read",
	"vitest":    "read",
	"jest":      "read",
	"typecheck": "read",
	"tsc":       "read",
	"check":     "read",
	"lint":      "read", // 只报不改；`lint:fix` 单独不在表里
	"vet":       "read",

	// ---- 构建：写构建产物（可重生成）----
	"build":       "edit",
	"build:web":   "edit",
	"build:front": "edit",
	"compile":     "edit",
}

// runnerDangerous 是 package runner 参数里出现即**拒绝降级**的词。
//
// 覆盖三类：
//   - 装/改依赖（`npm install`、`npx --package x`）—— 写 node_modules、可能联网；
//   - 常驻/交互（`--watch`、`--ui`、`--update-snapshots` 之外的 `-u` 暂不列）；
//   - 对外发布（`publish`、`audit fix`）。
var runnerDangerous = []string{
	"install", "i", "add", "remove", "rm", "uninstall", "unlink", "link",
	"update", "upgrade", "dedupe", "prune", "ci",
	"publish", "pack", "audit", "exec",
	"--global", "-g", "--watch", "--ui", "--dev", "--save", "-S", "-D",
	"--package", "-p", "--registry",
}

// npxSubKinds 是 `npx <bin>` 里**明确安全**的 bin 名单（按首词匹配）。
//
// npx 的默认行为是"本地没有就联网下载并执行" —— 那是任意代码执行，
// 绝不能按前缀放行。所以只认这几个确知是本仓库 devDependencies 里的测试/构建器，
// 且**必须先出现在 package.json 的 devDependencies**（运行时无法查，这里按仓库现状固定）。
var npxSubKinds = map[string]string{
	"vitest":     "read",
	"tsc":        "read",
	"vue-tsc":    "read",
	"playwright": "read", // `npx playwright test …`
	"eslint":     "read",
	"vite":       "edit", // `npx vite build`
}

// playwrightDangerous 是 `playwright` 子命令里的例外：这些会**改写基线/报告目录**。
var playwrightDangerous = []string{"install", "uninstall", "open", "codegen", "merge-reports"}

// packageRunnerKindFor 判断一个 package runner 段是否可降级。
//
// 入参 words 是从**真正的命令名**开始的参数切片（已剥掉 wrapper/赋值前缀）。
// 第二返回值 false 表示"不归本层管或认不出来" —— 调用方继续走只读白名单/保守判级。
func packageRunnerKindFor(words []string) (string, bool) {
	if len(words) == 0 {
		return "", false
	}
	name := baseCommandName(words[0])
	args := words[1:]

	switch name {
	case "npm", "pnpm", "yarn":
		return npmLikeKindFor(name, args)
	case "npx", "pnpx":
		return npxKindFor(args)
	}
	return "", false
}

// npmLikeKindFor 处理 `npm run <script>` 这一类。
func npmLikeKindFor(name string, args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	if hasAnyWord(args, runnerDangerous) {
		return "", false
	}

	// 允许 `npm run test -- --run` 这种把参数透传给底层工具的写法，
	// 但仍要看**整个**参数列表有没有危险词（上面已查）。
	nonFlag := nonFlagWords(args)

	// `npm test` / `yarn test` 简写：脚本名就是唯一那个非标志词。
	// 注意 `npm test -- --run` 里 nonFlag 是 ["test"]（`--` 之后的全是标志）。
	// 脚本名也可能带版本/范围后缀（`vitest@2`），比较前剥掉。
	scriptBase := func(s string) string {
		if i := strings.IndexByte(s, '@'); i > 0 {
			return s[:i]
		}
		return s
	}

	var script string
	switch name {
	case "npm", "pnpm":
		if len(args) < 2 || (args[0] != "run" && args[0] != "run-script") {
			// `npm test` / `npm t` 是 `npm run test` 的别名。
			if len(nonFlag) == 1 && (nonFlag[0] == "test" || nonFlag[0] == "t") {
				return "read", true
			}
			return "", false
		}
		script = args[1]
	case "yarn":
		// yarn 的 `run` 可省略：`yarn test` == `yarn run test`。
		if len(args) >= 1 && (args[0] == "run" || args[0] == "run-script") {
			if len(args) < 2 {
				return "", false
			}
			script = args[1]
		} else if len(nonFlag) == 1 {
			script = nonFlag[0]
		} else {
			return "", false
		}
	}
	kind, ok := scriptKinds[strings.ToLower(scriptBase(script))]
	if !ok {
		return "", false
	}
	return kind, true
}

// npxKindFor 处理 `npx <bin> …`。
func npxKindFor(args []string) (string, bool) {
	nonFlag := nonFlagWords(args)
	if len(nonFlag) == 0 || hasAnyWord(args, runnerDangerous) {
		return "", false
	}
	bin := strings.ToLower(nonFlag[0])
	kind, ok := npxSubKinds[bin]
	if !ok {
		return "", false
	}
	// 这些子命令会写基线/报告/安装浏览器，不能降。
	if bin == "playwright" && hasAnyWord(nonFlag[1:], playwrightDangerous) {
		return "", false
	}
	// `npx vitest --update` / `-u` 会改写快照（视觉/单元基线都是"别人可见的东西"）。
	if (bin == "vitest" || bin == "jest" || bin == "playwright") &&
		hasAnyWord(args, []string{"--update", "-u", "--update-snapshots", "--updateSnapshot"}) {
		return "", false
	}
	return kind, true
}

// scriptRunnerKindFor 处理"解释器 + 一段内联代码"：`node -e "…"`、`python -c "…"`。
//
// 判据：内联代码里**只出现读取**。看着粗糙，但它解决的问题很实：agent 用
// `node -e "…readFileSync…console.log…"` 现查一个文件的编译产物，原来一律弹卡。
//
// 拒绝面（宁多勿少）：
//   - 任何非 `-e`/`-c` 的形态（`node script.js` 可能干任何事）；
//   - 代码里出现写/执行动词：write/append/rename/unlink/mkdir/rm/cp/spawn/exec/
//     eval/require('child_process')/open(… 'w')/fsync/symlink/chmod…
//
// 这不是沙箱，只是个启发式 —— 越权方永远能用字符串拼接绕过；但绕过者要
// **主动**把动词藏起来，而"误放行"的门槛因此从"看起来只读"抬到了"证明不了就弹卡"。
func scriptRunnerKindFor(words []string) (string, bool) {
	if len(words) == 0 {
		return "", false
	}
	name := baseCommandName(words[0])
	if name != "node" && name != "nodejs" && name != "python" && name != "python3" && name != "py" {
		return "", false
	}
	// 只认 `-e`/`-c` 紧跟一段代码；`--eval`/`--print` 也行（都不落文件）。
	var code string
	for i := 1; i < len(words); i++ {
		w := words[i]
		if w == "-e" || w == "-c" || w == "--eval" {
			if i+1 < len(words) {
				code = words[i+1]
			}
			break
		}
		// `-e<code>` 连写形态
		if strings.HasPrefix(w, "-e") && len(w) > 2 {
			code = w[2:]
			break
		}
	}
	if code == "" {
		return "", false // 没有内联代码：`node x.js` 之类，不归本层
	}
	if strings.Contains(code, "${") || strings.Contains(code, "$(") {
		return "", false // 变量展开/命令替换会藏真东西
	}
	// 注意：分类器送给这里的是**已剥掉引号内容**的段（`node -e ""`），
	// 那段代码已经看不见了。所以**本函数从不放行** —— 它只在满足全部条件时
	// 返回("", true)，把"这是内联代码"这件事告诉调用方（segmentKind 把 true
	// 当作"不确定"处理）。真正判它只读的地方只有一个：
	// DowngradeReadonlyPermission（原文还完整时跑 permissionReqHasUnsafeText）。
	lower := strings.ToLower(code)
	for _, bad := range inlineCodeDangerous {
		if strings.Contains(lower, bad) {
			return "", false
		}
	}
	_ = lower
	return "", true
}

// inlineCodeDangerous 是内联代码里"一旦出现就说明它能写/能执行"的片段。
// 全部小写比较（调用方已 lower）。
//
// ⚠️ 它现在只作用于"原文还完整"的场景（见 DowngradeReadonlyPermission 的原文扫描），
// 分类器拿到的段里代码已被剥空，永远看不到这些词 —— 所以**不要**把"这里没命中"
// 当成"代码是安全的"（ADR-0015 踩过：曾据此把 `node -e "writeFileSync…"` 判成 read）。
var inlineCodeDangerous = []string{
	"write", "append", "createwrite", "writefile", "writeall", "mkdir", "makedirs",
	"rename", "unlink", "remove", "rmdir", "rm ", "rmtree", "delete", "truncate",
	"chmod", "chown", "symlink", "link(", "copy", "copyfile", "cp ",
	"open(", "open (", "'w'", "\"w\"", "'a'", "\"a\"", "fsync", "sync(", "touch",
	"spawn", "exec", "child_process", "eval(", "eval (", "function(", "import(",
	"require('http", "socket", "fetch(", "http", "popen", "system(", "subprocess",
	"os.remove", "shutil", "tempfile", "pipe(",
}

// hasAnyWord 判断 args 里是否出现 words 中的任一项（**整词**或标志等值形态）。
//
// 整词比较而不是子串：`i` 是 `npm i` 的写动作，但子串匹配会把
// `--inspect`、`vitest` 里的 i 也当成危险词。
func hasAnyWord(args []string, words []string) bool {
	set := make(map[string]struct{}, len(words))
	for _, w := range words {
		set[strings.ToLower(w)] = struct{}{}
	}
	for _, a := range args {
		la := strings.ToLower(a)
		if _, ok := set[la]; ok {
			return true
		}
		// `--global=true` / `install@latest` 这类：取 `=` 前与 `@` 前再比一次。
		if i := strings.IndexAny(la, "="); i > 0 {
			if _, ok := set[la[:i]]; ok {
				return true
			}
		}
		if i := strings.IndexByte(la, '@'); i > 0 {
			if _, ok := set[la[:i]]; ok {
				return true
			}
		}
	}
	return false
}

// nonFlagWords 返回 args 里的非标志词（不以下划线/减号开头）。
func nonFlagWords(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, a)
	}
	return out
}
