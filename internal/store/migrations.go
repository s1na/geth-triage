package store

import (
	"context"
	"database/sql"
)

func runMigrations(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS pull_requests (
			number        INTEGER PRIMARY KEY,
			title         TEXT NOT NULL,
			author        TEXT NOT NULL,
			state         TEXT NOT NULL DEFAULT 'open',
			labels        TEXT NOT NULL DEFAULT '[]',
			head_sha      TEXT NOT NULL,
			additions     INTEGER NOT NULL DEFAULT 0,
			deletions     INTEGER NOT NULL DEFAULT 0,
			comments_count INTEGER NOT NULL DEFAULT 0,
			created_at    DATETIME NOT NULL,
			updated_at    DATETIME NOT NULL,
			fetched_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS analyses (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			pr_number      INTEGER NOT NULL REFERENCES pull_requests(number),
			category       TEXT NOT NULL,
			confidence     REAL NOT NULL,
			explanation    TEXT NOT NULL,
			related_prs    TEXT NOT NULL DEFAULT '[]',
			model          TEXT NOT NULL,
			prompt_version TEXT NOT NULL,
			input_tokens   INTEGER NOT NULL DEFAULT 0,
			output_tokens  INTEGER NOT NULL DEFAULT 0,
			created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_analyses_pr_number ON analyses(pr_number);
		CREATE INDEX IF NOT EXISTS idx_analyses_category ON analyses(category);

		CREATE TABLE IF NOT EXISTS service_state (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`)
	return err
}
