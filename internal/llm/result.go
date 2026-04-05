package llm

// InvestigationResult is the structured output the LLM produces
// at the end of the ReAct loop. It maps directly to the Slack report
// sections built in Phase 5.
type InvestigationResult struct {
	RootCause          string   `json:"root_cause"`
	Confidence         string   `json:"confidence"` // "high" | "medium" | "low"
	Evidence           []string `json:"evidence"`
	RecommendedActions []string `json:"recommended_actions"`
	SimilarIncidents   []string `json:"similar_incidents"`

	// Metadata populated by the loop, not the LLM
	IterationsUsed int    `json:"-"`
	TokensUsed     int    `json:"-"`
	ModelUsed      string `json:"-"`
}
