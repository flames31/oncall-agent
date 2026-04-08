package llm

import (
	"fmt"
	"time"

	"github.com/flames31/oncall-agent/internal/webhook"
)

// SystemPrompt is sent as the first message in every investigation.
// It sets the LLM's role, investigation strategy, and required output format.
// Replace the Investigation strategy section with:
const SystemPrompt = `You are an experienced on-call SRE agent. You have been triggered by a
production alert. Your goal is to identify the most likely root cause and
recommend concrete remediation steps.

Investigation strategy — follow this order strictly:
1. ALWAYS query error_rate for the service first to confirm the signal magnitude
2. ALWAYS check recent deployments next — a deploy within 30 min is the #1 root cause
3. Query pod_restarts to check for crash loops or instability
4. Query logs only after you have metric context — look for error patterns
5. Search runbooks last — use the alert name and symptoms as your search query
6. Stop calling tools once you have 2+ pieces of corroborating evidence

Critical rules:
- If error_rate is above 5%, that is HIGH signal — state it explicitly in your evidence
- If a deployment happened within 30 minutes, that is the most likely cause
- If Prometheus returns "no data", note it briefly and move on — do not dwell on it
- Each evidence item must cite a specific number or fact from a tool result
- Never call the same tool twice with the same arguments

You MUST end your investigation by outputting ONLY a JSON object — no prose, no markdown:

{
  "root_cause": "one clear sentence — be specific about the signal (e.g. error rate is 82%, deploy 18 min ago)",
  "confidence": "high|medium|low",
  "evidence": [
    "specific data point from a tool — include numbers",
    "second corroborating data point"
  ],
  "recommended_actions": [
    "concrete action step 1",
    "concrete action step 2"
  ],
  "similar_incidents": [
    "runbook title if found, else empty"
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
