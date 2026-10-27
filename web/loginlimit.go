package web

import (
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Password logins are limited per client IP: five attempts per minute, with a
// burst of five. The limit is deliberately small because these dashboards have
// one shared account; a legitimate user rarely needs more than a few retries,
// while an attacker cannot try thousands of passwords from one address.
const (
	loginAttempts = 5
	loginWindow   = time.Minute
)

type loginLimitEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]*loginLimitEntry
	limit   rate.Limit
	burst   int
	now     func() time.Time
}

func newLoginLimiter(attempts int, window time.Duration) *loginLimiter {
	if attempts < 1 {
		attempts = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &loginLimiter{
		entries: make(map[string]*loginLimitEntry),
		limit:   rate.Limit(float64(attempts) / window.Seconds()),
		burst:   attempts,
		now:     time.Now,
	}
}

// allow reports whether one attempt from ip may proceed now. When it may not,
// the second return value is the Retry-After value in seconds.
func (l *loginLimiter) allow(ip string) (bool, int) {
	if ip == "" {
		ip = "unknown"
	}

	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// Keep abandoned client addresses from growing the map forever. This only
	// runs once the map is large, so the common path is one lookup.
	if len(l.entries) > 4096 {
		for key, entry := range l.entries {
			if now.Sub(entry.lastSeen) > 10*time.Minute {
				delete(l.entries, key)
			}
		}
	}

	entry, ok := l.entries[ip]
	if !ok {
		entry = &loginLimitEntry{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.entries[ip] = entry
	}
	entry.lastSeen = now

	if entry.limiter.AllowN(now, 1) {
		return true, 0
	}

	seconds := int(math.Ceil(1 / float64(l.limit)))
	if seconds < 1 {
		seconds = 1
	}
	return false, seconds
}
