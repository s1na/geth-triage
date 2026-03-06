package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const graphqlEndpoint = "https://api.github.com/graphql"

// GraphQL query for listing open PRs with cursor-based pagination.
const listPRsQuery = `
query($owner: String!, $repo: String!, $cursor: String) {
  repository(owner: $owner, name: $repo) {
    pullRequests(states: OPEN, first: 100, after: $cursor, orderBy: {field: UPDATED_AT, direction: DESC}) {
      pageInfo {
        hasNextPage
        endCursor
      }
      nodes {
        number
        title
        author { login }
        state
        labels(first: 20) { nodes { name } }
        headRefOid
        additions
        deletions
        comments { totalCount }
        reviews { totalCount }
        createdAt
        updatedAt
      }
    }
  }
}
`

// GraphQL query for fetching a single PR's metadata.
const prDetailQuery = `
query($owner: String!, $repo: String!, $number: Int!) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      number
      title
      author { login }
      state
      labels(first: 20) { nodes { name } }
      headRefOid
      additions
      deletions
      comments { totalCount }
      reviews { totalCount }
      createdAt
      updatedAt
    }
  }
}
`

// Response types for JSON unmarshaling.

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

type gqlError struct {
	Message string `json:"message"`
}

// List PRs response types.

type gqlListData struct {
	Repository struct {
		PullRequests struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []gqlPRNode `json:"nodes"`
		} `json:"pullRequests"`
	} `json:"repository"`
}

type gqlPRNode struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	Author     *gqlActor  `json:"author"`
	State      string     `json:"state"`
	Labels     gqlLabels  `json:"labels"`
	HeadRefOid string     `json:"headRefOid"`
	Additions  int        `json:"additions"`
	Deletions  int        `json:"deletions"`
	Comments   gqlCount   `json:"comments"`
	Reviews    gqlCount   `json:"reviews"`
	CreatedAt  string     `json:"createdAt"`
	UpdatedAt  string     `json:"updatedAt"`
}

type gqlActor struct {
	Login string `json:"login"`
}

type gqlLabels struct {
	Nodes []struct {
		Name string `json:"name"`
	} `json:"nodes"`
}

type gqlCount struct {
	TotalCount int `json:"totalCount"`
}

// Detail PR response types.

type gqlDetailData struct {
	Repository struct {
		PullRequest gqlPRNode `json:"pullRequest"`
	} `json:"repository"`
}

// graphql sends a GraphQL request and unmarshals the data portion into dest.
func (c *Client) graphql(ctx context.Context, query string, variables map[string]any, dest any) error {
	body, err := json.Marshal(gqlRequest{Query: query, Variables: variables})
	if err != nil {
		return fmt.Errorf("marshal graphql request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", graphqlEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create graphql request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("graphql request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read graphql response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("graphql HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var gqlResp gqlResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return fmt.Errorf("unmarshal graphql response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return fmt.Errorf("graphql error: %s", gqlResp.Errors[0].Message)
	}

	if err := json.Unmarshal(gqlResp.Data, dest); err != nil {
		return fmt.Errorf("unmarshal graphql data: %w", err)
	}

	return nil
}
