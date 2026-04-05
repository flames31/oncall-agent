package webhook

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// alertmanagerPayload mirrors the Alertmanager webhook JSON structure.
type alertmanagerPayload struct {
	Alerts []alertmanagerAlert `json:"alerts"`
}

type alertmanagerAlert struct {
	Fingerprint string            `json:"fingerprint"`
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    time.Time         `json:"startsAt"`
}

// parseAlertmanager reads an Alertmanager webhook request body and returns
// one Alert per firing alert in the payload.
func parseAlertmanager(r *http.Request) ([]Alert, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MB limit
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}

	var payload alertmanagerPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parsing alertmanager payload: %w", err)
	}

	var alerts []Alert
	for _, a := range payload.Alerts {
		// Only process firing alerts — skip resolved
		if a.Status != "firing" {
			continue
		}

		alerts = append(alerts, Alert{
			Fingerprint: a.Fingerprint,
			Source:      "alertmanager",
			ServiceName: extractServiceName(a.Labels),
			AlertName:   a.Labels["alertname"],
			Severity:    normaliseSeverity(a.Labels["severity"]),
			Description: a.Annotations["description"],
			StartsAt:    a.StartsAt,
			Labels:      a.Labels,
		})
	}

	return alerts, nil
}

// extractServiceName tries common label keys in order of preference.
func extractServiceName(labels map[string]string) string {
	for _, key := range []string{"service", "job", "app", "container"} {
		if v, ok := labels[key]; ok && v != "" {
			return v
		}
	}
	return "unknown"
}

// normaliseSeverity maps freeform severity strings to the three levels
// the rest of the system understands.
func normaliseSeverity(s string) string {
	switch s {
	case "critical", "page", "fatal":
		return "critical"
	case "warning", "warn":
		return "warning"
	default:
		return "info"
	}
}
