package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	"github.com/s1na/geth-triage/internal/analyzer"
	ghclient "github.com/s1na/geth-triage/internal/github"
	"github.com/s1na/geth-triage/internal/store"
)

type Handlers struct {
	store        *store.Store
	analyzer     *analyzer.Orchestrator
	github       *ghclient.Client
	log          zerolog.Logger
	pollInterval time.Duration
}

func NewHandlers(s *store.Store, az *analyzer.Orchestrator, gh *ghclient.Client, pollInterval time.Duration, log zerolog.Logger) *Handlers {
	return &Handlers{store: s, analyzer: az, github: gh, pollInterval: pollInterval, log: log}
}

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	lastPoll, _ := h.store.GetState(r.Context(), "last_poll_time")

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"time":           time.Now().UTC(),
		"last_poll_time": lastPoll,
	})
}

func (h *Handlers) ListPRs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	minConf, _ := strconv.ParseFloat(q.Get("min_confidence"), 64)
	maxConf, _ := strconv.ParseFloat(q.Get("max_confidence"), 64)

	params := store.ListPRsParams{
		Category:  q.Get("category"),
		MinConf:   minConf,
		MaxConf:   maxConf,
		Author:    q.Get("author"),
		SortBy:    q.Get("sort"),
		SortOrder: q.Get("order"),
		Limit:     limit,
		Offset:    offset,
	}

	prs, total, err := h.store.ListPRs(r.Context(), params)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to list PRs")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"prs":    prs,
		"total":  total,
		"limit":  params.Limit,
		"offset": offset,
	})
}

func (h *Handlers) GetPR(w http.ResponseWriter, r *http.Request) {
	numberStr := r.PathValue("number")
	number, err := strconv.Atoi(numberStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid PR number"})
		return
	}

	pr, err := h.store.GetPR(r.Context(), number)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if pr == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "PR not found"})
		return
	}

	history, err := h.store.AnalysisHistory(r.Context(), number)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"pr":       pr,
		"analyses": history,
	})
}

func (h *Handlers) AnalyzePR(w http.ResponseWriter, r *http.Request) {
	numberStr := r.PathValue("number")
	number, err := strconv.Atoi(numberStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid PR number"})
		return
	}

	// Fetch fresh PR data from GitHub
	prData, err := h.github.FetchPR(r.Context(), number)
	if err != nil {
		h.log.Error().Err(err).Int("pr", number).Msg("failed to fetch PR from GitHub")
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "failed to fetch PR from GitHub"})
		return
	}

	// Upsert PR in store
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
	if err := h.store.UpsertPR(r.Context(), storePR); err != nil {
		h.log.Error().Err(err).Int("pr", number).Msg("failed to upsert PR")
	}

	// Analyze via Messages API
	analysis, err := h.analyzer.AnalyzeSingle(r.Context(), *prData)
	if err != nil {
		h.log.Error().Err(err).Int("pr", number).Msg("failed to analyze PR")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "analysis failed"})
		return
	}

	writeJSON(w, http.StatusOK, analysis)
}

func (h *Handlers) Stats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.store.GetStats(r.Context(), h.pollInterval)
	if err != nil {
		h.log.Error().Err(err).Msg("failed to get stats")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
