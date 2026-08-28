package auth

import (
	"testing"
	"time"
)

func TestIPLimiter_AllowsUntilThreshold(t *testing.T) {
	l := NewIPLimiter(5, 10*time.Minute)
	ip := "1.2.3.4"
	for i := 0; i < 5; i++ {
		if !l.Allow(ip) {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		l.NoteFailure(ip)
	}
	// 6th failure within the same minute → blacklisted
	if l.Allow(ip) {
		t.Fatal("6th attempt within window should be blocked")
	}
}

func TestIPLimiter_BlacklistExpires(t *testing.T) {
	now := time.Now()
	l := NewIPLimiterWithNow(5, 10*time.Minute, func() time.Time { return now })
	ip := "9.9.9.9"
	for i := 0; i < 5; i++ {
		l.NoteFailure(ip)
	}
	if l.Allow(ip) {
		t.Fatal("should be blacklisted after 5 failures")
	}
	// Advance past blacklist duration
	now = now.Add(11 * time.Minute)
	if !l.Allow(ip) {
		t.Fatal("blacklist should expire after 10m")
	}
}

func TestIPLimiter_WindowSlidesAfterOneMinute(t *testing.T) {
	now := time.Now()
	l := NewIPLimiterWithNow(5, 10*time.Minute, func() time.Time { return now })
	ip := "8.8.8.8"
	for i := 0; i < 5; i++ {
		l.NoteFailure(ip)
	}
	// Advance past the failure-counting window (1 minute) but BEFORE the
	// blacklist kicks in: NoteFailure should reset since the previous 5
	// were >1m ago, but Allow should now pass because we didn't exceed
	// threshold in a fresh window.
	now = now.Add(61 * time.Second)
	if !l.Allow(ip) {
		t.Fatal("after 1m+ with no new failures, IP should be allowed again")
	}
}

func TestIPLimiter_DifferentIPsAreIndependent(t *testing.T) {
	l := NewIPLimiter(5, 10*time.Minute)
	for i := 0; i < 5; i++ {
		l.NoteFailure("1.1.1.1")
	}
	if l.Allow("1.1.1.1") {
		t.Fatal("1.1.1.1 should be blocked")
	}
	if !l.Allow("2.2.2.2") {
		t.Fatal("2.2.2.2 should be unaffected")
	}
}

func TestIPLimiter_SuccessDoesNotCount(t *testing.T) {
	l := NewIPLimiter(5, 10*time.Minute)
	// Successful validates (no NoteFailure) should never lead to block.
	for i := 0; i < 100; i++ {
		if !l.Allow("7.7.7.7") {
			t.Fatal("Allow without NoteFailure should always return true")
		}
	}
}
