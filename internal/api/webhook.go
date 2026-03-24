package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"context"
	"io"
	"net/http"

	"github.com/rs/zerolog"
	"github.com/s1na/geth-triage/internal/analyzer"
	ghclient "github.com/s1na/geth-triage/internal/github"
)

type WebhookHandler struct {
	secret string
	poller *ghclient.Poller
	az     *analyzer.Orchestrator
	log    zerolog.Logger
}

func NewWebhookHandler(secret string, poller *ghclient.Poller, az *analyzer.Orchestrator, log zerolog.Logger) *WebhookHandler {
	return &WebhookHandler{secret: secret, poller: poller, az: az, log: log}
}

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
	case "pull_request_review", "issue_comment":
		// Trigger metadata sync to update comment/review counts.
		go wh.syncMetadata()
	}

	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

type prEvent struct {
	Action string `json:"action"`
	Number int    `json:"number"`
}

func (wh *WebhookHandler) handlePullRequest(body []byte) {
	var ev prEvent
	json.Unmarshal(body, &ev)

	switch ev.Action {
	case "opened", "reopened", "synchronize", "closed",
		"labeled", "unlabeled", "assigned", "unassigned":
		go wh.syncMetadata()
	}
}

func (wh *WebhookHandler) syncMetadata() {
	ctx := context.Background()
	result, err := wh.poller.Poll(ctx)
	if err != nil {
		wh.log.Error().Err(err).Msg("webhook-triggered metadata sync failed")
		return
	}
	if len(result.NewPRs) > 0 {
		wh.az.Enqueue(result.NewPRs...)
		wh.log.Info().Int("count", len(result.NewPRs)).Msg("webhook: enqueued new PRs for analysis")
	}
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
