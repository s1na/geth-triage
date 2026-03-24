package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"github.com/s1na/geth-triage/internal/analyzer"
	ghclient "github.com/s1na/geth-triage/internal/github"
	"github.com/s1na/geth-triage/internal/store"
)

// WebhookHandler handles GitHub webhook events and performs targeted updates
// instead of a full poll.
type WebhookHandler struct {
	secret string
	store  *store.Store
	gh     *ghclient.Client
	az     *analyzer.Orchestrator
	log    zerolog.Logger
}

func NewWebhookHandler(secret string, s *store.Store, gh *ghclient.Client, az *analyzer.Orchestrator, log zerolog.Logger) *WebhookHandler {
	return &WebhookHandler{secret: secret, store: s, gh: gh, az: az, log: log}
}

// --- GitHub webhook payload structs ---

type webhookPRUser struct {
	Login string `json:"login"`
}

type webhookPRLabel struct {
	Name string `json:"name"`
}

type webhookPRHead struct {
	SHA string `json:"sha"`
}

type webhookPR struct {
	Number         int              `json:"number"`
	Title          string           `json:"title"`
	User           webhookPRUser    `json:"user"`
	State          string           `json:"state"`
	Labels         []webhookPRLabel `json:"labels"`
	Head           webhookPRHead    `json:"head"`
	Additions      int              `json:"additions"`
	Deletions      int              `json:"deletions"`
	Comments       int              `json:"comments"`
	ReviewComments int              `json:"review_comments"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
	Merged         bool             `json:"merged"`
}

type pullRequestEvent struct {
	Action      string    `json:"action"`
	Number      int       `json:"number"`
	PullRequest webhookPR `json:"pull_request"`
}

type pullRequestReviewEvent struct {
	Action      string    `json:"action"`
	PullRequest webhookPR `json:"pull_request"`
}

type issueCommentEvent struct {
	Action string `json:"action"`
	Issue  struct {
		Number           int `json:"number"`
		PullRequestField *struct {
			URL string `json:"url"`
		} `json:"pull_request"`
	} `json:"issue"`
}

// --- Conversion helpers ---

// prFromWebhook converts a webhook PR payload into a store.PullRequest and a ghclient.PRData.
func prFromWebhook(wpr webhookPR) (*store.PullRequest, ghclient.PRData) {
	labels := make([]string, len(wpr.Labels))
	for i, l := range wpr.Labels {
		labels[i] = l.Name
	}
	labelsJSON, _ := json.Marshal(labels)

	state := wpr.State
	if wpr.Merged {
		state = "closed"
	}

	storePR := &store.PullRequest{
		Number:        wpr.Number,
		Title:         wpr.Title,
		Author:        wpr.User.Login,
		State:         state,
		Labels:        labelsJSON,
		HeadSHA:       wpr.Head.SHA,
		Additions:     wpr.Additions,
		Deletions:     wpr.Deletions,
		CommentsCount: wpr.Comments + wpr.ReviewComments,
		CreatedAt:     wpr.CreatedAt,
		UpdatedAt:     wpr.UpdatedAt,
		FetchedAt:     time.Now().UTC(),
	}

	prData := ghclient.PRData{
		Number:        wpr.Number,
		Title:         wpr.Title,
		Author:        wpr.User.Login,
		State:         state,
		Labels:        labels,
		HeadSHA:       wpr.Head.SHA,
		Additions:     wpr.Additions,
		Deletions:     wpr.Deletions,
		CommentsCount: wpr.Comments + wpr.ReviewComments,
		CreatedAt:     wpr.CreatedAt,
		UpdatedAt:     wpr.UpdatedAt,
	}

	return storePR, prData
}

// --- Handler ---

func (wh *WebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if wh.secret == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "webhook not configured"})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}

	sig := r.Header.Get("X-Hub-Signature-256")
	if !verifySignature(wh.secret, body, sig) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid signature"})
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	wh.log.Info().Str("event", event).Msg("received GitHub webhook")

	switch event {
	case "pull_request":
		wh.handlePullRequest(body)
	case "pull_request_review":
		wh.handlePullRequestReview(body)
	case "issue_comment":
		wh.handleIssueComment(body)
	}

	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (wh *WebhookHandler) handlePullRequest(body []byte) {
	var ev pullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		wh.log.Error().Err(err).Msg("webhook: failed to parse pull_request event")
		return
	}

	storePR, prData := prFromWebhook(ev.PullRequest)
	log := wh.log.With().Int("pr", ev.Number).Str("action", ev.Action).Logger()

	switch ev.Action {
	case "opened", "reopened", "synchronize":
		// Upsert and enqueue for analysis.
		go func() {
			ctx := context.Background()
			if err := wh.store.UpsertPR(ctx, storePR); err != nil {
				log.Error().Err(err).Msg("webhook: failed to upsert PR")
				return
			}
			pos, added := wh.az.EnqueueOne(prData)
			log.Info().Int("position", pos).Bool("added", added).Msg("webhook: upserted and enqueued PR")
		}()

	case "closed":
		// Upsert with closed state, do NOT enqueue for analysis.
		go func() {
			ctx := context.Background()
			if err := wh.store.UpsertPR(ctx, storePR); err != nil {
				log.Error().Err(err).Msg("webhook: failed to upsert closed PR")
				return
			}
			log.Info().Msg("webhook: upserted closed PR")
		}()

	case "labeled", "unlabeled", "edited":
		// Upsert only (labels or title changed).
		go func() {
			ctx := context.Background()
			if err := wh.store.UpsertPR(ctx, storePR); err != nil {
				log.Error().Err(err).Msg("webhook: failed to upsert PR")
				return
			}
			log.Info().Msg("webhook: upserted PR metadata")
		}()

	default:
		log.Debug().Msg("webhook: ignoring pull_request action")
	}
}

func (wh *WebhookHandler) handlePullRequestReview(body []byte) {
	var ev pullRequestReviewEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		wh.log.Error().Err(err).Msg("webhook: failed to parse pull_request_review event")
		return
	}

	number := ev.PullRequest.Number
	log := wh.log.With().Int("pr", number).Logger()

	go func() {
		ctx := context.Background()
		wh.fetchAndUpsert(ctx, number, log)
	}()
}

func (wh *WebhookHandler) handleIssueComment(body []byte) {
	var ev issueCommentEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		wh.log.Error().Err(err).Msg("webhook: failed to parse issue_comment event")
		return
	}

	// Only handle comments on pull requests (not regular issues).
	if ev.Issue.PullRequestField == nil {
		return
	}

	number := ev.Issue.Number
	log := wh.log.With().Int("pr", number).Logger()

	go func() {
		ctx := context.Background()
		wh.fetchAndUpsert(ctx, number, log)
	}()
}

// fetchAndUpsert fetches a single PR from GitHub and upserts it into the store.
// Used for events that don't include full PR data (reviews, comments).
func (wh *WebhookHandler) fetchAndUpsert(ctx context.Context, number int, log zerolog.Logger) {
	prData, err := wh.gh.FetchPR(ctx, number)
	if err != nil {
		log.Error().Err(err).Msg("webhook: failed to fetch PR from GitHub")
		return
	}

	labelsJSON, _ := json.Marshal(prData.Labels)
	storePR := &store.PullRequest{
		Number:        prData.Number,
		Title:         prData.Title,
		Author:        prData.Author,
		State:         prData.State,
		Labels:        labelsJSON,
		HeadSHA:       prData.HeadSHA,
		Additions:     prData.Additions,
		Deletions:     prData.Deletions,
		CommentsCount: prData.CommentsCount,
		CreatedAt:     prData.CreatedAt,
		UpdatedAt:     prData.UpdatedAt,
		FetchedAt:     time.Now().UTC(),
	}

	if err := wh.store.UpsertPR(ctx, storePR); err != nil {
		log.Error().Err(err).Msg("webhook: failed to upsert PR after fetch")
		return
	}
	log.Info().Msg("webhook: fetched and upserted PR")
}

func verifySignature(secret string, body []byte, signature string) bool {
	if len(signature) < 8 || signature[:7] != "sha256=" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature[7:]), []byte(expected))
}
