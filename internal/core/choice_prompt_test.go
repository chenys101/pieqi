package core

import (
	"strings"
	"testing"
)

func TestParseChoiceBlock_Standard(t *testing.T) {
	text := `[CHOICE]
Q: 用哪种签名算法？
A: HS256
A: RS256
[/CHOICE]`
	pc, rem := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("should match standard block")
	}
	if pc.Question != "用哪种签名算法？" {
		t.Fatalf("question=%q", pc.Question)
	}
	if len(pc.Options) != 2 || pc.Options[0] != "HS256" || pc.Options[1] != "RS256" {
		t.Fatalf("options=%v", pc.Options)
	}
	if rem != "" {
		t.Fatalf("remainder=%q, want empty", rem)
	}
}

func TestParseChoiceBlock_WithLeadingText(t *testing.T) {
	text := `我分析了需求，需要你决定签名算法。

[CHOICE]
Q: 用哪种签名算法？
A: HS256
A: RS256
[/CHOICE]`
	pc, rem := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("should match")
	}
	if !strings.Contains(rem, "我分析了需求") {
		t.Fatalf("remainder=%q missing leading text", rem)
	}
	if strings.Contains(rem, "[CHOICE]") {
		t.Fatalf("remainder=%q should not contain CHOICE block", rem)
	}
}

func TestParseChoiceBlock_TooFewOptions(t *testing.T) {
	text := `[CHOICE]
Q: 只有一个选项？
A: only
[/CHOICE]`
	pc, _ := parseChoiceBlock(text)
	if pc != nil {
		t.Fatal("should not match with <2 options")
	}
}

func TestParseChoiceBlock_MissingCloseTag(t *testing.T) {
	// 容错模式：缺 [/CHOICE] 闭合，但有独立 [CHOICE] 行 + Q/A 行，应被救回。
	// （非 Claude 后端偶发漏闭合标签，路径 B 靠容错兜底。）
	text := `[CHOICE]
Q: 没有结束标记
A: HS256
A: RS256`
	pc, _ := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("lenient mode should rescue missing close tag")
	}
	if pc.Question != "没有结束标记" {
		t.Fatalf("question=%q", pc.Question)
	}
	if len(pc.Options) != 2 || pc.Options[0] != "HS256" || pc.Options[1] != "RS256" {
		t.Fatalf("options=%v", pc.Options)
	}
}

func TestParseChoiceBlock_NotAtEnd(t *testing.T) {
	// 严格模式：闭合块后还有正文 -> \z 不命中。
	// 容错模式：[/CHOICE] 行 + 尾文使选项占比不足（2/5 < 半数）-> 也不命中。
	// 即闭合块 + 大段尾文仍降级，避免误伤正文引用格式的场景。
	text := `[CHOICE]
Q: 在中间？
A: A
A: B
[/CHOICE]
后面还有文字`
	pc, _ := parseChoiceBlock(text)
	if pc != nil {
		t.Fatal("should not match when CHOICE block followed by trailing text")
	}
}

func TestParseChoiceBlock_MultipleQLines(t *testing.T) {
	text := `[CHOICE]
Q: 第一行
Q: 第二行
A: opt1
A: opt2
[/CHOICE]`
	pc, _ := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("should match")
	}
	if pc.Question != "第一行\n第二行" {
		t.Fatalf("question=%q, want two lines joined", pc.Question)
	}
}

func TestParseChoiceBlock_EmptyText(t *testing.T) {
	pc, _ := parseChoiceBlock("")
	if pc != nil {
		t.Fatal("empty text should not match")
	}
}

func TestParseChoiceBlock_MoreThanTwoOptions(t *testing.T) {
	text := `[CHOICE]
Q: 选哪个？
A: 红
A: 绿
A: 蓝
[/CHOICE]`
	pc, _ := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("should match")
	}
	if len(pc.Options) != 3 {
		t.Fatalf("options count=%d, want 3", len(pc.Options))
	}
}

