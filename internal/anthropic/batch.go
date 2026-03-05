package anthropic

import (
	"context"
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/rs/zerolog"
	"github.com/sina-geth/geth-triage/internal/github"
)

// CreateBatch creates a batch request for multiple PRs.
// Returns the batch ID and a map of custom_id -> PR number.
func (c *Client) CreateBatch(ctx context.Context, prs []github.PRData) (string, map[string]int, error) {
	var requests []sdk.MessageBatchNewParamsRequest
	customIDMap := make(map[string]int)

	for _, pr := range prs {
		customID := fmt.Sprintf("pr-%d", pr.Number)
		customIDMap[customID] = pr.Number
		userPrompt := BuildUserPrompt(pr)

		requests = append(requests, sdk.MessageBatchNewParamsRequest{
			CustomID: customID,
			Params: sdk.MessageBatchNewParamsRequestParams{
				Model:     sdk.Model(c.model),
				MaxTokens: 1024,
				System: []sdk.TextBlockParam{
					{Text: systemPrompt},
				},
				Messages: []sdk.MessageParam{
					sdk.NewUserMessage(sdk.NewTextBlock(userPrompt)),
				},
				Temperature: sdk.Float(0.2),
			},
		})
	}

	batch, err := c.sdk.Messages.Batches.New(ctx, sdk.MessageBatchNewParams{
		Requests: requests,
	})
	if err != nil {
		return "", nil, fmt.Errorf("create batch: %w", err)
	}

	return batch.ID, customIDMap, nil
}

// PollBatch checks the status of a batch.
func (c *Client) PollBatch(ctx context.Context, batchID string) (*sdk.MessageBatch, error) {
	batch, err := c.sdk.Messages.Batches.Get(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("poll batch %s: %w", batchID, err)
	}
	return batch, nil
}

// CollectBatchResults retrieves and parses results from a completed batch.
func (c *Client) CollectBatchResults(ctx context.Context, batchID string, log zerolog.Logger) (map[string]*AnalysisResult, map[string][2]int, error) {
	stream := c.sdk.Messages.Batches.ResultsStreaming(ctx, batchID)

	results := make(map[string]*AnalysisResult)
	tokens := make(map[string][2]int) // [input, output]

	for stream.Next() {
		item := stream.Current()
		customID := item.CustomID

		switch r := item.Result.AsAny().(type) {
		case sdk.MessageBatchSucceededResult:
			var text string
			for _, block := range r.Message.Content {
				if block.Type == "text" {
					text = block.Text
					break
				}
			}
			tokens[customID] = [2]int{int(r.Message.Usage.InputTokens), int(r.Message.Usage.OutputTokens)}

			result, err := parseAnalysisResult(text)
			if err != nil {
				log.Error().Err(err).Str("custom_id", customID).Str("raw", text).Msg("failed to parse batch result")
				continue
			}
			results[customID] = result

		case sdk.MessageBatchErroredResult:
			log.Error().Str("custom_id", customID).Str("error", r.Error.Error.Message).Msg("batch request errored")

		default:
			log.Warn().Str("custom_id", customID).Str("type", item.Result.Type).Msg("batch request not succeeded")
		}
	}

	if err := stream.Err(); err != nil {
		return results, tokens, fmt.Errorf("stream batch results: %w", err)
	}

	return results, tokens, nil
}
