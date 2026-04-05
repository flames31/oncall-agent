// internal/llm/tool_definitions.go
package llm

import (
	"encoding/json"

	openai "github.com/sashabaranov/go-openai"
)

func InvestigationTools() []openai.Tool {
	return []openai.Tool{
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "query_prometheus",
				Description: "Query Prometheus for a named metric for a given service. Use this first to understand the alerting metric and related signals.",
				Parameters: mustJSON(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"metric_name": map[string]any{
							"type": "string",
							"enum": []string{"error_rate", "latency_p99", "pod_restarts", "cpu_usage", "memory_usage"},
						},
						"service": map[string]any{
							"type": "string",
						},
						"lookback_minutes": map[string]any{
							"type":    "integer",
							"default": 30,
						},
					},
					"required": []string{"metric_name", "service"},
				}),
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "query_loki",
				Description: "Query Loki for recent ERROR and WARN log lines from a service. Use after checking metrics.",
				Parameters: mustJSON(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"service": map[string]any{
							"type": "string",
						},
						"window_minutes": map[string]any{
							"type":    "integer",
							"default": 5,
						},
					},
					"required": []string{"service"},
				}),
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "get_recent_deployments",
				Description: "Check for recent deployments of a service. A deploy within 30 minutes of the alert is the most common root cause. Always call this early.",
				Parameters: mustJSON(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"service": map[string]any{
							"type": "string",
						},
						"lookback_hours": map[string]any{
							"type":    "integer",
							"default": 2,
						},
					},
					"required": []string{"service"},
				}),
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "get_pod_status",
				Description: "Get Kubernetes pod health for a service. Returns pod counts, CrashLoopBackOff, and OOMKill status.",
				Parameters: mustJSON(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"service": map[string]any{
							"type": "string",
						},
						"namespace": map[string]any{
							"type":    "string",
							"default": "default",
						},
					},
					"required": []string{"service"},
				}),
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "search_runbooks",
				Description: "Search past incident runbooks for similar problems and proven remediation steps.",
				Parameters: mustJSON(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type": "string",
						},
						"top_k": map[string]any{
							"type":    "integer",
							"default": 3,
						},
					},
					"required": []string{"query"},
				}),
			},
		},
	}
}

// mustJSON marshals v to json.RawMessage. Panics on error — only called
// at startup with hardcoded schemas so a panic here is always a code bug.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic("tool schema marshal failed: " + err.Error())
	}
	return json.RawMessage(b)
}
