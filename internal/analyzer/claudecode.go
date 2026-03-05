package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/sina-geth/geth-triage/internal/github"
)

const claudeCodePromptVersion = "cc-v3"

const claudeCodeSystemPrompt = `You are an expert Go/Ethereum developer and open-source maintainer helping triage pull requests for the go-ethereum (geth) repository.

You have access to a local clone of the go-ethereum codebase. BEFORE making your judgment, you MUST actively explore the codebase to understand context:

1. Read the modified files to understand surrounding code and function signatures
2. Grep for usages of any modified functions, types, or constants to assess impact
3. Check for similar patterns elsewhere in the codebase
4. Use git log/blame on modified files to understand recent history if relevant
5. Assess correctness, edge cases, and code style consistency based on what you find

Reference specific files and functions you found in your explanation.

Categorize the PR into one of these categories:

1. **closeable** — Should be closed. Reasons: spam, clearly broken, AI-generated slop with no value, duplicate of existing work, abandoned with no response to feedback, trivial cosmetic-only changes with no functional value.

2. **high-priority** — Needs urgent maintainer attention. Reasons: security fixes, critical bug fixes, changes from known contributors/maintainers, performance improvements with significant value.

3. **duplicate** — Appears to duplicate or heavily overlap with another open PR. Note: only use this if you can identify specific related PRs.

4. **mergeable** — Has been reviewed and/or approved by maintainers but not yet merged. Use this when the PR has approving reviews or clear maintainer sign-off and appears ready to land.

5. **normal** — Default category for PRs that don't clearly fit other categories. Minor improvements, work-in-progress, unclear scope.

## Geth-Specific Context

Known team members and trusted contributors (GitHub usernames):

Current core team:
- fjl (Felix Lange) — networking, RLP, devp2p
- rjl493456442 (Gary Rong) — trie, state, snap sync
- MariusVanDerWijden (Marius van der Wijden) — consensus, testing, fuzzing
- s1na (Sina Mahmoodi) — tracing, state, JSON-RPC
- lightclient (Matt Garnett) — EVM, EIPs, consensus
- gballet (Guillaume Ballet) — verkle trees, EVM, witness
- jwasinger (Jared Wasinger) — EVM, precompiles
- zsfelfoldi (Zsolt Felföldi) — light client, les protocol
- cskiraly (Csaba Kiraly) — networking, portal
- healthykim — core improvements
- jrhea (Jonny Rhea) — networking

Regular trusted contributors:
- delweng (Delweng) — long-time contributor

Former maintainers / notable past contributors:
- karalabe (Péter Szilágyi) — former project lead
- holiman (Martin Holst Swende) — security, EVM, testing
- obscuren (Jeffrey Wilcke) — original co-creator
- Arachnid (Nick Johnson) — ENS, early core
- ligi — tooling, CI

PRs from these authors should be given higher trust. PRs with approving reviews from these authors are also higher signal.

AI-generated PR signals (lower trust):
- Generic descriptions, boilerplate commit messages
- Changes that don't compile or have no tests
- Cosmetic-only changes across many files
- "Improve code quality" without specific motivation

## CRITICAL: Output Format

Do NOT state your categorization in intermediate responses while using tools. Gather all evidence first, then provide your final categorization ONLY in your last response. Your final response will be automatically validated against a JSON schema — just provide the structured JSON object as your final answer.`

// analysisSchema is the JSON schema enforced via --json-schema for structured output.
var analysisSchema = func() string {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category": map[string]any{
				"type": "string",
				"enum": []string{"closeable", "high-priority", "duplicate", "mergeable", "normal"},
			},
			"confidence": map[string]any{
				"type": "number",
			},
			"explanation": map[string]any{
				"type": "string",
			},
			"related_prs": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "integer"},
			},
		},
		"required": []string{"category", "confidence", "explanation", "related_prs"},
	}
	b, _ := json.Marshal(schema)
	return string(b)
}()

// ClaudeCodeAnalyzer implements PRAnalyzer by shelling out to the Claude Code CLI
// running inside a local go-ethereum clone.
type ClaudeCodeAnalyzer struct {
	repoPath  string
	model     string
	maxBudget string
	timeout   time.Duration
	log       zerolog.Logger
}

