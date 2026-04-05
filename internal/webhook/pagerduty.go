package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// pagerdutyPayload mirrors the PagerDuty V3 webhook JSON structure.
type pagerdutyPayload struct {
	Event pagerdutyEvent `json:"event"`
}

type pagerdutyEvent struct {
	ID           string        `json:"id"`
	EventType    string        `json:"event_type"`
	ResourceType string        `json:"resource_type"`
	OccurredAt   time.Time     `json:"occurred_at"`
	Data         pagerdutyData `json:"data"`
}

type pagerdutyData struct {
	ID      string           `json:"id"`
	Number  int              `json:"number"`
	Title   string           `json:"title"`
	Status  string           `json:"status"`
	Urgency string           `json:"urgency"`
	Service pagerdutyService `json:"service"`
}

type pagerdutyService struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}

// parsePagerDuty reads a PagerDuty V3 webhook request, optionally verifies
// the HMAC signature, and returns an Alert if the event is incident.triggered.
//
// Pass an empty secret to skip signature verification during local development.
func parsePagerDuty(r *http.Request, secret string) ([]Alert, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}

	// Verify HMAC-SHA256 signature if a secret is configured
	if secret != "" {
		if err := verifyPagerDutySignature(r, body, secret); err != nil {
			return nil, fmt.Errorf("signature verification failed: %w", err)
		}
	}

	var payload pagerdutyPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parsing pagerduty payload: %w", err)
	}

	// Only act on incident.triggered — ignore acknowledged, resolved, etc.
	if payload.Event.EventType != "incident.triggered" {
		return nil, nil // not an error, just not something we handle
	}

	evt := payload.Event
	inc := evt.Data

	alert := Alert{
		// PagerDuty doesn't use the word "fingerprint" but the event ID
		// is unique per occurrence and stable for deduplication purposes.
		Fingerprint: evt.ID,
		Source:      "pagerduty",
		ServiceName: inc.Service.Summary,
		AlertName:   inc.Title,
		Severity:    pdUrgencyToSeverity(inc.Urgency),
		Description: inc.Title,
		StartsAt:    evt.OccurredAt,
		Labels: map[string]string{
			"incident_id":     inc.ID,
			"incident_number": fmt.Sprintf("%d", inc.Number),
			"service_id":      inc.Service.ID,
			"urgency":         inc.Urgency,
		},
	}

	return []Alert{alert}, nil
}

// verifyPagerDutySignature checks the x-pagerduty-signature header.
// PagerDuty signs the raw request body with HMAC-SHA256 using the
// subscription secret and sends "v1=<hex>" in the header.
func verifyPagerDutySignature(r *http.Request, body []byte, secret string) error {
	header := r.Header.Get("x-pagerduty-signature")
	if header == "" {
		return fmt.Errorf("missing x-pagerduty-signature header")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := "v1=" + hex.EncodeToString(mac.Sum(nil))

	// The header may contain multiple signatures separated by commas.
	// Accept if any of them match.
	for _, sig := range strings.Split(header, ",") {
		if hmac.Equal([]byte(strings.TrimSpace(sig)), []byte(expected)) {
			return nil
		}
	}

	return fmt.Errorf("signature mismatch")
}

func pdUrgencyToSeverity(urgency string) string {
	if urgency == "high" {
		return "critical"
	}
	return "warning"
}
