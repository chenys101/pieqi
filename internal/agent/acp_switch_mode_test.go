package agent

import (
	"encoding/json"
	"testing"
)

func TestEnrichSwitchModePresentation_FromRaw(t *testing.T) {
	title, raw := enrichSwitchModePresentation(
		"Switch Mode: unknown",
		"switch_mode",
		json.RawMessage(`{"modeId":"agent"}`),
		"",
	)
	if title != "Switch Mode: agent" {
		t.Fatalf("title=%q", title)
	}
	if switchModeIDFromRaw(raw) != "agent" {
		t.Fatalf("raw=%s", raw)
	}
}

func TestEnrichSwitchModePresentation_EmptyShellUnchanged(t *testing.T) {
	inTitle := "Switch Mode: unknown"
	inRaw := json.RawMessage(`{}`)
	title, raw := enrichSwitchModePresentation(inTitle, "switch_mode", inRaw, "")
	if title != inTitle {
		t.Fatalf("empty shell must not invent title, got %q", title)
	}
	if string(raw) != "{}" && string(raw) != "null" && len(raw) != 0 {
		// {} 保持空壳
		if !rawInputEffectivelyEmpty(raw) {
			t.Fatalf("empty shell must stay empty, raw=%s", raw)
		}
	}
}

func TestEnrichSwitchModePresentation_FromContent(t *testing.T) {
	title, raw := enrichSwitchModePresentation(
		"Switch Mode: unknown",
		"switch_mode",
		json.RawMessage(`{}`),
		"plan",
	)
	if title != "Switch Mode: plan" {
		t.Fatalf("title=%q", title)
	}
	if switchModeIDFromRaw(raw) != "plan" {
		t.Fatalf("raw=%s", raw)
	}
}

func TestEnrichSwitchModePresentation_NonSwitchUntouched(t *testing.T) {
	title, raw := enrichSwitchModePresentation(
		"Edit File",
		"edit",
		json.RawMessage(`{"path":"a.go"}`),
		"plan",
	)
	if title != "Edit File" {
		t.Fatalf("title=%q", title)
	}
	if string(raw) != `{"path":"a.go"}` {
		t.Fatalf("raw=%s", raw)
	}
}