func NewClaudeCodeAnalyzer(repoPath, model, maxBudget string, timeout time.Duration, log zerolog.Logger) *ClaudeCodeAnalyzer {
	return &ClaudeCodeAnalyzer{
		repoPath:  repoPath,
		model:     model,
		maxBudget: maxBudget,
		timeout:   timeout,
		log:       log,
	}
}

// EnsureRepo clones go-ethereum if missing, otherwise pulls latest.
func (c *ClaudeCodeAnalyzer) EnsureRepo(ctx context.Context) error {
	if _, err := os.Stat(c.repoPath); os.IsNotExist(err) {
		c.log.Info().Str("path", c.repoPath).Msg("cloning go-ethereum repository")
		cmd := exec.CommandContext(ctx, "git", "clone", "https://github.com/ethereum/go-ethereum.git", c.repoPath)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git clone: %w", err)
		}
		c.log.Info().Msg("clone complete")
		return nil
	}

	return c.resetRepo(ctx)
}

// resetRepo ensures the repo is on a clean master branch with latest upstream.
func (c *ClaudeCodeAnalyzer) resetRepo(ctx context.Context) error {
	c.log.Info().Str("path", c.repoPath).Msg("resetting go-ethereum repository")
	git := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", c.repoPath}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git %s: %w\n%s", args[0], err, out)
		}
		return nil
	}

	// Check for uncommitted changes (would indicate a previous agent left dirt)
	cmd := exec.CommandContext(ctx, "git", "-C", c.repoPath, "status", "--porcelain")
	out, err := cmd.Output()
	if err == nil && len(out) > 0 {
		c.log.Warn().Str("changes", string(out)).Msg("go-ethereum repo has uncommitted changes, resetting")
	}

	// Hard reset and checkout master
	if err := git("checkout", "master"); err != nil {
		return err
	}
	if err := git("reset", "--hard", "origin/master"); err != nil {
		return err
	}
	if err := git("clean", "-fd"); err != nil {
		return err
	}

	// Pull latest
	if err := git("pull", "--ff-only"); err != nil {
		c.log.Warn().Err(err).Msg("git pull failed, continuing with existing checkout")
	}

	return nil
}

