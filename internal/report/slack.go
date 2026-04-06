package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SlackClient posts messages to the Slack API.
type SlackClient struct {
	token      string
	channel    string
	httpClient *http.Client
}

func NewSlackClient(token, channel string) *SlackClient {
	return &SlackClient{
		token:      token,
		channel:    channel,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// PostReport sends the main incident report and a threaded raw-data follow-up.
// Returns the message timestamp (ts) which identifies the thread.
func (s *SlackClient) PostReport(ctx context.Context, report *Report) (string, error) {
	blocks := buildMainBlocks(report)

	ts, err := s.postMessage(ctx, s.channel, "", blocks)
	if err != nil {
		return "", fmt.Errorf("posting main message: %w", err)
	}

	// Post the raw data dump as a thread reply
	threadBlocks := buildThreadBlocks(report)
	if _, err := s.postMessage(ctx, s.channel, ts, threadBlocks); err != nil {
		// Non-fatal — the main message was sent, thread is best-effort
		slog.Warn("failed to post thread follow-up", "error", err)
	}

	return ts, nil
}

// ─── Block Kit builders ───────────────────────────────────────────────────────

func buildMainBlocks(r *Report) []map[string]interface{} {
	blocks := []map[string]interface{}{
		// Header
		{
			"type": "header",
			"text": map[string]interface{}{
				"type":  "plain_text",
				"text":  fmt.Sprintf("%s [%s] %s — %s", r.SeverityEmoji, strings.ToUpper(r.Severity), r.AlertName, r.ServiceName),
				"emoji": true,
			},
		},
		// Alert timing row
		{
			"type": "section",
			"fields": []map[string]interface{}{
				{"type": "mrkdwn", "text": fmt.Sprintf("*Firing since:*\n%s", r.FiringAt)},
				{"type": "mrkdwn", "text": fmt.Sprintf("*Investigated in:*\n%s", r.InvestigationDuration())},
			},
		},
		{"type": "divider"},
		// Root cause
		{
			"type": "section",
			"text": map[string]interface{}{
				"type": "mrkdwn",
				"text": fmt.Sprintf("*Root Cause* %s\n%s", r.ConfidenceBadge, r.RootCause),
			},
		},
	}

	// Evidence
	if len(r.Evidence) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"type": "section",
			"text": map[string]interface{}{
				"type": "mrkdwn",
				"text": "*Supporting Evidence*\n" + bulletList(r.Evidence),
			},
		})
	}

	// Recommended actions
	if len(r.Actions) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"type": "section",
			"text": map[string]interface{}{
				"type": "mrkdwn",
				"text": "*Recommended Actions*\n" + numberedList(r.Actions),
			},
		})
	}

	// Similar past incidents
	if len(r.SimilarIncidents) > 0 {
		blocks = append(blocks, map[string]interface{}{
			"type": "section",
			"text": map[string]interface{}{
				"type": "mrkdwn",
				"text": "*Similar Past Incidents*\n" + bulletList(r.SimilarIncidents),
			},
		})
	}

	// Feedback buttons
	blocks = append(blocks,
		map[string]interface{}{"type": "divider"},
		map[string]interface{}{
			"type": "actions",
			"elements": []map[string]interface{}{
				{
					"type":      "button",
					"style":     "primary",
					"text":      map[string]interface{}{"type": "plain_text", "text": "✅ Root Cause Correct", "emoji": true},
					"value":     fmt.Sprintf("correct|%s", r.Fingerprint),
					"action_id": "feedback_correct",
				},
				{
					"type":      "button",
					"style":     "danger",
					"text":      map[string]interface{}{"type": "plain_text", "text": "❌ Incorrect", "emoji": true},
					"value":     fmt.Sprintf("incorrect|%s", r.Fingerprint),
					"action_id": "feedback_incorrect",
				},
			},
		},
		// Footer
		map[string]interface{}{
			"type": "context",
			"elements": []map[string]interface{}{
				{
					"type": "mrkdwn",
					"text": fmt.Sprintf("Model: %s | Iterations: %d | Tokens: %d | ID: %s",
						r.Model, r.Iterations, r.Tokens,
						truncate(r.Fingerprint, 12),
					),
				},
			},
		},
	)

	return blocks
}

