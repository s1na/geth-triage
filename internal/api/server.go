package api

import (
	"net/http"

	"github.com/rs/zerolog"
	"github.com/s1na/geth-triage/internal/analyzer"
	ghclient "github.com/s1na/geth-triage/internal/github"
	"github.com/s1na/geth-triage/internal/store"
)

func NewServer(apiKey, webhookSecret string, s *store.Store, az *analyzer.Orchestrator, poller *ghclient.Poller, gh *ghclient.Client, log zerolog.Logger) http.Handler {
	h := NewHandlers(s, az, gh, log)
	wh := NewWebhookHandler(webhookSecret, poller, az, log)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", h.Health)
	mux.HandleFunc("GET /api/v1/prs", h.ListPRs)
	mux.HandleFunc("GET /api/v1/prs/{number}", h.GetPR)
	mux.HandleFunc("POST /api/v1/prs/{number}/analyze", h.AnalyzePR)
	mux.HandleFunc("GET /api/v1/stats", h.Stats)

	// Webhook endpoint sits outside API-key auth
	outer := http.NewServeMux()
	outer.HandleFunc("POST /api/v1/webhook/github", wh.Handle)
	outer.Handle("/", cors(apiKeyAuth(apiKey, mux)))

	return outer
}
