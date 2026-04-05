package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	openai "github.com/sashabaranov/go-openai"

	"github.com/flames31/oncall-agent/internal/webhook"
)

// Investigator runs the ReAct loop for a single alert.
type Investigator struct {
	client  *Client
	tools   *ToolSet
	maxIter int
}

// NewInvestigator creates an Investigator with the given client, tool set,
// and iteration limit.
func NewInvestigator(client *Client, tools *ToolSet, maxIterations int) *Investigator {
	return &Investigator{
		client:  client,
		tools:   tools,
		maxIter: maxIterations,
	}
}

// RunInvestigation runs the full ReAct loop for an alert and returns
// a structured result. The context should carry the overall 45-second
// investigation deadline.
func (inv *Investigator) RunInvestigation(
	ctx context.Context,
	alert webhook.Alert,
) (*InvestigationResult, error) {
	slog.Info("investigation started",
		"fingerprint", alert.Fingerprint,
		"service", alert.ServiceName,
		"alert", alert.AlertName,
	)

	// Seed the conversation with the system prompt and initial user message
	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: SystemPrompt,
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: BuildUserMessage(alert),
		},
	}

	toolDefs := InvestigationTools()
	totalTokens := 0

	for iter := 0; iter < inv.maxIter; iter++ {
		slog.Debug("react iteration",
			"iter", iter+1,
			"max", inv.maxIter,
			"messages", len(messages),
		)

		resp, err := inv.client.Chat(ctx, messages, toolDefs)
		if err != nil {
			return nil, fmt.Errorf("iteration %d: %w", iter+1, err)
		}

		totalTokens += resp.Usage.TotalTokens

		// ── Case 1: LLM wants to call tools ──────────────────────────────
		if len(resp.ToolCalls) > 0 {
			// Append the assistant message that contains the tool call requests
			assistantMsg := openai.ChatCompletionMessage{
				Role:      openai.ChatMessageRoleAssistant,
				ToolCalls: resp.ToolCalls,
			}
			messages = append(messages, assistantMsg)

			// Execute each tool call and append its result
			for _, tc := range resp.ToolCalls {
				slog.Info("tool call",
					"iter", iter+1,
					"tool", tc.Function.Name,
					"args", tc.Function.Arguments,
				)

				observation := inv.tools.Dispatch(
					ctx,
					alert,
					tc.Function.Name,
					tc.Function.Arguments,
				)

				slog.Debug("tool observation",
					"tool", tc.Function.Name,
					"result_length", len(observation),
				)

				// Append the tool result — must reference the tool call ID
				toolResultMsg := openai.ChatCompletionMessage{
					Role:       openai.ChatMessageRoleTool,
					Content:    observation,
					ToolCallID: tc.ID,
				}
				messages = append(messages, toolResultMsg)
			}

			// Continue the loop — send the updated history back to Groq
			continue
		}

		// ── Case 2: LLM produced a text response (final answer) ──────────
		if resp.Content != "" {
			result, err := parseResult(resp.Content)
			if err != nil {
				// JSON parse failed — ask the LLM to reformat (one retry)
				slog.Warn("result parse failed, requesting reformat",
					"error", err,
					"raw", resp.Content[:min(200, len(resp.Content))],
				)

				messages = append(messages,
					openai.ChatCompletionMessage{
						Role:    openai.ChatMessageRoleAssistant,
						Content: resp.Content,
					},
					openai.ChatCompletionMessage{
						Role: openai.ChatMessageRoleUser,
						Content: "Your response could not be parsed as JSON. " +
							"Please output ONLY the JSON object — no markdown, no prose, " +
							"no code fences. Start your response with { and end with }.",
					},
				)

				// One more call to get the clean JSON
				retryResp, retryErr := inv.client.Chat(ctx, messages, nil) // no tools on retry
				if retryErr != nil {
					return failsafeResult(alert, totalTokens, inv.client.model), nil
				}
				totalTokens += retryResp.Usage.TotalTokens

				result, err = parseResult(retryResp.Content)
				if err != nil {
					slog.Error("result parse failed after retry", "error", err)
					return failsafeResult(alert, totalTokens, inv.client.model), nil
				}
			}

			result.IterationsUsed = iter + 1
			result.TokensUsed = totalTokens
			result.ModelUsed = inv.client.model

			slog.Info("investigation complete",
				"fingerprint", alert.Fingerprint,
				"confidence", result.Confidence,
				"iterations", result.IterationsUsed,
				"tokens", result.TokensUsed,
			)

			return result, nil
		}

		// Neither tool calls nor text — shouldn't happen but handle it
		slog.Warn("empty response from groq", "iter", iter+1)
	}

	// Hit iteration limit — return a low-confidence result
	slog.Warn("investigation hit max iterations",
		"fingerprint", alert.Fingerprint,
		"max_iter", inv.maxIter,
	)
	return failsafeResult(alert, totalTokens, inv.client.model), nil
}

// parseResult strips optional markdown fences and parses the JSON.
func parseResult(content string) (*InvestigationResult, error) {
	// Strip ```json ... ``` or ``` ... ``` fences if present
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) > 2 {
			// Drop first and last lines (the fences)
			content = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}

	// Find the JSON object — scan for the first { and last }
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON object found in response")
	}
	content = content[start : end+1]

	var result InvestigationResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("JSON unmarshal: %w", err)
	}

	// Normalise confidence to one of three values
	switch strings.ToLower(result.Confidence) {
	case "high", "medium", "low":
		result.Confidence = strings.ToLower(result.Confidence)
	default:
		result.Confidence = "low"
	}

	if result.RootCause == "" {
		return nil, fmt.Errorf("root_cause field is empty")
	}

	return &result, nil
}

// failsafeResult returns a minimal low-confidence result when the loop
// fails or times out — ensures the Slack report always gets something.
func failsafeResult(alert webhook.Alert, tokens int, model string) *InvestigationResult {
	return &InvestigationResult{
		RootCause:  fmt.Sprintf("Investigation incomplete for alert %q on service %q. Manual investigation required.", alert.AlertName, alert.ServiceName),
		Confidence: "low",
		Evidence:   []string{"Automated investigation did not complete within the allowed time or iteration limit."},
		RecommendedActions: []string{
			"Check Prometheus for the alerting metric manually",
			"Review recent deployments for " + alert.ServiceName,
			"Check pod logs with: kubectl logs -l app=" + alert.ServiceName + " --tail=100",
		},
		SimilarIncidents: []string{},
		TokensUsed:       tokens,
		ModelUsed:        model,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
