# geth-triage

AI-powered PR triage service for [ethereum/go-ethereum](https://github.com/ethereum/go-ethereum). Uses Claude to analyze open PRs and categorize them to help maintainers prioritize reviews.

## Categories

| Category | Description |
|----------|-------------|
| **closeable** | Spam, AI-generated slop, broken, abandoned, cosmetic-only |
| **high-priority** | Security fixes, consensus-critical changes, known contributors |
| **duplicate** | Overlaps with another open PR |
| **needs-attention** | Meaningful changes that need review but aren't urgent |
| **normal** | Minor improvements, WIP, unclear scope |

## Quick Start

```bash
cp .env.example .env
# Fill in GITHUB_TOKEN, ANTHROPIC_API_KEY, and API_KEY

# Test on a specific PR
go build -o test-pr ./cmd/test
./test-pr 33702

# Run the service
go build -o geth-triage .
./geth-triage
```

## Architecture

- **GitHub poller** — fetches open PRs every 4 hours, detects new/changed PRs, marks closed PRs
- **Anthropic analyzer** — Batch API for bulk (>10 PRs), Messages API for individual analysis
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
| `ANTHROPIC_API_KEY` | required | Anthropic API key |
| `API_KEY` | required | Static key for REST API auth |
| `POLL_INTERVAL` | `4h` | How often to poll GitHub |
| `BATCH_POLL_INTERVAL` | `5m` | How often to check pending batches |
| `LISTEN_ADDR` | `:8443` | HTTPS listen address |
| `HTTP_LISTEN_ADDR` | `:8080` | HTTP listen address |
| `DB_PATH` | `/data/geth-triage.db` | SQLite database path |
| `ANTHROPIC_MODEL` | `claude-sonnet-4-20250514` | Model to use |
| `BATCH_THRESHOLD` | `10` | Use Batch API when analyzing more than N PRs |
| `MAX_DIFF_LINES` | `500` | Max diff lines sent to Claude |
