# geth-triage

AI-powered PR triage service for [ethereum/go-ethereum](https://github.com/ethereum/go-ethereum). Uses Claude to analyze open PRs and categorize them to help maintainers prioritize reviews.

## Categories

| Category | Description |
|----------|-------------|
| **closeable** | Spam, AI-generated slop, broken, abandoned, cosmetic-only |
| **high-priority** | Security fixes, critical bugs, known contributors/maintainers |
| **duplicate** | Overlaps with another open PR |
| **mergeable** | Reviewed/approved by maintainers but not yet merged |
| **normal** | Minor improvements, WIP, unclear scope |

## Quick Start

```bash
cp .env.example .env
# Fill in GITHUB_TOKEN, ANTHROPIC_API_KEY, and API_KEY

# Test on a specific PR
go run ./cmd/test 33702

# Run the service
go build -o geth-triage .
./geth-triage
```

## Architecture

- **GitHub poller** — fetches open PRs via GraphQL every hour, detects new/changed PRs
- **Analyzers** — two modes:
  - `api` — Anthropic Messages API (supports batch for bulk analysis)
  - `claudecode` — shells out to Claude Code CLI with a local go-ethereum clone for deep codebase exploration
- **Usage throttling** — queries Claude OAuth usage API, pauses when session utilization exceeds threshold, resumes after reset
- **REST API** — serves results to a frontend, authenticated via `X-API-Key` header
- **SQLite** — persistent storage using `modernc.org/sqlite` (pure Go, no CGo)

## API

See [API.md](API.md) for full endpoint documentation.

```
GET  /api/v1/health              — Service status
GET  /api/v1/prs                 — List PRs (filterable, sortable, paginated)
GET  /api/v1/prs/{number}        — Single PR with analysis history
POST /api/v1/prs/{number}/analyze — Trigger re-analysis
GET  /api/v1/stats               — Aggregate statistics
```

## Docker

```bash
# Run the service
docker compose up -d --build

# Initial bulk analysis of all open PRs
docker compose --profile init run --rm batch-init
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `GITHUB_TOKEN` | required | GitHub API token |
| `ANTHROPIC_API_KEY` | — | Anthropic API key (required for `api` mode) |
| `API_KEY` | required | Static key for REST API auth |
| `ANALYZER_TYPE` | `api` | `api` or `claudecode` |
| `POLL_INTERVAL` | `1h` | How often to poll GitHub |
| `USAGE_THRESHOLD` | `80` | Pause analysis when Claude session utilization exceeds this % (0 to disable) |
| `LISTEN_ADDR` | `:8443` | HTTPS listen address |
| `HTTP_LISTEN_ADDR` | `:8080` | HTTP listen address |
| `DB_PATH` | `/data/geth-triage.db` | SQLite database path |
| `ANTHROPIC_MODEL` | `claude-sonnet-4-20250514` | Model for API mode |
| `CLAUDE_CODE_MODEL` | `sonnet` | Model for Claude Code mode |
| `CLAUDE_CODE_MAX_BUDGET` | `0.50` | Max USD per PR analysis (Claude Code mode) |
| `CLAUDE_CODE_TIMEOUT` | `5m` | Timeout per PR analysis (Claude Code mode) |
| `BATCH_THRESHOLD` | `10` | Use Batch API when analyzing more than N PRs |
| `MAX_DIFF_LINES` | `500` | Max diff lines sent to Claude (API mode) |
