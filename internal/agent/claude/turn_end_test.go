package claude

import "testing"

func TestTurnEndFailed_SuccessBeatsIsError(t *testing.T) {
	// V2.0.1 终验：桥偶发 isError=true + subtype=success → 不得判失败。
	if turnEndFailed(true, "success") {
		t.Fatal("subtype=success must not fail even if isError")
	}
	if turnEndFailed(false, "success") {
		t.Fatal("success should pass")
	}
	if !turnEndFailed(true, "cancelled") {
		t.Fatal("cancelled + isError should fail")
	}
	if !turnEndFailed(false, "error_max_tokens") {
		t.Fatal("non-success subtype should fail")
	}
	if !turnEndFailed(true, "") {
		t.Fatal("isError with empty subtype should fail")
	}
	if turnEndFailed(false, "") {
		t.Fatal("idle empty turn_end should not fail")
	}
}
