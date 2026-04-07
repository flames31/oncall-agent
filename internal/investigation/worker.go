package investigation

import (
	"context"
	"log/slog"
	"time"

	"github.com/flames31/oncall-agent/internal/llm"
	"github.com/flames31/oncall-agent/internal/report"
	"github.com/flames31/oncall-agent/internal/store"
	"github.com/flames31/oncall-agent/internal/webhook"
)

// WorkerConfig holds everything a worker needs to run an investigation
// and post the result to Slack.
type WorkerConfig struct {
	Investigator         *llm.Investigator
	SlackClient          *report.SlackClient
	DB                   *store.DB
	InvestigationTimeout time.Duration
}

// Pool manages a fixed set of goroutines that pull alerts from a
// shared buffered channel and run investigations.
type Pool struct {
	jobs  chan webhook.Alert
	cfg   WorkerConfig
	count int
}

// NewPool creates a worker pool but does not start it.
// Call Start() to spawn the goroutines.
func NewPool(workerCount int, cfg WorkerConfig) *Pool {
	// Buffer capacity = workerCount * 2 so a burst of alerts doesn't
	// immediately block the webhook handler
	return &Pool{
		jobs:  make(chan webhook.Alert, workerCount*2),
		cfg:   cfg,
		count: workerCount,
	}
}

// Start spawns workerCount goroutines. Call this once at startup.
// The goroutines run until the jobs channel is closed (on shutdown).
func (p *Pool) Start() {
	for i := range p.count {
		go p.runWorker(i)
	}
	slog.Info("worker pool started", "workers", p.count, "queue_cap", cap(p.jobs))
}

// Submit attempts to enqueue an alert for investigation.
// Returns false if the queue is full — caller should drop and log.
func (p *Pool) Submit(alert webhook.Alert) bool {
	select {
	case p.jobs <- alert:
		WorkerQueueDepth.Inc()
		return true
	default:
		return false
	}
}

// Stop closes the jobs channel, causing all workers to exit after
// finishing their current investigation.
func (p *Pool) Stop() {
	close(p.jobs)
}

func (p *Pool) runWorker(id int) {
	slog.Debug("worker started", "worker_id", id)

	for alert := range p.jobs {
		WorkerQueueDepth.Dec()
		WorkerPoolActive.Inc()

		p.processAlert(alert)

		WorkerPoolActive.Dec()
	}

	slog.Debug("worker stopped", "worker_id", id)
}

func (p *Pool) processAlert(alert webhook.Alert) {
	start := time.Now()

	slog.Info("worker processing alert",
		"fingerprint", alert.Fingerprint,
		"service", alert.ServiceName,
		"alert", alert.AlertName,
	)

	// Run investigation with the configured timeout
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.InvestigationTimeout)
	defer cancel()

	result, err := p.cfg.Investigator.RunInvestigation(ctx, alert)
	if err != nil {
		slog.Error("investigation failed",
			"fingerprint", alert.Fingerprint,
			"error", err,
		)
		InvestigationsCompleted.WithLabelValues("error").Inc()
		return
	}

	duration := time.Since(start).Seconds()
	InvestigationDuration.Observe(duration)
	LLMTokensUsed.Observe(float64(result.TokensUsed))
	InvestigationsCompleted.WithLabelValues(result.Confidence).Inc()

	slog.Info("investigation complete",
		"fingerprint", alert.Fingerprint,
		"confidence", result.Confidence,
		"iterations", result.IterationsUsed,
		"tokens", result.TokensUsed,
		"duration_s", duration,
	)

	// Format and deliver to Slack
	r := report.Build(result, alert, start)

	postCtx, postCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer postCancel()

	ts, err := p.cfg.SlackClient.PostReport(postCtx, r)
	if err != nil {
		slog.Error("slack delivery failed",
			"fingerprint", alert.Fingerprint,
			"error", err,
		)
		SlackDeliveries.WithLabelValues("error").Inc()
		return
	}

	SlackDeliveries.WithLabelValues("success").Inc()
	slog.Info("report delivered",
		"fingerprint", alert.Fingerprint,
		"ts", ts,
		"total_duration_s", time.Since(start).Seconds(),
	)

	// Write initial feedback record (correct=nil = not yet reviewed)
	fbCtx, fbCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer fbCancel()

	if err := p.cfg.DB.WriteFeedback(fbCtx, store.FeedbackEntry{
		AlertFingerprint: alert.Fingerprint,
		ReportJSON:       result,
		Correct:          nil,
	}); err != nil {
		slog.Warn("feedback write failed", "error", err)
	}
}