// AnalyzePR implements PRAnalyzer by invoking claude --print with the PR data.
func (c *ClaudeCodeAnalyzer) AnalyzePR(ctx context.Context, pr github.PRData) (*AnalysisResult, error) {
	// Ensure clean working tree before each analysis
	if err := c.resetRepo(ctx); err != nil {
		return nil, fmt.Errorf("reset repo: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	userPrompt := buildClaudeCodeUserPrompt(pr)

	args := []string{
		"--print",
		"--output-format", "json",
		"--json-schema", analysisSchema,
		"--system-prompt", claudeCodeSystemPrompt,
		"--model", c.model,
		"--max-budget-usd", c.maxBudget,
		"--allowedTools", "Read Glob Grep Bash(git log:*) Bash(git blame:*) Bash(git show:*) Bash(gh:*) WebSearch WebFetch",
		"--no-session-persistence",
		"--dangerously-skip-permissions",
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = c.repoPath
	cmd.Stdin = strings.NewReader(userPrompt)
	cmd.Env = filterEnv(os.Environ(), "ANTHROPIC_API_KEY", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	c.log.Info().
		Int("pr", pr.Number).
		Str("model", c.model).
		Str("max_budget", c.maxBudget).
		Msg("invoking claude code CLI")

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	c.log.Info().
		Int("pr", pr.Number).
		Dur("elapsed", elapsed).
		Int("stdout_bytes", stdout.Len()).
		Int("stderr_bytes", stderr.Len()).
		Msg("claude code CLI finished")

	// --output-format json may write the envelope to stdout or stderr depending
	// on context. Check both, preferring whichever starts with '{'.
	var envelope []byte
	switch {
	case stdout.Len() > 0 && stdout.Bytes()[0] == '{':
		envelope = stdout.Bytes()
	case stderr.Len() > 0 && stderr.Bytes()[0] == '{':
		envelope = stderr.Bytes()
	default:
		if err != nil {
			return nil, fmt.Errorf("claude CLI error (exit=%v): %s", err, stderr.String())
		}
		return nil, fmt.Errorf("claude CLI produced no JSON output; stdout=%d bytes, stderr=%d bytes", stdout.Len(), stderr.Len())
	}

	return c.parseOutput(envelope)
}

// claudeCodeEnvelope is the JSON envelope returned by claude --output-format json (on stderr).
type claudeCodeEnvelope struct {
	Result           string              `json:"result"`
	StructuredOutput *claudeCodeResult   `json:"structured_output"`
	TotalCostUSD     float64             `json:"total_cost_usd"`
	NumTurns         int                 `json:"num_turns"`
	IsError          bool                `json:"is_error"`
	Subtype          string              `json:"subtype"`
	Usage            *claudeCodeUsage    `json:"usage"`
}

type claudeCodeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// claudeCodeResult is the structured analysis from the structured_output field.
type claudeCodeResult struct {
	Category    string  `json:"category"`
	Confidence  float64 `json:"confidence"`
	Explanation string  `json:"explanation"`
	RelatedPRs  []int   `json:"related_prs"`
}

func (c *ClaudeCodeAnalyzer) parseOutput(raw []byte) (*AnalysisResult, error) {
	var envelope claudeCodeEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("parse claude envelope: %w\nraw output: %s", err, string(raw))
	}

	if envelope.IsError {
		return nil, fmt.Errorf("claude code returned error: %s", envelope.Result)
	}

	var inputTokens, outputTokens int
	if envelope.Usage != nil {
		inputTokens = envelope.Usage.InputTokens
		outputTokens = envelope.Usage.OutputTokens
	}

	c.log.Info().
		Float64("cost_usd", envelope.TotalCostUSD).
		Int("num_turns", envelope.NumTurns).
		Int("input_tokens", inputTokens).
		Int("output_tokens", outputTokens).
		Msg("claude code usage")

	if envelope.StructuredOutput == nil {
		c.log.Error().
			Str("result", envelope.Result).
			Float64("cost_usd", envelope.TotalCostUSD).
			Int("num_turns", envelope.NumTurns).
			Msg("structured_output was null — model likely stated categorization in intermediate tool-use turn instead of final response")
		return nil, fmt.Errorf("no structured_output in response (cost=$%.2f, turns=%d)", envelope.TotalCostUSD, envelope.NumTurns)
	}
	result := envelope.StructuredOutput

	return &AnalysisResult{
		Category:      result.Category,
		Confidence:    result.Confidence,
		Explanation:   result.Explanation,
		RelatedPRs:    result.RelatedPRs,
		Model:         "claude-code:" + c.model,
		PromptVersion: claudeCodePromptVersion,
		InputTokens:   inputTokens,
		OutputTokens:  outputTokens,
	}, nil
}

func buildClaudeCodeUserPrompt(pr github.PRData) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Analyze this go-ethereum pull request.\n\n"))
	sb.WriteString(fmt.Sprintf("## PR #%d: %s\n\n", pr.Number, pr.Title))
	sb.WriteString(fmt.Sprintf("**Author:** %s\n", pr.Author))
	sb.WriteString(fmt.Sprintf("**Labels:** %s\n", strings.Join(pr.Labels, ", ")))
	sb.WriteString(fmt.Sprintf("**Additions:** %d | **Deletions:** %d\n", pr.Additions, pr.Deletions))
	sb.WriteString(fmt.Sprintf("**Comments:** %d\n", pr.CommentsCount))
	sb.WriteString(fmt.Sprintf("**Created:** %s | **Updated:** %s\n\n", pr.CreatedAt.Format("2006-01-02"), pr.UpdatedAt.Format("2006-01-02")))

	sb.WriteString("Start by fetching the PR diff and comments using the gh CLI:\n")
	sb.WriteString(fmt.Sprintf("- `gh pr diff %d --repo ethereum/go-ethereum`\n", pr.Number))
	if pr.CommentsCount > 0 {
		sb.WriteString(fmt.Sprintf("- `gh pr view %d --repo ethereum/go-ethereum --json comments,reviews`\n", pr.Number))
	}
	sb.WriteString("\nThen explore the codebase to understand the context of the changes. Read modified files, grep for usages, and check related code paths.")

	return sb.String()
}

// filterEnv returns a copy of env with the named variables removed.
func filterEnv(env []string, keys ...string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		skip := false
		for _, key := range keys {
			if strings.HasPrefix(e, key+"=") {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, e)
		}
	}
	return out
}
