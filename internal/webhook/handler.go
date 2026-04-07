package webhook

import (
	"log/slog"
	"net/http"
	"strings"
)

// internal/webhook/handler.go — simplified Config and NewHandler

type Config struct {
	PagerDutySecret string
	OnAlert         func(Alert)
}

type Handler struct {
	cfg Config
}

func NewHandler(cfg Config) *Handler {
	return &Handler{cfg: cfg}
}

// internal/webhook/handler.go — updated ServeHTTP
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

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
		slog.Warn("unknown webhook source, trying alertmanager",
			"user_agent", r.Header.Get("User-Agent"))
		alerts, err = parseAlertmanager(r)
	}

	if err != nil {
		slog.Error("webhook parse failed", "source", source, "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Respond immediately — dedup and investigation happen asynchronously
	w.WriteHeader(http.StatusAccepted)

	for _, alert := range alerts {
		slog.Info("alert received",
			"fingerprint", alert.Fingerprint,
			"source", alert.Source,
			"service", alert.ServiceName,
			"severity", alert.Severity,
		)
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
