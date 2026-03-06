package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	owner = "ethereum"
	repo  = "go-ethereum"
)

type Client struct {
	token      string
	httpClient *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
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

// FetchPR fetches metadata for a single PR via GraphQL.
func (c *Client) FetchPR(ctx context.Context, number int) (*PRData, error) {
	vars := map[string]any{
		"owner":  owner,
		"repo":   repo,
		"number": number,
	}

	var data gqlDetailData
	if err := c.graphql(ctx, prDetailQuery, vars, &data); err != nil {
		return nil, fmt.Errorf("get PR %d: %w", number, err)
	}

	result := nodeToListPR(data.Repository.PullRequest)
	return &result, nil
}
