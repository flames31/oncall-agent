package webhook

import "time"

// Alert is the normalised representation of an incoming incident,
// regardless of whether it came from Alertmanager or PagerDuty.
type Alert struct {
	// Fingerprint uniquely identifies this alert for deduplication.
	// Alertmanager provides one; we generate one for PagerDuty.
	Fingerprint string

	// Source is "alertmanager" or "pagerduty" — useful for logging.
	Source string

	ServiceName string
	AlertName   string
	Severity    string // critical | warning | info
	Description string
	StartsAt    time.Time

	// Labels contains all raw labels from the source payload.
	// Tools in Phase 3 can pull additional context from here.
	Labels map[string]string
}
