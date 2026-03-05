package store

import (
	"encoding/json"
	"time"
)

type PullRequest struct {
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	Author        string    `json:"author"`
	State         string    `json:"state"`
	Labels        json.RawMessage `json:"labels"`
	HeadSHA       string    `json:"head_sha"`
	Additions     int       `json:"additions"`
	Deletions     int       `json:"deletions"`
	CommentsCount int       `json:"comments_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	FetchedAt     time.Time `json:"fetched_at"`
}

type Analysis struct {
	ID             int64     `json:"id"`
	PRNumber       int       `json:"pr_number"`
	Category       string    `json:"category"`
	Confidence     float64   `json:"confidence"`
	Explanation    string    `json:"explanation"`
	RelatedPRs     json.RawMessage `json:"related_prs"`
	Model          string    `json:"model"`
	PromptVersion  string    `json:"prompt_version"`
	InputTokens    int       `json:"input_tokens"`
	OutputTokens   int       `json:"output_tokens"`
	CreatedAt      time.Time `json:"created_at"`
}

type BatchJob struct {
	ID             int64     `json:"id"`
	BatchID        string    `json:"batch_id"`
	Status         string    `json:"status"`
	TotalRequests  int       `json:"total_requests"`
	Succeeded      int       `json:"succeeded"`
	Errored        int       `json:"errored"`
	Canceled       int       `json:"canceled"`
	Expired        int       `json:"expired"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type BatchRequest struct {
	ID        int64  `json:"id"`
	BatchID   string `json:"batch_id"`
	CustomID  string `json:"custom_id"`
	PRNumber  int    `json:"pr_number"`
}
