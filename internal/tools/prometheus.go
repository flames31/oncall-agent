package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// PrometheusClient queries the Prometheus HTTP API.
type PrometheusClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewPrometheusClient(baseURL string) *PrometheusClient {
	return &PrometheusClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// namedQueries maps the short names the LLM uses to PromQL templates.
// %s is replaced with the service name.
var namedQueries = map[string]string{
	"error_rate": `rate(http_requests_total{service="%s",status=~"5.."}[5m])`,
	"latency_p99": `histogram_quantile(0.99,` +
		`rate(http_request_duration_seconds_bucket{service="%s"}[5m]))`,
	"pod_restarts": `increase(kube_pod_container_status_restarts_total{pod=~"%s.*"}[1h])`,
	"cpu_usage":    `rate(container_cpu_usage_seconds_total{pod=~"%s.*"}[5m])`,
	"memory_usage": `container_memory_working_set_bytes{pod=~"%s.*"}`,
}

// QueryNamed runs a named metric query for a service over the last
// lookbackMinutes and returns a human-readable summary.
func (c *PrometheusClient) QueryNamed(
	ctx context.Context,
	metricName, service string,
	lookbackMinutes int,
) (string, error) {
	tmpl, ok := namedQueries[metricName]
	if !ok {
		return "", fmt.Errorf("unknown metric %q — valid names: %s",
			metricName, strings.Join(validMetricNames(), ", "))
	}

	query := fmt.Sprintf(tmpl, service)
	end := time.Now()
	start := end.Add(-time.Duration(lookbackMinutes) * time.Minute)

	samples, err := c.queryRange(ctx, query, start, end)
	if err != nil {
		return "", err
	}

	return summariseMetric(metricName, service, samples, lookbackMinutes), nil
}

// prometheusResponse mirrors the Prometheus /api/v1/query_range response.
type prometheusResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]interface{}   `json:"values"` // [[timestamp, "value"], ...]
		} `json:"result"`
	} `json:"data"`
	Error string `json:"error"`
}

func (c *PrometheusClient) queryRange(
	ctx context.Context,
	query string,
	start, end time.Time,
) ([]float64, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", start.Format(time.RFC3339))
	params.Set("end", end.Format(time.RFC3339))
	params.Set("step", "60") // 1-minute resolution

	reqURL := c.baseURL + "/api/v1/query_range?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var result prometheusResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("prometheus error: %s", result.Error)
	}

	// Flatten all time series values into a single slice of floats
	var values []float64
	for _, series := range result.Data.Result {
		for _, point := range series.Values {
			if len(point) < 2 {
				continue
			}
			valStr, ok := point[1].(string)
			if !ok {
				continue
			}
			var v float64
			fmt.Sscanf(valStr, "%f", &v)
			if !math.IsNaN(v) && !math.IsInf(v, 0) {
				values = append(values, v)
			}
		}
	}

	return values, nil
}

// summariseMetric turns raw float samples into a readable sentence
// the LLM can act on.
func summariseMetric(name, service string, values []float64, lookbackMinutes int) string {
	if len(values) == 0 {
		return fmt.Sprintf("No data found for metric %q on service %q in the last %d minutes. "+
			"The service may not be instrumented or may not exist in Prometheus.",
			name, service, lookbackMinutes)
	}

	sort.Float64s(values)
	min := values[0]
	max := values[len(values)-1]
	avg := average(values)
	latest := values[len(values)-1]

	switch name {
	case "error_rate":
		return fmt.Sprintf(
			"Service %q error rate over last %d min: current=%.2f%%, avg=%.2f%%, peak=%.2f%%. "+
				"Based on %d data points.",
			service, lookbackMinutes,
			latest*100, avg*100, max*100, len(values),
		)
	case "latency_p99":
		return fmt.Sprintf(
			"Service %q p99 latency over last %d min: current=%.0fms, avg=%.0fms, peak=%.0fms. "+
				"Based on %d data points.",
			service, lookbackMinutes,
			latest*1000, avg*1000, max*1000, len(values),
		)
	case "pod_restarts":
		return fmt.Sprintf(
			"Service %q pod restarts in last %d min: current=%.0f, max=%.0f, min=%.0f. "+
				"Based on %d data points.",
			service, lookbackMinutes,
			latest, max, min, len(values),
		)
	case "cpu_usage":
		return fmt.Sprintf(
			"Service %q CPU usage over last %d min: current=%.3f cores, avg=%.3f, peak=%.3f. "+
				"Based on %d data points.",
			service, lookbackMinutes,
			latest, avg, max, len(values),
		)
	case "memory_usage":
		return fmt.Sprintf(
			"Service %q memory usage over last %d min: current=%.0f MiB, avg=%.0f MiB, peak=%.0f MiB. "+
				"Based on %d data points.",
			service, lookbackMinutes,
			latest/1024/1024, avg/1024/1024, max/1024/1024, len(values),
		)
	default:
		return fmt.Sprintf(
			"Metric %q for service %q over last %d min: current=%.4f, avg=%.4f, peak=%.4f. "+
				"Based on %d data points.",
			name, service, lookbackMinutes,
			latest, avg, max, len(values),
		)
	}
}

func average(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vs {
		sum += v
	}
	return sum / float64(len(vs))
}

func validMetricNames() []string {
	names := make([]string, 0, len(namedQueries))
	for k := range namedQueries {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
