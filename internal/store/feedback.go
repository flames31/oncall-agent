package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// FeedbackEntry is a single feedback record to insert.
type FeedbackEntry struct {
	AlertFingerprint string
	ReportJSON       interface{} // serialised to JSONB
	Correct          *bool       // nil = not yet reviewed
}

// WriteFeedback inserts a feedback row.
func (db *DB) WriteFeedback(ctx context.Context, entry FeedbackEntry) error {
	data, err := json.Marshal(entry.ReportJSON)
	if err != nil {
		return fmt.Errorf("marshalling report: %w", err)
	}

	const q = `
		INSERT INTO feedback (alert_fingerprint, report_json, correct)
		VALUES ($1, $2, $3)
	`
	if _, err := db.ExecContext(ctx, q,
		entry.AlertFingerprint,
		data,
		entry.Correct,
	); err != nil {
		return fmt.Errorf("inserting feedback: %w", err)
	}
	return nil
}

// UpsertRunbook inserts a new runbook row based on confirmed feedback.
// Called when the on-call engineer clicks "✅ Correct".
func (db *DB) UpsertRunbook(ctx context.Context, title, content string) error {
	const q = `
		INSERT INTO runbooks (title, content)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`
	if _, err := db.ExecContext(ctx, q, title, content); err != nil {
		return fmt.Errorf("upserting runbook: %w", err)
	}
	return nil
}