func TestChoicePromptSection_NonEmpty(t *testing.T) {
	if strings.TrimSpace(ChoicePromptSection) == "" {
		t.Fatal("ChoicePromptSection should not be empty")
	}
	// 当前是纯文本方案拍板协议（不再用 [CHOICE] 块）
	if !strings.Contains(ChoicePromptSection, "方案拍板") {
		t.Fatal("ChoicePromptSection should describe 方案拍板 protocol")
	}
	if !strings.Contains(ChoicePromptSection, "★") {
		t.Fatal("ChoicePromptSection should mention ★ recommendation marker")
	}
}

// ===== 容错模式：实测失败样本回放 =====

// 样本1：DSML 伪工具调用残留顶掉闭合标签（deepseek 偶发）。
func TestParseChoiceBlock_Lenient_DSMLNoise(t *testing.T) {
	text := "明白了，我会按格式问。\n\n[CHOICE]\nQ: 脚本清理哪个目录？\nA: 当前目录\nA: 指定目录\nA: 多个目录配置化</｜DSML｜parameter>\n</｜DSML｜invoke>\n</｜DSML｜tool_calls>"
	pc, rem := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("lenient should rescue DSML noise + missing close")
	}
	if pc.Question != "脚本清理哪个目录？" {
		t.Fatalf("question=%q", pc.Question)
	}
	if len(pc.Options) != 3 {
		t.Fatalf("options=%v want 3", pc.Options)
	}
	if !strings.Contains(rem, "明白了") {
		t.Fatalf("remainder=%q missing leading text", rem)
	}
	if strings.Contains(rem, "[CHOICE]") {
		t.Fatalf("remainder should not contain [CHOICE]")
	}
}

// 样本2：行内 [CHOICE]（同行接说明）+ A./B./C. 字母编号 + 前导 - 列表标记。
// 行内 [CHOICE] 是 deepseek 高频变体，容错模式应救回：[CHOICE] 同行剩余作问题兜底，
// - A. xxx 行作选项。
func TestParseChoiceBlock_Lenient_InlineChoice(t *testing.T) {
	text := "**[CHOICE] 决策1：要清理哪个目录？**\n- A. 固定路径\n- B. 参数传入\n- C. 交互选择\n请回复字母。"
	pc, rem := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("lenient should rescue inline [CHOICE]")
	}
	if !strings.Contains(pc.Question, "清理哪个目录") {
		t.Fatalf("question=%q", pc.Question)
	}
	if len(pc.Options) != 3 {
		t.Fatalf("options=%v want 3", pc.Options)
	}
	if pc.Options[0] != "固定路径" {
		t.Fatalf("opt[0]=%q", pc.Options[0])
	}
	_ = rem
}

// 样本3：中文「问题：/选项：」+ 数字编号 1./2./3.。
func TestParseChoiceBlock_Lenient_ChineseLabels(t *testing.T) {
	text := "[CHOICE]\n问题：这个清理脚本要扫描哪个目录？\n选项：\n1. 当前工作目录\n2. 指定固定绝对路径\n3. 运行时参数传入\n请选择（回复编号即可）。"
	pc, rem := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("lenient should rescue chinese labels + numeric options")
	}
	if !strings.Contains(pc.Question, "扫描哪个目录") {
		t.Fatalf("question=%q", pc.Question)
	}
	if len(pc.Options) != 3 {
		t.Fatalf("options=%v want 3", pc.Options)
	}
	if pc.Options[0] != "当前工作目录" {
		t.Fatalf("opt[0]=%q", pc.Options[0])
	}
	_ = rem
}

// 容错防误判：普通带编号列点的正文（无 [CHOICE] 标记）不应被误判为提问。
func TestParseChoiceBlock_Lenient_NoChoiceMarker(t *testing.T) {
	text := "给你三个方案：\n1. 文档驱动，先读 CONTEXT.md\n2. 代码驱动，追一条链路\n3. 实战驱动，从问题切入\n你看哪个合适？"
	pc, _ := parseChoiceBlock(text)
	if pc != nil {
		t.Fatal("plain numbered list without [CHOICE] must not match")
	}
}

