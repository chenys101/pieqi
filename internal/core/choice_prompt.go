package core

import (
	"regexp"
	"strings"
)

// choice_prompt.go 实现「多选一交互」（路径 B）：Claude 按约定的 [CHOICE] 格式输出
// 提问，后端在 result 事件时解析、拦截 completeTask 改置 waiting_input，用户选完
// 选项后走 Resume 续跑。详见 plan「多选一交互（路径 B）实现方案」。
//
// 格式约定（由 ChoicePromptSection 注入 system prompt）：
//
//	[CHOICE]
//	Q: <问题描述>
//	A: <选项1>
//	A: <选项2>
//	[/CHOICE]
//
// 解析只认 assistant 的 text block 末尾，thinking 里的同格式被忽略。
// 为兼容非 Claude 后端（如 deepseek）对格式的常见偏离，解析分两级：
//   - 严格模式：[CHOICE]...[/CHOICE] 闭合块在文本末尾，零误判。
//   - 容错模式：严格未命中时，若末尾有独立 [CHOICE] 行（无闭合标签），
//     用宽松规则识别 Q/问题行与 A/数字编号选项行。靠「选项行占比」防误判。

// ChoicePromptSection 追加到 system prompt 的方案拍板协议文案。
// 由 main.go 读 CLAUDE.md 后拼接，经 --append-system-prompt 注入 claude。
//
// 设计取舍：不要求模型输出结构化标记（曾用 [CHOICE]...[/CHOICE] 块 + 后端解析拦截
// completeTask 改 waiting_input，但非 Claude 后端如 deepseek 对格式遵从不稳定、
// 且会自创「决策点」「[决策 1]」等变体，无法穷举）。改为纯文本约束：模型在需要拍板时
// 列出所有方案 + ★ 标记推荐项，任务正常完成，用户看完方案文本直接续问回复选择。
// 零格式识别，模型怎么偏离都不影响方案呈现。解析函数 parseChoiceBlock 等保留但未启用
// （maybePauseForChoice 直接返回 false），将来换真 Claude 后端可复活路径 B 弹窗。
const ChoicePromptSection = `

## 方案拍板协议

遇到需要用户拍板的设计决策（如选哪种算法、用哪种方案、走哪条技术路线），不要自行替用户决定。按以下方式处理：

1. 先简要说明这个决策点是什么、为什么需要用户定（1-2 句）。
2. 列出所有可行方案，每个方案独立成行，简述其取舍（适用场景 / 代价）。
3. 在你推荐的方案前加 ★ 标记，并附一句推荐理由。

格式示例：

这个决策是关于 token 签名算法，它决定了密钥管理与验签流程：

- ★ HS256（HMAC-SHA256，对称）— 实现最简单、最快，适合单一服务自签自验。推荐：Bridge 自身签发自验，无需多方验签，对称密钥足够。
- RS256（RSA-SHA256，非对称）— 多方可独立验签，公钥可 JWKS 分发。代价：密钥管理复杂、签名较大。
- ES256（ECDSA，非对称）— 同 RS256 但更短更快。代价：ECC 体系门槛。

规则：
- 只有真正需要用户拍板的决策才问；能合理默认的小事直接定。
- 一次最多聚焦一个决策点；若多个决策耦合，先问最关键的那个。
- 必须给出 ≥2 个方案；每个决策点必须且只能在一个方案前加 ★ 标记推荐，附一句推荐理由。★ 是必须的，不是可选的。
- 列完方案后停下，等用户回复选择（用户会直接说「选 X」或方案名）。
- 不要用任何特殊标签或代码块包裹方案列表，就用普通 markdown 列表。
`

// choiceBlockRe 匹配文本末尾的 [CHOICE]...[/CHOICE] 或 <CHOICE>...</CHOICE> 块（严格模式）。
// (?s) 让 . 跨行；\z 锚定整个文本结尾，确保只在末尾命中（避免正文中间的伪 CHOICE 误判）。
// 同时兼容方括号 [CHOICE] 与尖括号 <CHOICE>（deepseek 偶发用尖括号变体）。
var choiceBlockRe = regexp.MustCompile(`(?s)[[<]CHOICE[]>][ \t]*\n(.*?)\n[[<]/CHOICE[]>][\s]*\z`)

