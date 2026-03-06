package api

import (
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"github.com/sina-geth/geth-triage/internal/analyzer"
	ghclient "github.com/sina-geth/geth-triage/internal/github"
	"github.com/sina-geth/geth-triage/internal/store"
)

func NewServer(apiKey string, s *store.Store, az *analyzer.Orchestrator, gh *ghclient.Client, pollInterval time.Duration, log zerolog.Logger) http.Handler {
	h := NewHandlers(s, az, gh, pollInterval, log)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", h.Health)
	mux.HandleFunc("GET /api/v1/prs", h.ListPRs)
	mux.HandleFunc("GET /api/v1/prs/{number}", h.GetPR)
	mux.HandleFunc("POST /api/v1/prs/{number}/analyze", h.AnalyzePR)
	mux.HandleFunc("GET /api/v1/stats", h.Stats)

	return cors(apiKeyAuth(apiKey, mux))
}
