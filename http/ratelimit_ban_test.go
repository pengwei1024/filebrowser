package fbhttp

import (
	"testing"
	"time"
)

// TestRateLimiterBansIPAfterThreshold covers the headline behaviour: an address
// that burns its per-window budget is refused outright for the ban duration,
// not merely until the counting window rolls over.
func TestRateLimiterBansIPAfterThreshold(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(100, 5)

	var sig attackSignal
	for i := 0; i < 5; i++ {
		sig = l.recordFailure("alice", "203.0.113.9")
	}

	if !sig.ipCrossed || !sig.ipBanned {
		t.Fatalf("the fifth failure should cross the per-IP limit and ban: %+v", sig)
	}
	if _, banned := l.bannedUntil("203.0.113.9"); !banned {
		t.Fatal("the IP should be banned")
	}
	// The ban applies to the address, so a fresh username does not help.
	if l.allow("bob", "203.0.113.9") {
		t.Fatal("a banned IP must be refused regardless of username")
	}
	if !l.allow("bob", "198.51.100.7") {
		t.Fatal("other addresses must be unaffected")
	}
}

// The ban must outlive the counting window — otherwise an attacker just waits
// one minute and continues.
func TestRateLimiterBanOutlivesWindow(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(100, 2)

	l.recordFailure("alice", "203.0.113.9")
	l.recordFailure("alice", "203.0.113.9")

	// Age the counters past the window; the ban is a separate clock.
	for _, c := range l.counters {
		c.windowStart = time.Now().Add(-2 * loginRateLimitWindow)
	}
	if l.allow("alice", "203.0.113.9") {
		t.Fatal("the ban must still apply after the counting window elapsed")
	}
}

func TestRateLimiterBanExpires(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(100, 1)

	l.recordFailure("alice", "203.0.113.9")
	if l.allow("alice", "203.0.113.9") {
		t.Fatal("the IP should be banned")
	}

	// Backdate the ban so it has elapsed. The counting window (one minute) is
	// far shorter than the ban, so by the time a real ban expires its counters
	// have long since rolled over — age them too.
	l.bans[ipKey("203.0.113.9")] = time.Now().Add(-time.Second)
	for _, c := range l.counters {
		c.windowStart = time.Now().Add(-2 * loginRateLimitWindow)
	}
	if !l.allow("alice", "203.0.113.9") {
		t.Fatal("an expired ban must be lifted")
	}
	if _, banned := l.bannedUntil("203.0.113.9"); banned {
		t.Fatal("an expired ban must not be reported as active")
	}
}

// An unresolvable address is shared by every request whose IP could not be
// determined, so banning it would deny service to all of them.
func TestRateLimiterNeverBansUnknownIP(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(100, 1)

	sig := l.recordFailure("alice", unknownIP)
	if sig.ipBanned {
		t.Fatal("the unknown placeholder must not be banned")
	}
	if _, banned := l.bannedUntil(unknownIP); banned {
		t.Fatal("the unknown placeholder must not be banned")
	}
}

func TestRateLimiterBanDefaultDuration(t *testing.T) {
	t.Parallel()
	l := newRateLimiter(0, 0)

	if defaultLoginMaxPerIP != 5 {
		t.Fatalf("per-IP default = %d, want 5", defaultLoginMaxPerIP)
	}
	if l.maxPerIP != defaultLoginMaxPerIP {
		t.Fatalf("limiter per-IP = %d, want the default %d", l.maxPerIP, defaultLoginMaxPerIP)
	}
	if l.banFor != 30*time.Minute {
		t.Fatalf("ban duration = %s, want 30m", l.banFor)
	}

	for i := 0; i < 5; i++ {
		l.recordFailure("alice", "203.0.113.9")
	}
	until, banned := l.bannedUntil("203.0.113.9")
	if !banned {
		t.Fatal("five failures in the window should ban the IP")
	}
	if remaining := time.Until(until); remaining < 29*time.Minute || remaining > 30*time.Minute {
		t.Fatalf("remaining ban = %s, want ~30m", remaining)
	}
}
