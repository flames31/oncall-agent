package tools

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// RunbookClient searches the runbooks table using Postgres full-text search.
type RunbookClient struct {
	db *sql.DB
}

func NewRunbookClient(db *sql.DB) *RunbookClient {
	return &RunbookClient{db: db}
}

// RunbookResult is a single search hit.
type RunbookResult struct {
	ID      int64
	Title   string
	Snippet string  // ts_headline excerpt — matched terms highlighted
	Score   float64 // ts_rank score
}

// Search runs a full-text search over the runbooks table and returns
// a human-readable summary of the top-K results.
func (c *RunbookClient) Search(
	ctx context.Context,
	query string,
	topK int,
) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "No runbook search query provided.", nil
	}

	const q = `
		SELECT
			id,
			title,
			ts_rank(search_vector, plainto_tsquery('english', $1)) AS score,
			ts_headline(
				'english',
				content,
				plainto_tsquery('english', $1),
				'MaxFragments=2, MaxWords=35, MinWords=10, StartSel=«, StopSel=»'
			) AS snippet
		FROM runbooks
		WHERE search_vector @@ plainto_tsquery('english', $1)
		ORDER BY score DESC
		LIMIT $2
	`

	rows, err := c.db.QueryContext(ctx, q, query, topK)
	if err != nil {
		return "", fmt.Errorf("runbook search query: %w", err)
	}
	defer rows.Close()

	var results []RunbookResult
	for rows.Next() {
		var r RunbookResult
		if err := rows.Scan(&r.ID, &r.Title, &r.Score, &r.Snippet); err != nil {
			return "", fmt.Errorf("scanning runbook row: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterating runbook rows: %w", err)
	}

	return formatRunbookResults(query, results), nil
}

func formatRunbookResults(query string, results []RunbookResult) string {
	if len(results) == 0 {
		return fmt.Sprintf(
			"No runbooks found matching %q. "+
				"The runbook store may be empty — run the seed script (Phase 7) to populate it.",
			query,
		)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Runbook search for %q — %d result(s) found:\n\n", query, len(results))

	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s (relevance score: %.4f)\n", i+1, r.Title, r.Score)
		fmt.Fprintf(&b, "   %s\n\n", r.Snippet)
	}

	return b.String()
}
