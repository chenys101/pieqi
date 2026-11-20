package agent

import (
	"errors"
	"testing"

	"github.com/coder/acp-go-sdk"
)

func TestStopReasonErr_Cancelled(t *testing.T) {
	err := stopReasonErr(acp.StopReasonCancelled)
	if !errors.Is(err, ErrPromptCancelled) {
		t.Fatalf("got %v, want ErrPromptCancelled", err)
	}
}

func TestStopReasonErr_EndTurn(t *testing.T) {
	if err := stopReasonErr(acp.StopReasonEndTurn); err != nil {
		t.Fatalf("end_turn should be nil, got %v", err)
	}
}
