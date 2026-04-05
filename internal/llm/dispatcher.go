package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/flames31/oncall-agent/internal/tools"
	"github.com/flames31/oncall-agent/internal/webhook"
)

// ToolSet holds all the Phase 3 clients the dispatcher can call.
type ToolSet struct {
	Prometheus  *tools.PrometheusClient
	Loki        *tools.LokiClient
	Deployments *tools.DeploymentClient
	Kubernetes  *tools.KubernetesClient // may be nil if no cluster available
	Runbooks    *tools.RunbookClient
}

// Dispatch executes the named tool with the given JSON arguments and
// returns a plain string result for the LLM to read as an observation.
//
// All errors are returned as readable strings rather than Go errors —
// the LLM should see "Tool failed: connection refused" as an observation
// and continue investigating, not crash the whole loop.
func (ts *ToolSet) Dispatch(
	ctx context.Context,
	alert webhook.Alert,
	toolName string,
	argsJSON string,
) string {
	// Give each individual tool call a 15-second deadline
	toolCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	switch toolName {

	case "query_prometheus":
		var args struct {
			MetricName      string `json:"metric_name"`
			Service         string `json:"service"`
			LookbackMinutes int    `json:"lookback_minutes"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Tool error: invalid arguments: %v", err)
		}
		if args.LookbackMinutes == 0 {
			args.LookbackMinutes = 30
		}
		result, err := ts.Prometheus.QueryNamed(toolCtx, args.MetricName, args.Service, args.LookbackMinutes)
		if err != nil {
			return fmt.Sprintf("Prometheus query failed: %v", err)
		}
		return result

	case "query_loki":
		var args struct {
			Service       string `json:"service"`
			WindowMinutes int    `json:"window_minutes"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Tool error: invalid arguments: %v", err)
		}
		if args.WindowMinutes == 0 {
			args.WindowMinutes = 5
		}
		result, err := ts.Loki.QueryLogs(toolCtx, args.Service, alert.StartsAt, args.WindowMinutes)
		if err != nil {
			return fmt.Sprintf("Loki query failed: %v", err)
		}
		return result

	case "get_recent_deployments":
		var args struct {
			Service       string `json:"service"`
			LookbackHours int    `json:"lookback_hours"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Tool error: invalid arguments: %v", err)
		}
		if args.LookbackHours == 0 {
			args.LookbackHours = 2
		}
		result, err := ts.Deployments.GetRecentDeployments(toolCtx, args.Service, alert.StartsAt, args.LookbackHours)
		if err != nil {
			return fmt.Sprintf("Deployment query failed: %v", err)
		}
		return result

	case "get_pod_status":
		if ts.Kubernetes == nil {
			return "Kubernetes client unavailable — no cluster configured for local development. " +
				"Pod status cannot be checked in this environment."
		}
		var args struct {
			Service   string `json:"service"`
			Namespace string `json:"namespace"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Tool error: invalid arguments: %v", err)
		}
		if args.Namespace == "" {
			args.Namespace = "default"
		}
		result, err := ts.Kubernetes.GetPodStatus(toolCtx, args.Service, args.Namespace)
		if err != nil {
			return fmt.Sprintf("Kubernetes query failed: %v", err)
		}
		return result

	case "search_runbooks":
		var args struct {
			Query string `json:"query"`
			TopK  int    `json:"top_k"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return fmt.Sprintf("Tool error: invalid arguments: %v", err)
		}
		if args.TopK == 0 {
			args.TopK = 3
		}
		result, err := ts.Runbooks.Search(toolCtx, args.Query, args.TopK)
		if err != nil {
			return fmt.Sprintf("Runbook search failed: %v", err)
		}
		return result

	default:
		return fmt.Sprintf("Unknown tool %q — the LLM called a tool that does not exist.", toolName)
	}
}
