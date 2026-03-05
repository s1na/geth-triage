# Geth Triage API

Base URL: `http://localhost:8080`

## Authentication

All endpoints require a static API key via header:

```
X-API-Key: <your-api-key>
```

Returns `401` with `{"error":"unauthorized"}` if missing or invalid.

---

## Endpoints

### GET /api/v1/health

Service health check.

**Response** `200`
```json
{
  "status": "ok",
  "time": "2025-03-04T19:17:00Z",
  "last_poll_time": "2025-03-04T15:00:00Z",
  "pending_batches": 0
}
```

| Field | Type | Description |
|-------|------|-------------|
| `status` | string | Always `"ok"` if service is running |
| `time` | string | Current server time (UTC, RFC 3339) |
| `last_poll_time` | string | When GitHub was last polled (empty string if never) |
| `pending_batches` | number | Count of in-progress Anthropic batch jobs |

---

### GET /api/v1/prs

List open PRs with their latest analysis. Supports filtering, sorting, and pagination.

**Query Parameters**

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `category` | string | — | Filter by category: `closeable`, `high-priority`, `duplicate`, `needs-attention`, `normal` |
| `author` | string | — | Filter by GitHub username |
| `min_confidence` | float | — | Minimum confidence score (0.0–1.0) |
| `max_confidence` | float | — | Maximum confidence score (0.0–1.0) |
| `sort` | string | `number` | Sort by: `number`, `updated_at`, `confidence`, `category` |
| `order` | string | `desc` | Sort direction: `asc`, `desc` |
| `limit` | int | 50 | Results per page (max 200) |
| `offset` | int | 0 | Pagination offset |

**Example**
```
GET /api/v1/prs?category=closeable&sort=confidence&order=desc&limit=20
```

**Response** `200`
```json
{
  "prs": [
    {
      "number": 33702,
      "title": "apitypes: fix truncation of opening parenthesis",
      "author": "conorpp",
      "state": "open",
      "labels": ["bug"],
      "head_sha": "abc123...",
      "additions": 3,
      "deletions": 1,
      "comments_count": 0,
      "created_at": "2025-03-01T10:00:00Z",
      "updated_at": "2025-03-02T15:30:00Z",
      "fetched_at": "2025-03-04T19:17:00Z",
      "category": "high-priority",
      "confidence": 0.85,
      "explanation": "Bug fix in EIP-712 typed data encoding...",
      "analyzed_at": "2025-03-04T19:17:05Z"
    }
  ],
  "total": 142,
  "limit": 50,
  "offset": 0
}
```

| Field | Type | Description |
|-------|------|-------------|
| `prs` | array | List of PRs with latest analysis |
| `prs[].labels` | array | GitHub label names |
| `prs[].category` | string\|null | Analysis category (null if not yet analyzed) |
| `prs[].confidence` | number\|null | Confidence score 0.0–1.0 (null if not analyzed) |
| `prs[].explanation` | string\|null | Analysis explanation (null if not analyzed) |
| `prs[].analyzed_at` | string\|null | When the latest analysis was created (null if not analyzed) |
| `total` | number | Total matching PRs (for pagination) |
| `limit` | number | Effective limit used |
| `offset` | number | Offset used |

---

### GET /api/v1/prs/{number}

Get a single PR with its full analysis history.

**Response** `200`
```json
{
  "pr": {
    "number": 33702,
    "title": "apitypes: fix truncation of opening parenthesis",
    "author": "conorpp",
    "state": "open",
    "labels": "[]",
    "head_sha": "abc123...",
    "additions": 3,
    "deletions": 1,
    "comments_count": 0,
    "created_at": "2025-03-01T10:00:00Z",
    "updated_at": "2025-03-02T15:30:00Z",
    "fetched_at": "2025-03-04T19:17:00Z"
  },
  "analyses": [
    {
      "id": 5,
      "pr_number": 33702,
      "category": "high-priority",
      "confidence": 0.85,
      "explanation": "Bug fix in EIP-712 typed data encoding...",
      "related_prs": [],
      "model": "claude-sonnet-4-20250514",
      "prompt_version": "v1",
      "input_tokens": 885,
      "output_tokens": 99,
      "created_at": "2025-03-04T19:17:05Z"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `pr` | object | PR metadata |
| `analyses` | array | All analyses for this PR, newest first |
| `analyses[].related_prs` | array | Related PR numbers (e.g. `[33500, 33412]`) |
| `analyses[].model` | string | Claude model used |
| `analyses[].prompt_version` | string | Prompt version (for tracking prompt changes) |
| `analyses[].input_tokens` | number | Tokens sent to Claude |
| `analyses[].output_tokens` | number | Tokens received from Claude |

**Response** `404`
```json
{"error": "PR not found"}
```

---

### POST /api/v1/prs/{number}/analyze

Trigger a fresh analysis for a specific PR. Fetches latest PR data from GitHub and analyzes synchronously via Claude. Takes 3–10 seconds depending on diff size.

**Request** — No body required.

**Response** `200`
```json
{
  "id": 6,
  "pr_number": 33702,
  "category": "high-priority",
  "confidence": 0.85,
  "explanation": "Bug fix in EIP-712 typed data encoding...",
  "related_prs": [],
  "model": "claude-sonnet-4-20250514",
  "prompt_version": "v1",
  "input_tokens": 885,
  "output_tokens": 99,
  "created_at": "2025-03-04T19:20:00Z"
}
```

**Response** `502`
```json
{"error": "failed to fetch PR from GitHub"}
```

**Response** `500`
```json
{"error": "analysis failed"}
```

---

### GET /api/v1/stats

Aggregate statistics across all analyzed open PRs.

**Response** `200`
```json
{
  "total_prs": 142,
  "analyzed_prs": 130,
  "category_counts": {
    "closeable": 24,
    "high-priority": 8,
    "duplicate": 5,
    "needs-attention": 41,
    "normal": 52
  },
  "avg_confidence": 0.78
}
```

| Field | Type | Description |
|-------|------|-------------|
| `total_prs` | number | Total open PRs in the database |
| `analyzed_prs` | number | PRs that have at least one analysis |
| `category_counts` | object | Count of PRs per category (latest analysis only) |
| `avg_confidence` | number | Weighted average confidence across all categories |

---

## Categories

| Category | Description |
|----------|-------------|
| `closeable` | Spam, AI slop, broken, abandoned, cosmetic-only |
| `high-priority` | Security fixes, consensus-critical, known contributors, critical bugs |
| `duplicate` | Overlaps with another open PR |
| `needs-attention` | Meaningful changes that need review but aren't urgent |
| `normal` | Default — minor improvements, WIP, unclear scope |

## Error Format

All errors return:
```json
{"error": "description of what went wrong"}
```

## Notes

- The service polls GitHub every 4 hours and automatically analyzes new/changed PRs
- Only open PRs are tracked; closed/merged PRs are retained but excluded from list queries
- The `/analyze` endpoint is rate-limited by Claude API quotas, not by the service itself
- Analysis history is append-only — re-analyzing a PR adds a new entry, doesn't overwrite
