package agent

import "testing"

func TestNormalizePermissionKind(t *testing.T) {
	cases := map[string]string{
		"allow_once":   PermissionOptionAllowOnce,
		"allow-once":   PermissionOptionAllowOnce,
		"AllowOnce":    PermissionOptionAllowOnce,
		"allow_always": PermissionOptionAllowAlways,
		"allow-always": PermissionOptionAllowAlways,
		"reject_once":  PermissionOptionRejectOnce,
		"reject-once":  PermissionOptionRejectOnce,
		"REJECT_ALWAYS": PermissionOptionRejectAlways,
		"":             "",
		"other":        "other",
	}
	for in, want := range cases {
		if got := NormalizePermissionKind(in); got != want {
			t.Fatalf("NormalizePermissionKind(%q)=%q want %q", in, got, want)
		}
	}
}

func TestPickAllowOptionID_HyphenKind(t *testing.T) {
	// Cursor 实测 kind 是下划线；这里模拟日后 hyphen kind。
	opts := []PermissionOption{
		{ID: "allow-once", Kind: "allow-once", Name: "Allow once"},
		{ID: "reject-once", Kind: "reject-once", Name: "Reject"},
	}
	id, ok := PickAllowOptionID(opts)
	if !ok || id != "allow-once" {
		t.Fatalf("got (%q,%v), want allow-once", id, ok)
	}
}

func TestPickAllowOptionID_EmptyKindFuzzyID(t *testing.T) {
	opts := []PermissionOption{
		{ID: "reject-once", Kind: "", Name: "Reject"},
		{ID: "allow-always", Kind: "", Name: "Allow always"},
	}
	id, ok := PickAllowOptionID(opts)
	if !ok || id != "allow-always" {
		t.Fatalf("got (%q,%v), want allow-always via fuzzy id", id, ok)
	}
}

func TestPickAllowOptionID_EmptyKindNoAllowCancels(t *testing.T) {
	opts := []PermissionOption{
		{ID: "reject-once", Kind: "", Name: "Reject"},
	}
	if id, ok := PickAllowOptionID(opts); ok {
		t.Fatalf("expected no allow pick, got %q", id)
	}
}

func TestAutoApproveOutcome_PrefersAllowOnce(t *testing.T) {
	opts := []PermissionOption{
		{ID: "allow-once", Kind: "allow_once"},
		{ID: "allow-always", Kind: "allow_always"},
	}
	out := autoApproveOutcome(opts)
	if out.Selected == nil || string(out.Selected.OptionId) != "allow-once" {
		t.Fatalf("outcome=%+v, want selected allow-once", out)
	}
}

func TestAutoApproveOutcome_NoAllowIsCancelled(t *testing.T) {
	opts := []PermissionOption{{ID: "reject-once", Kind: "reject_once"}}
	out := autoApproveOutcome(opts)
	if out.Cancelled == nil {
		t.Fatalf("want Cancelled when no allow option, got %+v", out)
	}
}
