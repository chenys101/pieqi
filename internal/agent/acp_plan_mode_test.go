package agent

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"pieqi/internal/config"
)

func TestSessionUpdate_PlanEmitsCallback(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "agent", InitTimeout: 5}, nil)
	var got PlanUpdate
	a.OnPlanUpdate(func(u PlanUpdate) { got = u })

	err := a.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "s1",
		Update: acp.SessionUpdate{Plan: &acp.SessionUpdatePlan{
			Entries: []acp.PlanEntry{
				{Content: "读代码", Priority: acp.PlanEntryPriorityHigh, Status: acp.PlanEntryStatusPending},
				{Content: "改文件", Priority: acp.PlanEntryPriorityMedium, Status: acp.PlanEntryStatusInProgress},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "s1" || len(got.Entries) != 2 {
		t.Fatalf("got=%+v", got)
	}
	if got.Entries[1].Status != "in_progress" || got.Entries[0].Content != "读代码" {
		t.Fatalf("entries=%+v", got.Entries)
	}
}

func TestSessionUpdate_CurrentModeUpdate(t *testing.T) {
	a := NewACPAgent(config.ACPConfig{AgentType: "agent", InitTimeout: 5}, nil)
	var got ModeUpdate
	a.OnModeUpdate(func(u ModeUpdate) { got = u })

	err := a.SessionUpdate(context.Background(), acp.SessionNotification{
		SessionId: "s1",
		Update: acp.SessionUpdate{CurrentModeUpdate: &acp.SessionCurrentModeUpdate{
			CurrentModeId: "agent",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ModeID != "agent" {
		t.Fatalf("got=%+v", got)
	}
	if a.Modes().CurrentID != "agent" {
		t.Fatalf("Modes().CurrentID=%q", a.Modes().CurrentID)
	}
}

func TestRememberModesAndApplyMode(t *testing.T) {
	conn := &fakeModelConn{
		newSessionResp: acp.NewSessionResponse{
			SessionId: "sess",
			Modes: &acp.SessionModeState{
				CurrentModeId: "plan",
				AvailableModes: []acp.SessionMode{
					{Id: "plan", Name: "Plan"},
					{Id: "agent", Name: "Agent"},
				},
			},
		},
	}
	a := newModelAgent(t, conn, acp.AgentCapabilities{})
	sid, err := a.NewSession(context.Background(), SessionConfig{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if sid != "sess" {
		t.Fatalf("sid=%s", sid)
	}
	st := a.Modes()
	if st.CurrentID != "plan" || len(st.Available) != 2 {
		t.Fatalf("Modes()=%+v", st)
	}
	if err := a.SetMode(context.Background(), "agent"); err != nil {
		t.Fatal(err)
	}
	if a.Modes().CurrentID != "agent" {
		t.Fatalf("after SetMode CurrentID=%q", a.Modes().CurrentID)
	}
	found := false
	for _, s := range conn.order {
		if s == "set_mode:agent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected set_mode:agent in order=%v", conn.order)
	}
}

func TestToolCallContentText(t *testing.T) {
	got := toolCallContentText([]acp.ToolCallContent{toolTextBlock("## Plan\nstep 1")})
	if got != "## Plan\nstep 1" {
		t.Fatalf("got=%q", got)
	}
	if toolCallContentText(nil) != "" {
		t.Fatal("empty should be empty")
	}
}
