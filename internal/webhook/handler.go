package webhook

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/flames31/oncall-agent/internal/investigation"
)

// Config holds the dependencies the handler needs.
type Config struct {
	PagerDutySecret string

	// OnAlert is called for every valid, non-duplicate alert.
	// In Phase 2 this is a simple logging stub.
	// In Phase 6 it becomes the worker pool dispatcher.
	OnAlert func(Alert)
}

// Handler is the HTTP handler for POST /webhook.
type Handler struct {
	cfg   Config
	dedup *investigation.Deduplicator
}

// NewHandler creates a webhook handler with the given config.
func NewHandler(cfg Config, dedup *investigation.Deduplicator) *Handler {
	return &Handler{cfg: cfg, dedup: dedup}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Detect source from User-Agent or Content-Type header.
	// Alertmanager sends "Alertmanager/<version>" in User-Agent.
	// PagerDuty sends "PagerDuty-Webhook/V3.0".
	source := detectSource(r)

	var (
		alerts []Alert
		err    error
	)

	switch source {
	case "alertmanager":
		alerts, err = parseAlertmanager(r)
	case "pagerduty":
		alerts, err = parsePagerDuty(r, h.cfg.PagerDutySecret)
	default:
		// Unknown source — try Alertmanager format as fallback
		slog.Warn("unknown webhook source, attempting alertmanager parse",
			"user_agent", r.Header.Get("User-Agent"))
		alerts, err = parseAlertmanager(r)
	}

	if err != nil {
		slog.Error("failed to parse webhook", "source", source, "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Respond immediately — Alertmanager and PagerDuty both expect a fast 2xx
	w.WriteHeader(http.StatusAccepted)

	// Process after responding so we don't block the HTTP response
	for _, alert := range alerts {
		slog.Info("alert received",
			"fingerprint", alert.Fingerprint,
			"source", alert.Source,
			"service", alert.ServiceName,
			"alert", alert.AlertName,
			"severity", alert.Severity,
		)

		if h.dedup.IsDuplicate(alert.Fingerprint) {
			slog.Info("alert deduplicated",
				"fingerprint", alert.Fingerprint,
				"service", alert.ServiceName,
			)
			continue
		}

		if h.cfg.OnAlert != nil {
			h.cfg.OnAlert(alert)
		}
	}
}

// detectSource inspects request headers to identify the webhook source.
func detectSource(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	if strings.HasPrefix(ua, "Alertmanager/") {
		return "alertmanager"
	}
	if strings.HasPrefix(ua, "PagerDuty-Webhook/") {
		return "pagerduty"
	}
	// PagerDuty also sets this header
	if r.Header.Get("x-pagerduty-signature") != "" {
		return "pagerduty"
	}
	return "unknown"
}
