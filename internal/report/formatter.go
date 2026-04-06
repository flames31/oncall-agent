package report

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/flames31/oncall-agent/internal/llm"
	"github.com/flames31/oncall-agent/internal/webhook"
)

// Report is the fully formatted, display-ready version of an investigation.
// Every field is safe to drop directly into Slack Block Kit text.
type Report struct {
	// Header fields
	AlertName     string
	ServiceName   string
	Severity      string
	SeverityEmoji string

	// Timing
	FiringAt        string // human-readable firing time
	InvestigationMS int64  // how long the investigation took

	// LLM output — formatted for display
	ConfidenceBadge  string // e.g. "🟢 High"
	RootCause        string
	Evidence         []string
	Actions          []string
	SimilarIncidents []string

	// Metadata
	Fingerprint string
	Model       string
	Iterations  int
	Tokens      int

	// Raw fields for the threaded follow-up
	RawResult *llm.InvestigationResult
	RawAlert  webhook.Alert
}

// Build creates a Report from an InvestigationResult and the original Alert.
// investigationStart is the time RunInvestigation was called — used to
// compute how long the investigation took.
func Build(
	result *llm.InvestigationResult,
	alert webhook.Alert,
	investigationStart time.Time,
) *Report {
	return &Report{
		AlertName:        alert.AlertName,
		ServiceName:      alert.ServiceName,
		Severity:         alert.Severity,
		SeverityEmoji:    severityEmoji(alert.Severity),
		FiringAt:         alert.StartsAt.UTC().Format("Jan 2, 15:04:05 UTC"),
		InvestigationMS:  time.Since(investigationStart).Milliseconds(),
		ConfidenceBadge:  confidenceBadge(result.Confidence),
		RootCause:        truncate(result.RootCause, 300),
		Evidence:         formatList(result.Evidence, 150),
		Actions:          formatList(result.RecommendedActions, 150),
		SimilarIncidents: formatList(result.SimilarIncidents, 120),
		Fingerprint:      alert.Fingerprint,
		Model:            result.ModelUsed,
		Iterations:       result.IterationsUsed,
		Tokens:           result.TokensUsed,
		RawResult:        result,
		RawAlert:         alert,
	}
}

func severityEmoji(severity string) string {
	switch strings.ToLower(severity) {
	case "critical", "page", "fatal":
		return "🚨"
	case "warning", "warn":
		return "⚠️"
	default:
		return "ℹ️"
	}
}

func confidenceBadge(confidence string) string {
	switch strings.ToLower(confidence) {
	case "high":
		return "🟢 High"
	case "medium":
		return "🟡 Medium"
	default:
		return "🔴 Low"
	}
}

// truncate cuts a string to maxChars, appending "…" if truncated.
func truncate(s string, maxChars int) string {
	if utf8.RuneCountInString(s) <= maxChars {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxChars-1]) + "…"
}

// formatList truncates each item in a list to maxItemChars.
func formatList(items []string, maxItemChars int) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			continue
		}
		out = append(out, truncate(item, maxItemChars))
	}
	return out
}

// InvestigationDuration returns a human-readable duration string.
func (r *Report) InvestigationDuration() string {
	ms := r.InvestigationMS
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}
