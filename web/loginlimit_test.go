package web

import (
	"testing"
	"time"
)

func TestLoginLimiterBurstPerIP(t *testing.T) {
	limiter := newLoginLimiter(2, time.Minute)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	for i := 1; i <= 2; i++ {
		if ok, _ := limiter.allow("192.0.2.1"); !ok {
			t.Fatalf("attempt %d should be allowed", i)
		}
	}
	if ok, retry := limiter.allow("192.0.2.1"); ok {
		t.Fatal("expected the bucket to be exhausted")
	} else if retry < 1 {
		t.Fatalf("expected a positive Retry-After, got %d", retry)
	}

	// Another client has its own bucket.
	if ok, _ := limiter.allow("198.51.100.2"); !ok {
		t.Fatal("expected a different IP to be allowed")
	}

	// One token refills after half the window.
	now = now.Add(31 * time.Second)
	if ok, _ := limiter.allow("192.0.2.1"); !ok {
		t.Fatal("expected a refilled token to be allowed")
	}
}
