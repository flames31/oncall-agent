package investigation

import (
	"log/slog"

	"github.com/flames31/oncall-agent/internal/webhook"
)

// Orchestrator wires dedup, metrics, and the worker pool together.
type Orchestrator struct {
	dedup *Deduplicator
	pool  *Pool
}

// NewOrchestrator creates an orchestrator. Call pool.Start() before
// passing the pool here, or start it separately.
func NewOrchestrator(dedup *Deduplicator, pool *Pool) *Orchestrator {
	return &Orchestrator{
		dedup: dedup,
		pool:  pool,
	}
}

// HandleAlert is the single entry point for all incoming alerts.
// It is safe to call from multiple goroutines concurrently.
func (o *Orchestrator) HandleAlert(alert webhook.Alert) {
	// 1. Emit received counter regardless of dedup outcome
	AlertsReceived.WithLabelValues(alert.Source, alert.Severity).Inc()

	// 2. Deduplication check
	if o.dedup.IsDuplicate(alert.Fingerprint) {
		AlertsDeduplicated.Inc()
		slog.Info("alert deduplicated",
			"fingerprint", alert.Fingerprint,
			"service", alert.ServiceName,
		)
		return
	}

	// 3. Submit to worker pool (non-blocking)
	if !o.pool.Submit(alert) {
		AlertsDropped.Inc()
		slog.Warn("worker pool full — alert dropped",
			"fingerprint", alert.Fingerprint,
			"service", alert.ServiceName,
			"alert", alert.AlertName,
		)
		return
	}

	slog.Info("alert queued",
		"fingerprint", alert.Fingerprint,
		"service", alert.ServiceName,
		"alert", alert.AlertName,
		"severity", alert.Severity,
	)
}
