package core

import (
	"strings"
	"testing"

	"pieqi/internal/agent"
	"pieqi/internal/model"

	"go.uber.org/zap"
)

type fakePlanModeAdapter struct {
	onPlan agent.PlanUpdateFunc
	onMode agent.ModeUpdateFunc
	modes  agent.ModeState
}

func (f *fakePlanModeAdapter) OnPlanUpdate(fn agent.PlanUpdateFunc) { f.onPlan = fn }
func (f *fakePlanModeAdapter) OnModeUpdate(fn agent.ModeUpdateFunc) { f.onMode = fn }
func (f *fakePlanModeAdapter) Modes() agent.ModeState               { return f.modes }

func TestWirePlan_PersistsTaskPlanAndEvent(t *testing.T) {
	bus := NewEventBus()
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := store.Create(&model.Task{ProjectID: "p", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	fa := &fakePlanModeAdapter{}
	h := WirePlan(fa, bus, store, tt.ID, zap.NewNop())
	if h == nil {
		t.Fatal("WirePlan returned nil")
	}
	defer h.Unwire()

	fa.onPlan(agent.PlanUpdate{
		SessionID: "s",
		Entries: []agent.PlanEntry{
			{Content: "A", Priority: "high", Status: "pending"},
			{Content: "B", Priority: "low", Status: "completed"},
		},
	})

	got, ok := store.Get(tt.ID)
	if !ok || got.Plan == nil || len(got.Plan.Entries) != 2 {
		t.Fatalf("Task.Plan=%v", got.Plan)
	}
	var planEv *model.TaskEvent
	for i := range got.Events {
		if got.Events[i].Type == model.EventPlan {
			planEv = &got.Events[i]
		}
	}
	if planEv == nil {
		t.Fatal("missing EventPlan")
	}
	if planEv.Text != "计划 2 项 · 1 完成" {
		t.Fatalf("Text=%q", planEv.Text)
	}
}

func TestWireMode_UpdatesTaskAndEvent(t *testing.T) {
	bus := NewEventBus()
	store, err := NewTaskStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tt, err := store.Create(&model.Task{
		ProjectID: "p",
		Prompt:    "hi",
		Mode:      "plan",
		Modes:     []model.ModeOption{{ID: "plan", Name: "Plan"}, {ID: "agent", Name: "Agent"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fa := &fakePlanModeAdapter{}
	h := WireMode(fa, bus, store, tt.ID, zap.NewNop())
	if h == nil {
		t.Fatal("WireMode returned nil")
	}
	defer h.Unwire()

	fa.onMode(agent.ModeUpdate{SessionID: "s", ModeID: "agent"})

	got, ok := store.Get(tt.ID)
	if !ok || got.Mode != "agent" {
		t.Fatalf("Mode=%q", got.Mode)
	}
	n := 0
	for _, ev := range got.Events {
		if ev.Type == model.EventModeSwitch {
			n++
			if ev.Text != "模式：Plan → Agent" {
				t.Fatalf("Text=%q", ev.Text)
			}
		}
	}
	if n != 1 {
		t.Fatalf("mode_switch events=%d", n)
	}
}

func TestBuildPermSummary_ContentTextPreferred(t *testing.T) {
	got := buildPermSummary(agent.PermissionRequest{
		ToolTitle:   "Ready for implementation",
		ToolKind:    "switch_mode",
		ContentText: "## Implementation Plan\n1. edit foo",
	})
	if !strings.Contains(got, "Ready for implementation") {
		t.Fatalf("missing title: %q", got)
	}
	if !strings.Contains(got, "Implementation Plan") {
		t.Fatalf("missing plan body: %q", got)
	}
}
