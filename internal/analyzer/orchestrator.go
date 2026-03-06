package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/s1na/geth-triage/internal/github"
	"github.com/s1na/geth-triage/internal/store"
)

func NewOrchestrator(a *ClaudeCodeAnalyzer, s *store.Store, log zerolog.Logger, opts ...OrchestratorOption) *Orchestrator {
	o := &Orchestrator{
		analyzer: a,
		store:    s,
		items:    make(map[int]github.PRData),
		notify:   make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(o)
	}
	o.log = log
	return o
}

// Enqueue adds PRs to the analysis queue. Existing entries are updated with newer data,
// preserving their position in the queue.
func (o *Orchestrator) Enqueue(prs ...github.PRData) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, pr := range prs {
		if _, exists := o.items[pr.Number]; !exists {
			o.order = append(o.order, pr.Number)
		}
		o.items[pr.Number] = pr
	}
	select {
	case o.notify <- struct{}{}:
	default:
	}
}

// EnqueueOne adds a single PR to the queue and returns its position (1-indexed).
// If the PR is already queued with the same HEAD SHA, added is false.
func (o *Orchestrator) EnqueueOne(pr github.PRData) (position int, added bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	existing, exists := o.items[pr.Number]
	if exists && existing.HeadSHA == pr.HeadSHA {
		for i, num := range o.order {
			if num == pr.Number {
				return i + 1, false
			}
		}
	}

	if !exists {
		o.order = append(o.order, pr.Number)
	}
	o.items[pr.Number] = pr

	pos := 0
	for i, num := range o.order {
		if num == pr.Number {
			pos = i + 1
			break
		}
	}

	select {
	case o.notify <- struct{}{}:
	default:
	}

	return pos, true
}

// QueueLen returns the number of PRs waiting for analysis.
func (o *Orchestrator) QueueLen() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.order)
}

func (o *Orchestrator) dequeue() (github.PRData, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.order) == 0 {
		return github.PRData{}, false
	}
	num := o.order[0]
	o.order = o.order[1:]
	pr := o.items[num]
	delete(o.items, num)
	return pr, true
}

// Run starts the single analysis worker. Blocks until ctx is cancelled.
func (o *Orchestrator) Run(ctx context.Context) {
	o.log.Info().Msg("analysis worker started")
	for {
		pr, ok := o.dequeue()
		if !ok {
			select {
			case <-ctx.Done():
				o.log.Info().Msg("analysis worker stopped")
				return
			case <-o.notify:
				continue
			}
		}

		if err := o.waitForUsage(ctx); err != nil {
			o.log.Warn().Err(err).Msg("usage wait cancelled, worker stopping")
			return
		}

		if _, err := o.analyzeSingle(ctx, pr); err != nil {
			o.log.Error().Err(err).Int("pr", pr.Number).Msg("failed to analyze PR")
			continue
		}
		o.log.Info().Int("pr", pr.Number).Int("queued", o.QueueLen()).Msg("analyzed PR")
	}
}

// AnalyzePending enqueues PRs from the database that still need analysis.
// Called on startup to resume work interrupted by usage throttling or restarts.
func (o *Orchestrator) AnalyzePending(ctx context.Context) error {
	pending, err := o.store.PRsNeedingAnalysis(ctx, ClaudeCodePromptVersion)
	if err != nil {
		return fmt.Errorf("query pending PRs: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}
	o.log.Info().Int("count", len(pending)).Msg("enqueuing pending PRs")

	var prs []github.PRData
	for _, pr := range pending {
		prs = append(prs, storePRToData(pr))
	}
	o.Enqueue(prs...)
	return nil
}

func storePRToData(pr store.PullRequest) github.PRData {
	var labels []string
	_ = json.Unmarshal(pr.Labels, &labels)
	return github.PRData{
		Number:        pr.Number,
		Title:         pr.Title,
		Author:        pr.Author,
		State:         pr.State,
		Labels:        labels,
		HeadSHA:       pr.HeadSHA,
		Additions:     pr.Additions,
		Deletions:     pr.Deletions,
		CommentsCount: pr.CommentsCount,
		CreatedAt:     pr.CreatedAt,
		UpdatedAt:     pr.UpdatedAt,
	}
}

// analyzeSingle analyzes one PR and persists the result.
func (o *Orchestrator) analyzeSingle(ctx context.Context, pr github.PRData) (*store.Analysis, error) {
	result, err := o.analyzer.AnalyzePR(ctx, pr)
	if err != nil {
		return nil, err
	}

	relatedJSON, _ := json.Marshal(result.RelatedPRs)
	analysis := &store.Analysis{
		PRNumber:      pr.Number,
		Category:      result.Category,
		Confidence:    result.Confidence,
		Explanation:   result.Explanation,
		RelatedPRs:    relatedJSON,
		Model:         result.Model,
		PromptVersion: result.PromptVersion,
		InputTokens:   result.InputTokens,
		OutputTokens:  result.OutputTokens,
		CreatedAt:     time.Now().UTC(),
	}

	if err := o.store.InsertAnalysis(ctx, analysis); err != nil {
		return nil, fmt.Errorf("insert analysis: %w", err)
	}
	if err := o.store.SetAnalyzedSHA(ctx, pr.Number, pr.HeadSHA); err != nil {
		o.log.Error().Err(err).Int("pr", pr.Number).Msg("failed to set analyzed SHA")
	}

	return analysis, nil
}

// waitForUsage checks usage and blocks until utilization drops below threshold.
func (o *Orchestrator) waitForUsage(ctx context.Context) error {
	if o.usageChecker == nil || o.usageThreshold <= 0 {
		return nil
	}
	for {
		status, err := o.usageChecker.CheckUsage(ctx)
		if err != nil {
			o.log.Warn().Err(err).Msg("failed to check usage, continuing anyway")
			return nil
		}
		o.log.Info().Float64("utilization", status.Utilization).Float64("threshold", o.usageThreshold).Msg("usage check")
		if status.Utilization < o.usageThreshold {
			return nil
		}

		waitDur := time.Until(status.ResetsAt)
		if waitDur <= 0 {
			waitDur = 5 * time.Minute
		}
		o.log.Warn().
			Float64("utilization", status.Utilization).
			Time("resets_at", status.ResetsAt).
			Dur("wait", waitDur).
			Msg("usage threshold exceeded, waiting for reset")

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitDur):
		}
	}
}
