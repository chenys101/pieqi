package core

import (
	"strings"
	"testing"
)

func TestUnifiedDiff_BasicHunk(t *testing.T) {
	before := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n"
	after := "line1\nline2\nlineX\nline4\nline5\nline6\nline7\nline8\nline9\nlineY\n"
	diff, add, del := UnifiedDiff("a.txt", before, after, 3)

	if add != 2 || del != 2 {
		t.Fatalf("counts wrong: +%d -%d", add, del)
	}
	if !strings.Contains(diff, "--- a/a.txt") || !strings.Contains(diff, "+++ b/a.txt") {
		t.Errorf("missing headers: %s", diff)
	}
	// 两个变更点间隔 6 行 = 2×context，git -U3 惯例合并为单 hunk
	if !strings.Contains(diff, "@@ -1,10 +1,10 @@") {
		t.Errorf("unexpected first hunk header: %s", diff)
	}
	if !strings.Contains(diff, "-line3\n+lineX\n") {
		t.Errorf("missing change lines: %s", diff)
	}
	if !strings.Contains(diff, "-line10\n+lineY\n") {
		t.Errorf("missing tail change: %s", diff)
	}
}

func TestUnifiedDiff_Identical(t *testing.T) {
	diff, add, del := UnifiedDiff("a.txt", "same\n", "same\n", 3)
	if diff != "" || add != 0 || del != 0 {
		t.Errorf("identical should produce empty diff, got %q +%d -%d", diff, add, del)
	}
}

func TestUnifiedDiff_EmptyBefore(t *testing.T) {
	diff, add, del := UnifiedDiff("new.txt", "", "a\nb\n", 3)
	if add != 2 || del != 0 {
		t.Fatalf("counts wrong: +%d -%d", add, del)
	}
	if !strings.Contains(diff, "@@ -0,0 +1,2 @@") {
		t.Errorf("hunk header wrong: %s", diff)
	}
	if !strings.Contains(diff, "+a\n+b\n") {
		t.Errorf("content wrong: %s", diff)
	}
}

func TestUnifiedDiff_LargeFallback(t *testing.T) {
	// 规模超限走整段替换兜底：内容是 old 全删 + new 全增
	var a, b strings.Builder
	for i := 0; i < 3000; i++ {
		a.WriteString("aaa\n")
		b.WriteString("bbb\n")
	}
	_, add, del := UnifiedDiff("big.txt", a.String(), b.String(), 3)
	if add != 3000 || del != 3000 {
		t.Fatalf("fallback counts wrong: +%d -%d", add, del)
	}
}

func TestIsBinaryContent(t *testing.T) {
	if IsBinaryContent([]byte("text\nfile\n")) {
		t.Error("plain text misdetected as binary")
	}
	if !IsBinaryContent([]byte("a\x00b")) {
		t.Error("NUL content should be binary")
	}
}

func TestCountLines(t *testing.T) {
	if CountLines([]byte("a\nb\n")) != 2 {
		t.Error("trailing newline count wrong")
	}
	if CountLines([]byte("a\nb")) != 2 {
		t.Error("no trailing newline count wrong")
	}
	if CountLines(nil) != 0 {
		t.Error("empty should be 0")
	}
}