// choiceStartRe 匹配 [CHOICE] 或 <CHOICE> 标记（容错模式定位起点）。
// 兼容三种用法：独立行、行内、尖括号变体（deepseek 偶发用 <CHOICE> 代替 [CHOICE]）。
// 取最后一个匹配位置作为 body 起点（末尾的才是真提问，前文解释性标记不干扰）。
var choiceStartRe = regexp.MustCompile(`[[<]CHOICE[]>]`)

// optionRe 匹配一行选项，捕获选项正文。兼容多种编号风格：
//   - A: / A. / A) / A、  （字母编号，大小写均可）
//   - 1. / 1) / 1、        （数字编号）
//   - - A. / * 1) 等        （前导 - 或 * 列表标记）
// 命中后 group 1 = 选项正文。
var optionRe = regexp.MustCompile(`^\s*(?:[-*][ \t]+)?(?:[A-Za-z]|\d+)[.):、][ \t]+(.+?)\s*$`)

// questionRe 匹配一行问题，捕获问题正文。兼容：
//   - Q: / Q：  / Q.
//   - 问题: / 问题： / 问题：
//   - question: / Question:（英文小写/大写，deepseek 偶发用英文标签）
var questionRe = regexp.MustCompile(`^\s*(?:Q|问题|question|Question)[.：:][ \t]*(.+?)\s*$`)

// parsedChoice 解析后的提问块。
type parsedChoice struct {
	Question string   // Q: 行内容（多行 Q 用 \n 连接）
	Options  []string // A: 行内容
}

// parseChoiceBlock 从 text 末尾解析 [CHOICE] 块。
// 返回 (choice, remainder)：choice 为 nil 表示未命中（调用方应正常 completeTask）。
// remainder 是剥离 CHOICE 块后的前导正文（正常回答），供调用方改写最后一个 text event，
// 避免前端把 [CHOICE] 格式串当回答渲染。
//
// 解析分两级：严格模式优先（闭合块，零误判），未命中再走容错模式（无闭合标签的变体）。
func parseChoiceBlock(text string) (*parsedChoice, string) {
	if pc, rem := parseStrictChoice(text); pc != nil {
		return pc, rem
	}
	return parseLenientChoice(text)
}

// parseStrictChoice 严格模式：匹配末尾闭合的 [CHOICE]...[/CHOICE] 块。
func parseStrictChoice(text string) (*parsedChoice, string) {
	m := choiceBlockRe.FindStringSubmatch(text)
	if m == nil {
		return nil, ""
	}
	pc := parseBody(m[1])
	if pc == nil || pc.Question == "" {
		return nil, ""
	}
	remainder := strings.TrimSuffix(text, m[0])
	remainder = strings.TrimRight(remainder, "\n\r")
	return pc, remainder
}

// parseLenientChoice 容错模式：文本含 [CHOICE] 标记但缺 [/CHOICE] 闭合时，
// 从最后一个 [CHOICE] 之后到文本结尾当 body 解析。用宽松规则识别问题/选项，
// 靠「选项行占比 ≥ 2」判定有效，防止把普通带列点的正文误判成提问。
//
// 问题来源优先级：① Q:/问题: 显式问题行；② [CHOICE] 同行剩余文本（行内用法如
// "**[CHOICE] 决策1：要清理哪个目录？**"）；③ 兜底空（仍需选项占比通过才命中）。
// 行内噪声（如 "选项</｜DSML｜parameter>"）由 stripInlineNoise 清理。
func parseLenientChoice(text string) (*parsedChoice, string) {
	matches := choiceStartRe.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return nil, ""
	}
	last := matches[len(matches)-1]
	// [CHOICE] 同行剩余（到下一个换行前）
	afterTag := text[last[1]:]
	nlIdx := strings.IndexByte(afterTag, '\n')
	var inlineRest string
	var tail string
	if nlIdx >= 0 {
		inlineRest = afterTag[:nlIdx]
		tail = afterTag[nlIdx+1:]
	} else {
		inlineRest = afterTag
		tail = ""
	}
	inlineRest = strings.TrimSpace(stripInlineNoise(inlineRest))
	// 去掉行内用法常见的首尾 ** 加粗标记
	inlineRest = strings.Trim(inlineRest, "*")

	tail = strings.TrimLeft(tail, "\r\n")
	body := tail
	if inlineRest != "" {
		body = inlineRest + "\n" + tail
	}

	pc := parseBody(body)
	if pc == nil {
		return nil, ""
	}
	// 问题兜底：显式问题行没匹配到时，用 [CHOICE] 同行剩余
	if pc.Question == "" && inlineRest != "" {
		pc.Question = inlineRest
	}
	if pc.Question == "" {
		return nil, ""
	}
	if !lenientLooksValid(tail, len(pc.Options)) {
		return nil, ""
	}
	remainder := text[:last[0]]
	remainder = strings.TrimRight(remainder, "\n\r")
	return pc, remainder
}