// 容错防误判：有 [CHOICE] 行但选项行不占多数（大段说明 + 少量编号行）不应命中。
func TestParseChoiceBlock_Lenient_OptionsNotMajority(t *testing.T) {
	text := "[CHOICE]\n这只是一个介绍段落，讲了很多背景和上下文。\n再补充一段说明文字。\n还有更多解释。\nA: 选项一\nA: 选项二"
	pc, _ := parseChoiceBlock(text)
	if pc != nil {
		t.Fatalf("options not majority should not match, got %+v", pc)
	}
}

// 容错：字母编号 A)/B)/C) + 前导 - 列表标记。
func TestParseChoiceBlock_Lenient_ParenLetterOptions(t *testing.T) {
	text := "[CHOICE]\nQ: 用哪个目录？\n- A) 临时目录\n- B) 下载目录\n- C) 项目目录"
	pc, _ := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("lenient should match A)/B)/C) options")
	}
	if len(pc.Options) != 3 {
		t.Fatalf("options=%v want 3", pc.Options)
	}
	if pc.Options[0] != "临时目录" {
		t.Fatalf("opt[0]=%q", pc.Options[0])
	}
}

// 容错：英文标签 question:/options: + 数字编号（deepseek 偶发用英文标签）。
func TestParseChoiceBlock_Lenient_EnglishLabels(t *testing.T) {
	text := "[CHOICE]\nquestion: 脚本应该清理哪个目录？\noptions:\n  1. 固定绝对路径\n  2. 当前工作目录\n  3. 运行时参数传入\n[/CHOICE]"
	pc, _ := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("lenient should match english question:/options: labels")
	}
	if !strings.Contains(pc.Question, "清理哪个目录") {
		t.Fatalf("question=%q", pc.Question)
	}
	if len(pc.Options) != 3 {
		t.Fatalf("options=%v want 3", pc.Options)
	}
	if pc.Options[0] != "固定绝对路径" {
		t.Fatalf("opt[0]=%q", pc.Options[0])
	}
}

// 容错：[CHOICE] 块在 markdown 代码块围栏内（```...```），应忽略围栏行。
func TestParseChoiceBlock_Lenient_CodeFence(t *testing.T) {
	text := "先确认格式：\n```\n[CHOICE]\n问题: 清理哪个目录？\n选项:\n  A. 日志目录\n  B. 临时目录\n  C. 构建产物\n```\n请选择。"
	pc, _ := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("lenient should match CHOICE inside code fence")
	}
	if !strings.Contains(pc.Question, "清理哪个目录") {
		t.Fatalf("question=%q", pc.Question)
	}
	if len(pc.Options) != 3 {
		t.Fatalf("options=%v want 3", pc.Options)
	}
}

// 容错：尖括号变体 <CHOICE>（deepseek 偶发用尖括号代替方括号）。
func TestParseChoiceBlock_Lenient_AngleBracket(t *testing.T) {
	text := "<CHOICE>\nQ: 用哪种算法？\nA: HS256\nA: RS256\n</CHOICE>"
	pc, _ := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("lenient should match <CHOICE> angle bracket variant")
	}
	if pc.Question != "用哪种算法？" {
		t.Fatalf("question=%q", pc.Question)
	}
	if len(pc.Options) != 2 {
		t.Fatalf("options=%v want 2", pc.Options)
	}
}

// 容错：Q 行在选项之后出现也应能拼上问题（部分模型先列选项后给问题）。
func TestParseChoiceBlock_Lenient_QAfterOptions(t *testing.T) {
	text := "[CHOICE]\nA: HS256\nA: RS256\nQ: 用哪种算法？"
	pc, _ := parseChoiceBlock(text)
	if pc == nil {
		t.Fatal("lenient should match Q after options")
	}
	if pc.Question != "用哪种算法？" {
		t.Fatalf("question=%q", pc.Question)
	}
}
