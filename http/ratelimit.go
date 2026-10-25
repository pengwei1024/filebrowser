package fbhttp

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// loginRateLimitWindow is the rolling window over which failed login attempts
// are counted. Only the per-window thresholds are configurable; the window is
// fixed so the throttle stays predictable.
const loginRateLimitWindow = time.Minute

// Conservative defaults, used when the corresponding Server setting is unset
// (<= 0). The per-IP budget is looser than the per-username one so that several
// users behind a single NAT (or a reverse proxy that does not forward the real
// client IP) are not locked out by one another's failures.
const (
	defaultLoginMaxPerUser = 5
	defaultLoginMaxPerIP   = 50
)

// failCounter tracks failed attempts for one key within the current window.
type failCounter struct {
	count       int
	windowStart time.Time
}

// rateLimiter throttles repeated failed login attempts, keyed independently by
// username and by client IP. It is purely in-memory: counters reset when the
// process restarts and are not shared across instances. Its job is to blunt
// online password guessing and to cap the number of expensive bcrypt
// comparisons an unauthenticated client can force, not to be an authoritative
// quota — deployments that need distributed or persistent limiting should rely
// on the fronting reverse proxy.
type rateLimiter struct {
	mu         sync.Mutex
	counters   map[string]*failCounter
	maxPerUser int
	maxPerIP   int
	window     time.Duration
	lastSweep  time.Time
}

func newRateLimiter(maxPerUser, maxPerIP int) *rateLimiter {
	if maxPerUser <= 0 {
		maxPerUser = defaultLoginMaxPerUser
	}
	if maxPerIP <= 0 {
		maxPerIP = defaultLoginMaxPerIP
	}
	return &rateLimiter{
		counters:   map[string]*failCounter{},
		maxPerUser: maxPerUser,
		maxPerIP:   maxPerIP,
		window:     loginRateLimitWindow,
	}
}

// userKey normalizes the username so an attacker cannot multiply their budget
// by varying only the letter case.
func userKey(username string) string { return "u:" + strings.ToLower(username) }

func ipKey(ip string) string { return "i:" + ip }

// allow reports whether a login attempt from ip for username may proceed.
func (l *rateLimiter) allow(username, ip string) bool {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)
	return !l.blocked(userKey(username), l.maxPerUser, now) &&
		!l.blocked(ipKey(ip), l.maxPerIP, now)
}

// attackSignal reports, for a single recorded failure, whether that failure is
// the one that first pushed the username and/or the IP over its limit within
// the current window, along with the counts at that moment.
type attackSignal struct {
	userCrossed bool
	ipCrossed   bool
	userCount   int
	ipCount     int
}

// recordFailure counts a failed attempt against both the username and the IP
// and reports whether either dimension just crossed its limit.
func (l *rateLimiter) recordFailure(username, ip string) attackSignal {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	sig := attackSignal{}
	sig.userCrossed = l.bump(userKey(username), l.maxPerUser, now)
	sig.userCount = l.counters[userKey(username)].count
	sig.ipCrossed = l.bump(ipKey(ip), l.maxPerIP, now)
	sig.ipCount = l.counters[ipKey(ip)].count
	return sig
}

// alertOnCross sends a webhook notice for each dimension that just crossed its
// limit, using the destination configured in the web admin settings. It is a
// no-op when alerting is unconfigured or when nothing crossed. The message is
// built and sent outside recordFailure's lock (notify is itself async).
func (l *rateLimiter) alertOnCross(a *alerter, sig attackSignal, username, ip, token string, group int) {
	if a == nil || !alertConfigured(token, group) {
		return
	}
	if sig.userCrossed {
		a.notify(token, group, fmt.Sprintf(
			"[File Browser] 疑似登录爆破：账号 %q 在 %s 内登录失败 %d 次（阈值 %d），最近一次来自 IP %s；该账号已被临时限流。",
			username, l.window, sig.userCount, l.maxPerUser, ip))
	}
	if sig.ipCrossed {
		a.notify(token, group, fmt.Sprintf(
			"[File Browser] 疑似登录爆破：IP %s 在 %s 内登录失败 %d 次（阈值 %d），涉及最近账号 %q；该来源已被临时限流。",
			ip, l.window, sig.ipCount, l.maxPerIP, username))
	}
}

// recordSuccess clears the counters so a legitimate login is never penalized
// for earlier failures.
func (l *rateLimiter) recordSuccess(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.counters, userKey(username))
	delete(l.counters, ipKey(ip))
}

// blocked reports whether key has reached its limit within the current window.
// Callers must hold l.mu.
func (l *rateLimiter) blocked(key string, max int, now time.Time) bool {
	c := l.counters[key]
	if c == nil || now.Sub(c.windowStart) >= l.window {
		return false
	}
	return c.count >= max
}

// bump records one failure for key, starting a fresh window if the previous one
// has elapsed, and reports whether this failure is the one that first reaches
// max within the current window. Callers must hold l.mu.
func (l *rateLimiter) bump(key string, max int, now time.Time) (crossed bool) {
	c := l.counters[key]
	if c == nil || now.Sub(c.windowStart) >= l.window {
		l.counters[key] = &failCounter{count: 1, windowStart: now}
		return 1 >= max
	}
	c.count++
	return c.count == max
}

// sweep evicts counters whose window has elapsed, bounding memory against a
// flood of unique usernames or IPs. It runs at most once per window. Callers
// must hold l.mu.
func (l *rateLimiter) sweep(now time.Time) {
	if !l.lastSweep.IsZero() && now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now
	for k, c := range l.counters {
		if now.Sub(c.windowStart) >= l.window {
			delete(l.counters, k)
		}
	}
}
