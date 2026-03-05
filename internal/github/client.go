package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	owner = "ethereum"
	repo  = "go-ethereum"
)

type Client struct {
	token        string
	httpClient   *http.Client
	maxDiffLines int
}

func NewClient(token string, maxDiffLines int) *Client {
	return &Client{
		token:        token,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		maxDiffLines: maxDiffLines,
	}
}

func (c *Client) ListOpenPRs(ctx context.Context) ([]PRData, error) {
	var allPRs []PRData
	var cursor *string

	for {
		vars := map[string]any{
			"owner":  owner,
			"repo":   repo,
			"cursor": cursor,
		}

		var data gqlListData
		if err := c.graphql(ctx, listPRsQuery, vars, &data); err != nil {
			return nil, fmt.Errorf("list PRs: %w", err)
		}

		prs := data.Repository.PullRequests
		for _, node := range prs.Nodes {
			allPRs = append(allPRs, nodeToListPR(node))
		}

		if !prs.PageInfo.HasNextPage {
			break
		}
		cursor = &prs.PageInfo.EndCursor
	}
	return allPRs, nil
}

func nodeToListPR(n gqlPRNode) PRData {
	var labels []string
	for _, l := range n.Labels.Nodes {
		labels = append(labels, l.Name)
	}

	author := ""
	if n.Author != nil {
		author = n.Author.Login
	}

	createdAt, _ := time.Parse(time.RFC3339, n.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, n.UpdatedAt)

	return PRData{
		Number:        n.Number,
		Title:         n.Title,
		Author:        author,
		State:         strings.ToLower(n.State),
		Labels:        labels,
		HeadSHA:       n.HeadRefOid,
		Additions:     n.Additions,
		Deletions:     n.Deletions,
		CommentsCount: n.Comments.TotalCount + n.Reviews.TotalCount,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}
}

func (c *Client) FetchPRDetail(ctx context.Context, number int) (*PRData, error) {
	vars := map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": number,
	}

	var data gqlDetailData
	if err := c.graphql(ctx, prDetailQuery, vars, &data); err != nil {
		return nil, fmt.Errorf("get PR %d: %w", number, err)
	}

	pr := data.Repository.PullRequest

	var labels []string
	for _, l := range pr.Labels.Nodes {
		labels = append(labels, l.Name)
	}

	author := ""
	if pr.Author != nil {
		author = pr.Author.Login
	}

	createdAt, _ := time.Parse(time.RFC3339, pr.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, pr.UpdatedAt)

	result := &PRData{
		Number:        pr.Number,
		Title:         pr.Title,
		Author:        author,
		State:         strings.ToLower(pr.State),
		Labels:        labels,
		HeadSHA:       pr.HeadRefOid,
		Additions:     pr.Additions,
		Deletions:     pr.Deletions,
		CommentsCount: pr.Comments.TotalCount + pr.Reviews.TotalCount,
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
	}

	// Collect comments from issue comments and review comments.
	var comments []Comment
	for _, c := range pr.Comments.Nodes {
		a := ""
		if c.Author != nil {
			a = c.Author.Login
		}
		t, _ := time.Parse(time.RFC3339, c.CreatedAt)
		comments = append(comments, Comment{Author: a, Body: c.Body, CreatedAt: t})
	}
	for _, r := range pr.Reviews.Nodes {
		// Include the review body itself if non-empty.
		if r.Body != "" {
			a := ""
			if r.Author != nil {
				a = r.Author.Login
			}
			t, _ := time.Parse(time.RFC3339, r.CreatedAt)
			comments = append(comments, Comment{Author: a, Body: r.Body, CreatedAt: t})
		}
		// Include inline review comments.
		for _, rc := range r.Comments.Nodes {
			a := ""
			if rc.Author != nil {
				a = rc.Author.Login
			}
			t, _ := time.Parse(time.RFC3339, rc.CreatedAt)
			comments = append(comments, Comment{Author: a, Body: rc.Body, CreatedAt: t})
		}
	}
	result.Comments = comments

	// Fetch diff via REST (not available in GraphQL).
	diff, err := c.fetchDiff(ctx, number)
	if err == nil {
		result.Diff = diff
	}

	return result, nil
}

func (c *Client) fetchDiff(ctx context.Context, number int) (string, error) {
	// Try authenticated REST API first.
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, number)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("create diff request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github.v3.diff")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch diff: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("read diff: %w", err)
		}
		return truncateDiff(string(b), c.maxDiffLines), nil
	}

	// Fallback: public diff URL.
	diffURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d.diff", owner, repo, number)
	req2, err := http.NewRequestWithContext(ctx, "GET", diffURL, nil)
	if err != nil {
		return "", fmt.Errorf("create fallback diff request: %w", err)
	}
	resp2, err := c.httpClient.Do(req2)
	if err != nil {
		return "", fmt.Errorf("fetch fallback diff: %w", err)
	}
	defer resp2.Body.Close()

	b, _ := io.ReadAll(resp2.Body)
	return truncateDiff(string(b), c.maxDiffLines), nil
}

func truncateDiff(diff string, maxLines int) string {
	lines := strings.Split(diff, "\n")
	if len(lines) <= maxLines {
		return diff
	}
	truncated := strings.Join(lines[:maxLines], "\n")
	return truncated + fmt.Sprintf("\n\n... [truncated: %d/%d lines shown]", maxLines, len(lines))
}
