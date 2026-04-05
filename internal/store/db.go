package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	_ "github.com/lib/pq"
)

type DB struct {
	*sql.DB
}

func New(dsn string) (*DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("connecting to db: %w", err)
	}
	return &DB{db}, nil
}

// Migrate runs all schema migrations idempotently.
// Safe to call on every startup — all statements use IF NOT EXISTS.
func (db *DB) Migrate(ctx context.Context) error {
	steps := []struct {
		name string
		sql  string
	}{
		{
			"create runbooks table",
			`CREATE TABLE IF NOT EXISTS runbooks (
				id            BIGSERIAL PRIMARY KEY,
				title         TEXT        NOT NULL,
				content       TEXT        NOT NULL,
				search_vector TSVECTOR    GENERATED ALWAYS AS (
				                  to_tsvector('english', title || ' ' || content)
				              ) STORED,
				created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
		},
		{
			"create runbooks full-text index",
			`CREATE INDEX IF NOT EXISTS runbooks_fts_idx
			     ON runbooks USING GIN (search_vector)`,
		},
		{
			"create feedback table",
			`CREATE TABLE IF NOT EXISTS feedback (
				id                BIGSERIAL   PRIMARY KEY,
				alert_fingerprint TEXT        NOT NULL,
				report_json       JSONB       NOT NULL,
				correct           BOOLEAN,
				reviewed          BOOLEAN     NOT NULL DEFAULT FALSE,
				created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`,
		},
		{
			"create deployments table",
			`CREATE TABLE IF NOT EXISTS deployments (
				id             BIGSERIAL   PRIMARY KEY,
				service        TEXT        NOT NULL,
				version        TEXT        NOT NULL,
				deployed_at    TIMESTAMPTZ NOT NULL,
				commit_sha     TEXT,
				commit_message TEXT
			)`,
		},
		{
			"create deployments index",
			`CREATE INDEX IF NOT EXISTS deployments_service_time_idx
			     ON deployments (service, deployed_at DESC)`,
		},
	}

	for _, step := range steps {
		slog.Debug("running migration", "step", step.name)
		if _, err := db.ExecContext(ctx, step.sql); err != nil {
			return fmt.Errorf("migration %q: %w", step.name, err)
		}
	}

	slog.Info("migrations complete", "steps", len(steps))
	return nil
}
