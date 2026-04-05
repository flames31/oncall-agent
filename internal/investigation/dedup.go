package investigation

import (
	"sync"
	"time"
)

// Deduplicator tracks recent alert fingerprints and drops duplicates
// that arrive within the configured window.
type Deduplicator struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	window time.Duration
}

func NewDeduplicator(windowSeconds int) *Deduplicator {
	d := &Deduplicator{
		seen:   make(map[string]time.Time),
		window: time.Duration(windowSeconds) * time.Second,
	}
	// Periodically clean up old entries so the map doesn't grow forever
	go d.cleanup()
	return d
}

// IsDuplicate returns true if this fingerprint was seen within the window.
// If it returns false, the fingerprint is recorded as seen now.
func (d *Deduplicator) IsDuplicate(fingerprint string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if last, ok := d.seen[fingerprint]; ok {
		if time.Since(last) < d.window {
			return true
		}
	}

	d.seen[fingerprint] = time.Now()
	return false
}

// cleanup removes entries older than the dedup window every minute.
func (d *Deduplicator) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		d.mu.Lock()
		cutoff := time.Now().Add(-d.window)
		for fp, t := range d.seen {
			if t.Before(cutoff) {
				delete(d.seen, fp)
			}
		}
		d.mu.Unlock()
	}
}
