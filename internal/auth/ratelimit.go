package auth

import (
	"sync"
	"time"
)

// IPLimiter implements per-IP token-brute-force protection (PRD §7.2):
//   - Up to maxFailures failed token validations per IP per 1-minute
//     sliding window are tolerated.
//   - Once maxFailures is hit within that window, the IP is blacklisted
//     for blacklistDuration; during blacklist, Allow() returns false.
//   - The failure window is 1 minute (sliding), independent of the
//     blacklist duration.
//
// Blacklisting is LAZY: NoteFailure only records a timestamp; Allow is
// where the threshold check happens. This is necessary so that an IP
// whose failures all happened at T=0 is NOT blacklisted once 1 minute
// has passed (the sliding window has moved past those failures).
//
// All operations are goroutine-safe.
type IPLimiter struct {
	mu                sync.Mutex
	maxFailures       int
	blacklistDuration time.Duration
	now               func() time.Time
	failures          map[string][]time.Time // ip → recent failure timestamps
	blacklisted       map[string]time.Time   // ip → blacklist-until
}

// NewIPLimiter creates a limiter with the real wall clock.
func NewIPLimiter(maxFailures int, blacklistDuration time.Duration) *IPLimiter {
	return &IPLimiter{
		maxFailures:       maxFailures,
		blacklistDuration: blacklistDuration,
		now:               time.Now,
		failures:          make(map[string][]time.Time),
		blacklisted:      make(map[string]time.Time),
	}
}

// NewIPLimiterWithNow injects a custom clock for tests.
func NewIPLimiterWithNow(maxFailures int, blacklistDuration time.Duration, now func() time.Time) *IPLimiter {
	l := NewIPLimiter(maxFailures, blacklistDuration)
	l.now = now
	return l
}

// Allow reports whether ip is currently permitted to attempt a token
// validation. Returns false if the IP is blacklisted OR if it has
// accumulated >= maxFailures failures in the last 1-minute sliding
// window (in which case the IP is blacklisted as a side effect, so
// subsequent calls fast-fail). Does NOT itself count as a failure —
// call NoteFailure for that.
func (l *IPLimiter) Allow(ip string) bool {
	if ip == "" {
		return true // missing IP: don't block (auth will reject anyway)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	// 1. Check existing blacklist.
	if until, ok := l.blacklisted[ip]; ok {
		if now.Before(until) {
			return false
		}
		delete(l.blacklisted, ip) // expired — clean up
	}
	// 2. Check sliding 1-minute failure window. If threshold met,
	//    blacklist now and deny.
	cutoff := now.Add(-time.Minute)
	recent := l.failures[ip][:0]
	for _, t := range l.failures[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	l.failures[ip] = recent
	if len(recent) >= l.maxFailures {
		l.blacklisted[ip] = now.Add(l.blacklistDuration)
		// Clear the failure log so a post-blacklist re-try starts fresh.
		delete(l.failures, ip)
		return false
	}
	return true
}

// NoteFailure records a failed token attempt for ip. Does NOT eagerly
// blacklist — the threshold check happens lazily in Allow so the sliding
// window semantics work correctly.
func (l *IPLimiter) NoteFailure(ip string) {
	if ip == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[ip] = append(l.failures[ip], l.now())
}