func buildThreadBlocks(r *Report) []map[string]interface{} {
	var sb strings.Builder
	sb.WriteString("*🔍 Raw Investigation Data*\n\n")

	if len(r.Evidence) > 0 {
		sb.WriteString("*Evidence collected:*\n")
		for _, e := range r.Evidence {
			sb.WriteString(fmt.Sprintf("• %s\n", e))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("*Alert labels:*\n```%v```\n", r.RawAlert.Labels))
	sb.WriteString(fmt.Sprintf("\n*Full root cause:*\n%s", r.RawResult.RootCause))

	// Slack has a 3000-char limit per text block
	text := sb.String()
	if len(text) > 2900 {
		text = text[:2900] + "\n_(truncated)_"
	}

	return []map[string]interface{}{
		{
			"type": "section",
			"text": map[string]interface{}{
				"type": "mrkdwn",
				"text": text,
			},
		},
	}
}

// ─── Slack API calls ──────────────────────────────────────────────────────────

type slackPostResponse struct {
	OK      bool   `json:"ok"`
	TS      string `json:"ts"`
	Error   string `json:"error"`
	Channel string `json:"channel"`
}

func (s *SlackClient) postMessage(
	ctx context.Context,
	channel, threadTS string,
	blocks []map[string]interface{},
) (string, error) {
	body := map[string]interface{}{
		"channel": channel,
		"blocks":  blocks,
	}
	if threadTS != "" {
		body["thread_ts"] = threadTS
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshalling message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost,
		"https://slack.com/api/chat.postMessage",
		bytes.NewReader(payload),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("slack request: %w", err)
	}
	defer resp.Body.Close()

	var result slackPostResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parsing slack response: %w", err)
	}
	if !result.OK {
		return "", fmt.Errorf("slack API error: %s", result.Error)
	}

	return result.TS, nil
}

// ─── Feedback handler ─────────────────────────────────────────────────────────

// FeedbackPayload is what Slack sends when a button is clicked.
type FeedbackPayload struct {
	Type    string `json:"type"`
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	} `json:"actions"`
}

// ParseFeedback parses a Slack interactive component POST payload.
// Returns actionID ("feedback_correct" or "feedback_incorrect") and
// alert fingerprint.
func ParseFeedback(r *http.Request) (actionID, fingerprint string, err error) {
	if err := r.ParseForm(); err != nil {
		return "", "", fmt.Errorf("parsing form: %w", err)
	}

	raw := r.FormValue("payload")
	if raw == "" {
		return "", "", fmt.Errorf("missing payload field")
	}

	// Slack URL-encodes the JSON payload in a form field
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		return "", "", fmt.Errorf("url-decoding payload: %w", err)
	}

	var payload FeedbackPayload
	if err := json.Unmarshal([]byte(decoded), &payload); err != nil {
		return "", "", fmt.Errorf("parsing payload JSON: %w", err)
	}

	if len(payload.Actions) == 0 {
		return "", "", fmt.Errorf("no actions in payload")
	}

	action := payload.Actions[0]

	// Value format: "correct|<fingerprint>" or "incorrect|<fingerprint>"
	parts := strings.SplitN(action.Value, "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected action value format: %s", action.Value)
	}

	return action.ActionID, parts[1], nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func bulletList(items []string) string {
	lines := make([]string, len(items))
	for i, item := range items {
		lines[i] = "• " + item
	}
	return strings.Join(lines, "\n")
}

func numberedList(items []string) string {
	lines := make([]string, len(items))
	for i, item := range items {
		lines[i] = fmt.Sprintf("%d. %s", i+1, item)
	}
	return strings.Join(lines, "\n")
}
