package agent

import (
	"testing"
)

func TestClassifyShellSegments_CompoundHighlightsDanger(t *testing.T) {
	segs := ClassifyShellSegments(`Remove-Item -Recurse -Force build; npx vitest run`)
	if len(segs) < 2 {
		t.Fatalf("want >=2 segments, got %d %#v", len(segs), segs)
	}
	if segs[0].Kind != "delete" {
		t.Errorf("seg0 kind=%q want delete (text=%q)", segs[0].Kind, segs[0].Text)
	}
	if segs[1].Kind != "read" {
		t.Errorf("seg1 kind=%q want read (text=%q)", segs[1].Kind, segs[1].Text)
	}
}

func TestClassifyShellSegments_SingleReturnsOne(t *testing.T) {
	segs := ClassifyShellSegments(`go test ./internal/...`)
	if len(segs) != 1 {
		t.Fatalf("len=%d want 1", len(segs))
	}
	if segs[0].Kind != "read" {
		t.Errorf("kind=%q want read", segs[0].Kind)
	}
}

func TestClassifyShellSegments_Empty(t *testing.T) {
	if segs := ClassifyShellSegments(""); segs != nil {
		t.Fatalf("want nil, got %#v", segs)
	}
}
