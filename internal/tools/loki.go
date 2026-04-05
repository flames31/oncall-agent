package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// LokiClient queries the Loki HTTP API.
type LokiClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewLokiClient(baseURL string) *LokiClient {
	return &LokiClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// LogSummary is what the tool returns to the LLM.
type LogSummary struct {
	TotalErrors int
	TopPatterns []LogPattern
	Sample      []string // up to 10 raw lines
}

// LogPattern is a recurring error message with its frequency count.
type LogPattern struct {
	Message string
	Count   int
}

// QueryLogs fetches ERROR/WARN logs for a service around the alert time
// and returns a human-readable summary string.
func (c *LokiClient) QueryLogs(
	ctx context.Context,
	service string,
	alertTime time.Time,
	windowMinutes int,
) (string, error) {
	start := alertTime.Add(-time.Duration(windowMinutes) * time.Minute)
	end := alertTime.Add(time.Duration(windowMinutes) * time.Minute)

	lines, err := c.fetchLogs(ctx, service, start, end)
	if err != nil {
		return "", err
	}

	if len(lines) == 0 {
		return fmt.Sprintf(
			"No ERROR or WARN logs found for service %q in the %d-minute window around the alert. "+
				"The service may use a different log label or logging format.",
			service, windowMinutes*2,
		), nil
	}

	summary := analyseLogs(lines)
	return formatLogSummary(service, summary), nil
}

// lokiResponse mirrors the Loki /loki/api/v1/query_range response shape.
type lokiResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"` // [[nanosecond_ts, "log line"], ...]
		} `json:"result"`
	} `json:"data"`
}

func (c *LokiClient) fetchLogs(
	ctx context.Context,
	service string,
	start, end time.Time,
) ([]string, error) {
	// LogQL: match service label, filter for ERROR or WARN lines
	logQL := fmt.Sprintf(`{service="%s"} |~ "ERROR|WARN|error|warn"`, service)

	params := url.Values{}
	params.Set("query", logQL)
	params.Set("start", fmt.Sprintf("%d", start.UnixNano()))
	params.Set("end", fmt.Sprintf("%d", end.UnixNano()))
	params.Set("limit", "100")
	params.Set("direction", "backward") // most recent first

	reqURL := c.baseURL + "/loki/api/v1/query_range?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building loki request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("loki request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading loki response: %w", err)
	}

	var result lokiResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing loki response: %w", err)
	}

	var lines []string
	for _, stream := range result.Data.Result {
		for _, entry := range stream.Values {
			if len(entry) >= 2 {
				lines = append(lines, entry[1])
			}
		}
	}

	return lines, nil
}

// analyseLogs counts error patterns and picks representative lines.
func analyseLogs(lines []string) LogSummary {
	// Frequency map: normalised message → count
	freq := make(map[string]int)
	for _, line := range lines {
		key := normaliseLogLine(line)
		freq[key]++
	}

	// Sort patterns by frequency descending
	type kv struct {
		msg   string
		count int
	}
	var pairs []kv
	for msg, count := range freq {
		pairs = append(pairs, kv{msg, count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].count > pairs[j].count
	})

	var top []LogPattern
	for i, p := range pairs {
		if i >= 5 {
			break
		}
		top = append(top, LogPattern{Message: p.msg, Count: p.count})
	}

	// Take up to 10 sample lines
	sample := lines
	if len(sample) > 10 {
		sample = sample[:10]
	}

	return LogSummary{
		TotalErrors: len(lines),
		TopPatterns: top,
		Sample:      sample,
	}
}

// normaliseLogLine strips timestamps, request IDs, and numbers so that
// similar log lines collapse into the same pattern key.
func normaliseLogLine(line string) string {
	// Truncate to 120 chars for the key
	if len(line) > 120 {
		line = line[:120]
	}
	return line
}

func formatLogSummary(service string, s LogSummary) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Log analysis for service %q: %d total ERROR/WARN lines found.\n\n",
		service, s.TotalErrors)

	if len(s.TopPatterns) > 0 {
		b.WriteString("Top error patterns:\n")
		for i, p := range s.TopPatterns {
			fmt.Fprintf(&b, "  %d. [%dx] %s\n", i+1, p.Count, p.Message)
		}
		b.WriteString("\n")
	}

	if len(s.Sample) > 0 {
		b.WriteString("Recent log sample (up to 10 lines):\n")
		for _, line := range s.Sample {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	return b.String()
}
