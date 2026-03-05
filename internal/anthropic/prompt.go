package anthropic

import (
	"fmt"
	"strings"

	"github.com/sina-geth/geth-triage/internal/github"
)

const PromptVersion = "v1"

const systemPrompt = `You are an expert Go/Ethereum developer and open-source maintainer helping triage pull requests for the go-ethereum (geth) repository.

Your job is to analyze a PR and categorize it into one of these categories:

1. **closeable** — Should be closed. Reasons: spam, clearly broken, AI-generated slop with no value, duplicate of existing work, against project direction, abandoned with no response to feedback, trivial cosmetic-only changes with no functional value.

2. **high-priority** — Needs urgent maintainer attention. Reasons: security fixes, consensus-critical changes (core/vm, consensus/, core/state, core/rawdb), critical bug fixes, changes from known contributors/maintainers, performance improvements with benchmarks.

3. **duplicate** — Appears to duplicate or heavily overlap with another open PR. Note: only use this if you can identify specific related PRs.

4. **needs-attention** — Needs maintainer review but not urgent. Reasons: meaningful feature additions, well-structured refactoring, documentation improvements with substance, dependency updates, test improvements.

5. **normal** — Default category for PRs that don't clearly fit other categories. Minor improvements, work-in-progress, unclear scope.

## Geth-Specific Context

Consensus-critical paths (changes here = high-priority):
- core/vm/ — EVM implementation
- consensus/ — Consensus engines
- core/state/ — State trie management
- core/rawdb/ — Low-level database layer
- core/types/ — Transaction and block types
- params/config.go — Chain configuration

Known maintainer signals (higher trust):
- Authors who are members of the ethereum org
- PRs with maintainer approval reviews
- PRs referenced in EIPs or ethereum/EIPs

AI-generated PR signals (lower trust):
- Generic descriptions, boilerplate commit messages
- Changes that don't compile or have no tests
- Cosmetic-only changes across many files
- "Improve code quality" without specific motivation

## Response Format

Respond with a JSON object only, no markdown fences:
{
  "category": "<one of: closeable, high-priority, duplicate, needs-attention, normal>",
  "confidence": <float 0.0 to 1.0>,
  "explanation": "<2-3 sentences explaining your reasoning>",
  "related_prs": [<list of related PR numbers, if any>]
}`

func BuildUserPrompt(pr github.PRData) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("## PR #%d: %s\n\n", pr.Number, pr.Title))
	sb.WriteString(fmt.Sprintf("**Author:** %s\n", pr.Author))
	sb.WriteString(fmt.Sprintf("**Labels:** %s\n", strings.Join(pr.Labels, ", ")))
	sb.WriteString(fmt.Sprintf("**Additions:** %d | **Deletions:** %d\n", pr.Additions, pr.Deletions))
	sb.WriteString(fmt.Sprintf("**Comments:** %d\n", pr.CommentsCount))
	sb.WriteString(fmt.Sprintf("**Created:** %s | **Updated:** %s\n\n", pr.CreatedAt.Format("2006-01-02"), pr.UpdatedAt.Format("2006-01-02")))

	if len(pr.Comments) > 0 {
		sb.WriteString("### Recent Comments\n\n")
		// Show up to 10 most recent comments
		limit := 10
		if len(pr.Comments) < limit {
			limit = len(pr.Comments)
		}
		for _, c := range pr.Comments[:limit] {
			body := c.Body
			if len(body) > 500 {
				body = body[:500] + "..."
			}
			sb.WriteString(fmt.Sprintf("**%s** (%s):\n%s\n\n", c.Author, c.CreatedAt.Format("2006-01-02"), body))
		}
	}

	if pr.Diff != "" {
		sb.WriteString("### Diff\n\n```diff\n")
		sb.WriteString(pr.Diff)
		sb.WriteString("\n```\n")
	}

	return sb.String()
}
