package llm

import (
	"fmt"
	"time"

	"github.com/flames31/oncall-agent/internal/webhook"
)

// SystemPrompt is sent as the first message in every investigation.
// It sets the LLM's role, investigation strategy, and required output format.
const SystemPrompt = `You are an experienced on-call SRE agent. You have been triggered by a
production alert. Your goal is to identify the most likely root cause and
recommend concrete remediation steps.

Investigation strategy — follow this order:
1. Query the alerting metric first to confirm the signal
2. Check recent deployments — this is the most common root cause
3. Check pod health (CrashLoopBackOff, OOMKilled, restart counts)
4. Query logs for error patterns in the alert window
5. Search runbooks for similar past incidents
6. Stop when you reach high confidence, or after using all available tools

Rules:
- Call at most one tool per step
- Do not repeat a tool call with the same arguments
- If a tool returns no data, note it and move on
- When you have enough evidence, stop calling tools and produce the final answer

You MUST end your investigation by outputting ONLY a JSON object in this
exact structure — no prose before or after it:

{
  "root_cause": "one clear sentence describing the most likely root cause",
  "confidence": "high|medium|low",
  "evidence": [
    "bullet point 1 — specific data point from a tool",
    "bullet point 2 — another supporting data point"
  ],
  "recommended_actions": [
    "step 1 — concrete action to take",
    "step 2 — next action"
  ],
  "similar_incidents": [
    "title of a matching past runbook if found, or empty array"
  ]
}`

// BuildUserMessage constructs the initial user message for an investigation.
// It injects all alert metadata so the LLM knows exactly what fired and when.
func BuildUserMessage(alert webhook.Alert) string {
	firingAge := time.Since(alert.StartsAt).Round(time.Second)

	return fmt.Sprintf(`A production alert has fired. Investigate and identify the root cause.

Alert details:
- Alert name:  %s
- Service:     %s
- Severity:    %s
- Description: %s
- Firing since: %s (%s ago)
- Source:      %s
- Labels:      %v

Begin your investigation now. Start with the alerting metric for service "%s".`,
		alert.AlertName,
		alert.ServiceName,
		alert.Severity,
		alert.Description,
		alert.StartsAt.UTC().Format("2006-01-02 15:04:05 UTC"),
		firingAge,
		alert.Source,
		alert.Labels,
		alert.ServiceName,
	)
}
