package middleware

import (
	"sync"
	"time"
)

// deduper suppresses repeat emission of the same finding within a time window.
// A finding's identity is (fingerprint, ruleName): the same rule firing on the
// same canonical query shape. Without it, Guard.Check would re-emit every
// static finding on every execution of a recurring query (or every Exec of a
// prepared statement) and flood the log sink. The N+1 detector already
// self-dedups and slow-query is intentionally per-execution; this covers the
// per-query static rules. It reuses the QueryTracker windowing shape.
//
// A deduper is safe for concurrent use.
type deduper struct {
	mu      sync.Mutex
	seen    map[string]time.Time // key -> time the finding was last allowed
	window  time.Duration
	maxKeys int
}

func newDeduper(window time.Duration) *deduper {
	return &deduper{
		seen:    make(map[string]time.Time),
		window:  window,
		maxKeys: 10000,
	}
}

// allow reports whether a finding identified by (fingerprint, rule) should be
// emitted at time now. It returns true the first time the finding is seen and
// again only after window has elapsed since it was last allowed. A window <= 0
// disables dedup, so every call returns true (the legacy report-every-time
// behavior).
func (d *deduper) allow(fingerprint, rule string, now time.Time) bool {
	if d.window <= 0 {
		return true
	}
	key := fingerprint + "\x00" + rule

	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.seen) >= d.maxKeys {
		d.evictExpired(now)
		// If eviction freed nothing (every entry still in-window) and this is a
		// new key, drop the finding rather than grow the map without bound. A
		// key already present is still updated below — never lose dedup state
		// for a finding we're actively tracking.
		if len(d.seen) >= d.maxKeys {
			if _, ok := d.seen[key]; !ok {
				return false
			}
		}
	}

	last, ok := d.seen[key]
	if !ok || now.Sub(last) > d.window {
		d.seen[key] = now
		return true
	}
	return false
}

// evictExpired removes entries whose window has elapsed. Caller holds the lock.
func (d *deduper) evictExpired(now time.Time) {
	for k, t := range d.seen {
		if now.Sub(t) > d.window {
			delete(d.seen, k)
		}
	}
}
