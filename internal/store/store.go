package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func New(ctx context.Context, dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	s := &Store{db: db}
	if err := runMigrations(ctx, db); err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// --- Pull Requests ---

func (s *Store) UpsertPR(ctx context.Context, pr *PullRequest) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pull_requests (number, title, author, state, labels, head_sha, additions, deletions, comments_count, created_at, updated_at, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(number) DO UPDATE SET
			title=excluded.title, author=excluded.author, state=excluded.state, labels=excluded.labels,
			head_sha=excluded.head_sha, additions=excluded.additions, deletions=excluded.deletions,
			comments_count=excluded.comments_count, updated_at=excluded.updated_at, fetched_at=excluded.fetched_at`,
		pr.Number, pr.Title, pr.Author, pr.State, pr.Labels, pr.HeadSHA,
		pr.Additions, pr.Deletions, pr.CommentsCount, pr.CreatedAt, pr.UpdatedAt, pr.FetchedAt,
	)
	return err
}

func (s *Store) GetPR(ctx context.Context, number int) (*PullRequest, error) {
	pr := &PullRequest{}
	err := s.db.QueryRowContext(ctx, `SELECT number, title, author, state, labels, head_sha, additions, deletions, comments_count, created_at, updated_at, fetched_at FROM pull_requests WHERE number = ?`, number).
		Scan(&pr.Number, &pr.Title, &pr.Author, &pr.State, &pr.Labels, &pr.HeadSHA, &pr.Additions, &pr.Deletions, &pr.CommentsCount, &pr.CreatedAt, &pr.UpdatedAt, &pr.FetchedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return pr, err
}

type ListPRsParams struct {
	Category   string
	MinConf    float64
	MaxConf    float64
	Author     string
	SortBy     string // "number", "updated_at", "confidence", "category"
	SortOrder  string // "asc", "desc"
	Limit      int
	Offset     int
}

type PRWithAnalysis struct {
	PullRequest
	Category    *string  `json:"category"`
	Confidence  *float64 `json:"confidence"`
	Explanation *string  `json:"explanation"`
	AnalyzedAt  *time.Time `json:"analyzed_at"`
}

func (s *Store) ListPRs(ctx context.Context, p ListPRsParams) ([]PRWithAnalysis, int, error) {
	// Default sort
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 200 {
		p.Limit = 200
	}
	sortCol := "pr.number"
	switch p.SortBy {
	case "updated_at":
		sortCol = "pr.updated_at"
	case "confidence":
		sortCol = "a.confidence"
	case "category":
		sortCol = "a.category"
	}
	sortDir := "DESC"
	if p.SortOrder == "asc" {
		sortDir = "ASC"
	}

	var conditions []string
	var args []any

	conditions = append(conditions, "pr.state = 'open'")

	if p.Category != "" {
		conditions = append(conditions, "a.category = ?")
		args = append(args, p.Category)
	}
	if p.MinConf > 0 {
		conditions = append(conditions, "a.confidence >= ?")
		args = append(args, p.MinConf)
	}
	if p.MaxConf > 0 {
		conditions = append(conditions, "a.confidence <= ?")
		args = append(args, p.MaxConf)
	}
	if p.Author != "" {
		conditions = append(conditions, "pr.author = ?")
		args = append(args, p.Author)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Latest analysis per PR via subquery
	baseQuery := fmt.Sprintf(`
		FROM pull_requests pr
		LEFT JOIN (
			SELECT a1.* FROM analyses a1
			INNER JOIN (SELECT pr_number, MAX(id) as max_id FROM analyses GROUP BY pr_number) a2
			ON a1.id = a2.max_id
		) a ON pr.number = a.pr_number
		%s`, where)

	// Count
	var total int
	countArgs := make([]any, len(args))
	copy(countArgs, args)
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) "+baseQuery, countArgs...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// Fetch
	query := fmt.Sprintf(`SELECT pr.number, pr.title, pr.author, pr.state, pr.labels, pr.head_sha,
		pr.additions, pr.deletions, pr.comments_count, pr.created_at, pr.updated_at, pr.fetched_at,
		a.category, a.confidence, a.explanation, a.created_at
		%s ORDER BY %s %s LIMIT ? OFFSET ?`, baseQuery, sortCol, sortDir)
	args = append(args, p.Limit, p.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var results []PRWithAnalysis
	for rows.Next() {
		var r PRWithAnalysis
		err := rows.Scan(
			&r.Number, &r.Title, &r.Author, &r.State, &r.Labels, &r.HeadSHA,
			&r.Additions, &r.Deletions, &r.CommentsCount, &r.CreatedAt, &r.UpdatedAt, &r.FetchedAt,
			&r.Category, &r.Confidence, &r.Explanation, &r.AnalyzedAt,
		)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, r)
	}
	return results, total, rows.Err()
}

// CloseStale marks PRs as closed if they are no longer in the set of open PR numbers.
func (s *Store) CloseStale(ctx context.Context, openNumbers map[int]bool) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT number FROM pull_requests WHERE state = 'open'`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var toClose []int
	for rows.Next() {
		var num int
		if err := rows.Scan(&num); err != nil {
			return 0, err
		}
		if !openNumbers[num] {
			toClose = append(toClose, num)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, num := range toClose {
		if _, err := s.db.ExecContext(ctx, `UPDATE pull_requests SET state = 'closed' WHERE number = ?`, num); err != nil {
			return 0, err
		}
	}
	return len(toClose), nil
}

// --- Analyses ---

func (s *Store) InsertAnalysis(ctx context.Context, a *Analysis) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO analyses (pr_number, category, confidence, explanation, related_prs, model, prompt_version, input_tokens, output_tokens, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.PRNumber, a.Category, a.Confidence, a.Explanation, a.RelatedPRs, a.Model, a.PromptVersion, a.InputTokens, a.OutputTokens, a.CreatedAt,
	)
	return err
}

func (s *Store) AnalysisHistory(ctx context.Context, prNumber int) ([]Analysis, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, pr_number, category, confidence, explanation, related_prs, model, prompt_version, input_tokens, output_tokens, created_at
		FROM analyses WHERE pr_number = ? ORDER BY id DESC`, prNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Analysis
	for rows.Next() {
		var a Analysis
		if err := rows.Scan(&a.ID, &a.PRNumber, &a.Category, &a.Confidence, &a.Explanation, &a.RelatedPRs, &a.Model, &a.PromptVersion, &a.InputTokens, &a.OutputTokens, &a.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, a)
	}
	return results, rows.Err()
}

// --- Change Detection ---

func (s *Store) PRsNeedingAnalysis(ctx context.Context, promptVersion string) ([]PullRequest, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT pr.number, pr.title, pr.author, pr.state, pr.labels, pr.head_sha,
			pr.additions, pr.deletions, pr.comments_count, pr.created_at, pr.updated_at, pr.fetched_at
		FROM pull_requests pr
		LEFT JOIN (
			SELECT pr_number, MAX(id) as max_id FROM analyses GROUP BY pr_number
		) latest ON pr.number = latest.pr_number
		LEFT JOIN analyses a ON a.id = latest.max_id
		WHERE pr.state = 'open'
		AND (
			a.id IS NULL
			OR a.prompt_version != ?
			OR a.pr_number IN (
				SELECT pr2.number FROM pull_requests pr2
				LEFT JOIN analyses a2 ON a2.id = (SELECT MAX(id) FROM analyses WHERE pr_number = pr2.number)
				WHERE pr2.state = 'open' AND a2.id IS NOT NULL
				AND pr2.head_sha != (
					SELECT value FROM service_state WHERE key = 'analyzed_sha_' || pr2.number
				)
			)
		)`, promptVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var prs []PullRequest
	for rows.Next() {
		var pr PullRequest
		if err := rows.Scan(&pr.Number, &pr.Title, &pr.Author, &pr.State, &pr.Labels, &pr.HeadSHA, &pr.Additions, &pr.Deletions, &pr.CommentsCount, &pr.CreatedAt, &pr.UpdatedAt, &pr.FetchedAt); err != nil {
			return nil, err
		}
		prs = append(prs, pr)
	}
	return prs, rows.Err()
}

func (s *Store) SetAnalyzedSHA(ctx context.Context, prNumber int, sha string) error {
	key := fmt.Sprintf("analyzed_sha_%d", prNumber)
	return s.SetState(ctx, key, sha)
}

// --- Service State ---

func (s *Store) GetState(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM service_state WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func (s *Store) SetState(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO service_state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// --- Stats ---

type Stats struct {
	TotalPRs       int            `json:"total_prs"`
	AnalyzedPRs    int            `json:"analyzed_prs"`
	CategoryCounts map[string]int `json:"category_counts"`
	AvgConfidence  float64        `json:"avg_confidence"`
	LastPollTime   *time.Time     `json:"last_poll_time,omitempty"`
}

func (s *Store) GetStats(ctx context.Context) (*Stats, error) {
	stats := &Stats{CategoryCounts: make(map[string]int)}

	// Last poll time
	lastPollStr, _ := s.GetState(ctx, "last_poll_time")
	if lastPollStr != "" {
		if t, err := time.Parse(time.RFC3339, lastPollStr); err == nil {
			stats.LastPollTime = &t
		}
	}

	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pull_requests WHERE state = 'open'`).Scan(&stats.TotalPRs); err != nil {
		return nil, err
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT a.pr_number) FROM analyses a
		JOIN pull_requests pr ON pr.number = a.pr_number WHERE pr.state = 'open'`).Scan(&stats.AnalyzedPRs); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT a.category, COUNT(*), AVG(a.confidence) FROM analyses a
		INNER JOIN (SELECT pr_number, MAX(id) as max_id FROM analyses GROUP BY pr_number) latest ON a.id = latest.max_id
		JOIN pull_requests pr ON pr.number = a.pr_number
		WHERE pr.state = 'open'
		GROUP BY a.category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var totalConf float64
	var totalCount int
	for rows.Next() {
		var cat string
		var count int
		var avgConf float64
		if err := rows.Scan(&cat, &count, &avgConf); err != nil {
			return nil, err
		}
		stats.CategoryCounts[cat] = count
		totalConf += avgConf * float64(count)
		totalCount += count
	}
	if totalCount > 0 {
		stats.AvgConfidence = totalConf / float64(totalCount)
	}
	return stats, rows.Err()
}
