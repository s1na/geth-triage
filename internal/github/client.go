package github

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	gh "github.com/google/go-github/v68/github"
	"golang.org/x/oauth2"
)

const (
	owner = "ethereum"
	repo  = "go-ethereum"
)

type Client struct {
	gh           *gh.Client
	maxDiffLines int
}

func NewClient(token string, maxDiffLines int) *Client {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(context.Background(), ts)
	return &Client{
		gh:           gh.NewClient(tc),
		maxDiffLines: maxDiffLines,
	}
}

func (c *Client) ListOpenPRs(ctx context.Context) ([]PRData, error) {
	var allPRs []PRData
	opts := &gh.PullRequestListOptions{
		State:     "open",
		Sort:      "updated",
		Direction: "desc",
		ListOptions: gh.ListOptions{
			PerPage: 100,
		},
	}

	for {
		prs, resp, err := c.gh.PullRequests.List(ctx, owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("list PRs: %w", err)
		}
		for _, pr := range prs {
			var labels []string
			for _, l := range pr.Labels {
				labels = append(labels, l.GetName())
			}
			allPRs = append(allPRs, PRData{
				Number:    pr.GetNumber(),
				Title:     pr.GetTitle(),
				Author:    pr.GetUser().GetLogin(),
				State:     pr.GetState(),
				Labels:    labels,
				HeadSHA:   pr.GetHead().GetSHA(),
				Additions: pr.GetAdditions(),
				Deletions: pr.GetDeletions(),
				CommentsCount: pr.GetComments() + pr.GetReviewComments(),
				CreatedAt: pr.GetCreatedAt().Time,
				UpdatedAt: pr.GetUpdatedAt().Time,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return allPRs, nil
}

func (c *Client) FetchPRDetail(ctx context.Context, number int) (*PRData, error) {
	pr, _, err := c.gh.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return nil, fmt.Errorf("get PR %d: %w", number, err)
	}

	var labels []string
	for _, l := range pr.Labels {
		labels = append(labels, l.GetName())
	}

	data := &PRData{
		Number:        pr.GetNumber(),
		Title:         pr.GetTitle(),
		Author:        pr.GetUser().GetLogin(),
		State:         pr.GetState(),
		Labels:        labels,
		HeadSHA:       pr.GetHead().GetSHA(),
		Additions:     pr.GetAdditions(),
		Deletions:     pr.GetDeletions(),
		CommentsCount: pr.GetComments() + pr.GetReviewComments(),
		CreatedAt:     pr.GetCreatedAt().Time,
		UpdatedAt:     pr.GetUpdatedAt().Time,
	}

	// Fetch diff
	diff, err := c.fetchDiff(ctx, number)
	if err == nil {
		data.Diff = diff
	}

	// Fetch comments
	comments, err := c.fetchComments(ctx, number)
	if err == nil {
		data.Comments = comments
	}

	return data, nil
}

func (c *Client) fetchDiff(ctx context.Context, number int) (string, error) {
	raw, resp, err := c.gh.PullRequests.GetRaw(ctx, owner, repo, number, gh.RawOptions{Type: gh.Diff})
	if err != nil {
		// Fallback: try to get the diff via the diff URL
		diffURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d.diff", owner, repo, number)
		req, err2 := http.NewRequestWithContext(ctx, "GET", diffURL, nil)
		if err2 != nil {
			return "", fmt.Errorf("get diff: %w", err)
		}
		r, err2 := http.DefaultClient.Do(req)
		if err2 != nil {
			return "", fmt.Errorf("get diff: %w", err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
	} else if resp != nil {
		_ = resp.Body.Close()
	}

	return truncateDiff(raw, c.maxDiffLines), nil
}

func truncateDiff(diff string, maxLines int) string {
	lines := strings.Split(diff, "\n")
	if len(lines) <= maxLines {
		return diff
	}
	truncated := strings.Join(lines[:maxLines], "\n")
	return truncated + fmt.Sprintf("\n\n... [truncated: %d/%d lines shown]", maxLines, len(lines))
}

func (c *Client) fetchComments(ctx context.Context, number int) ([]Comment, error) {
	// Get issue comments (general discussion)
	issueComments, _, err := c.gh.Issues.ListComments(ctx, owner, repo, number, &gh.IssueListCommentsOptions{
		Sort:      gh.String("created"),
		Direction: gh.String("desc"),
		ListOptions: gh.ListOptions{PerPage: 20},
	})
	if err != nil {
		return nil, err
	}

	var comments []Comment
	for _, c := range issueComments {
		comments = append(comments, Comment{
			Author:    c.GetUser().GetLogin(),
			Body:      c.GetBody(),
			CreatedAt: c.GetCreatedAt().Time,
		})
	}

	// Get review comments (inline code comments)
	reviewComments, _, err := c.gh.PullRequests.ListComments(ctx, owner, repo, number, &gh.PullRequestListCommentsOptions{
		Sort:      "created",
		Direction: "desc",
		ListOptions: gh.ListOptions{PerPage: 20},
	})
	if err != nil {
		return nil, err
	}

	for _, c := range reviewComments {
		comments = append(comments, Comment{
			Author:    c.GetUser().GetLogin(),
			Body:      c.GetBody(),
			CreatedAt: c.GetCreatedAt().Time,
		})
	}

	return comments, nil
}