// parseBody 解析 CHOICE 块体（不含 [CHOICE]/[/CHOICE] 标签），识别问题与选项。
// 严格模式与容错模式共用。只校验选项 ≥ 2；问题可为空（容错模式用 [CHOICE] 同行
// 剩余兜底，严格模式自行判空）。
func parseBody(body string) *parsedChoice {
	var question strings.Builder
	var opts []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[/CHOICE]") {
			continue
		}
		// 先试问题行
		if qm := questionRe.FindStringSubmatch(line); qm != nil {
			q := stripInlineNoise(qm[1])
			q = strings.TrimSpace(q)
			if q != "" {
				if question.Len() > 0 {
					question.WriteString("\n")
				}
				question.WriteString(q)
			}
			continue
		}
		// 再试选项行
		if om := optionRe.FindStringSubmatch(line); om != nil {
			opt := stripInlineNoise(om[1])
			opt = strings.TrimSpace(opt)
			if opt != "" {
				opts = append(opts, opt)
			}
			continue
		}
	}
	if len(opts) < 2 {
		return nil
	}
	return &parsedChoice{Question: question.String(), Options: opts}
}

// lenientLooksValid 容错模式防误判：body 的有效行里，选项行应占多数。
// 避免模型普通带编号列点的正文（如「1. 方案一 2. 方案二」+ 大段说明）被误判为提问。
// 规则：选项数 ≥ 2 且选项行数 >= 有效行总数的一半。
// 有效行 = 非空且非纯噪声（整行伪标签）的行。
func lenientLooksValid(body string, optCount int) bool {
	effective := 0
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || isNoiseLine(line) {
			continue
		}
		effective++
	}
	if effective == 0 {
		return false
	}
	return optCount*2 >= effective
}

// isNoiseLine 判断一行是否为纯噪声/非内容行，应从占比计算中排除。
// 包括：① 伪标签残留（以 < 开头或含全角 ｜）；② markdown 代码块围栏（``` 或 ~~~）。
// 注意：「有效内容 + 行尾噪声」（如 "A: xxx</｜DSML｜...>"）不算噪声行，
// 由 stripInlineNoise 在解析时清理行内部分。
func isNoiseLine(line string) bool {
	if strings.HasPrefix(line, "<") {
		return true
	}
	if strings.Contains(line, "｜") {
		stripped := strings.TrimSpace(stripInlineNoise(line))
		return stripped == ""
	}
	// markdown 代码块围栏：仅 ``` 或 ~~~（可带语言名如 ```go，但纯围栏只含反引号/波浪号）
	if strings.Trim(line, "`~") == "" && len(line) >= 3 {
		return true
	}
	return false
}

// stripInlineNoise 去掉选项/问题正文里粘附的行内伪标签噪声。
// 如 "多个目录配置化</｜DSML｜parameter>" -> "多个目录配置化"。
// 在第一个 < 或全角 ｜ 处截断（这些字符不会出现在正常选项短语里）。
func stripInlineNoise(s string) string {
	for _, ch := range []string{"<", "｜"} {
		if i := strings.Index(s, ch); i >= 0 {
			s = s[:i]
		}
	}
	return s
}
