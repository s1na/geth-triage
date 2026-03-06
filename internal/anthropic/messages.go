package anthropic

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/s1na/geth-triage/internal/github"
)

// AnalyzePR analyzes a single PR using the Messages API (synchronous).
func (c *Client) AnalyzePR(ctx context.Context, pr github.PRData) (*AnalysisResult, int, int, error) {
	userPrompt := BuildUserPrompt(pr)

	msg, err := c.sdk.Messages.New(ctx, sdk.MessageNewParams{
		Model:     sdk.Model(c.model),
		MaxTokens: 1024,
		System: []sdk.TextBlockParam{
			{Text: systemPrompt},
		},
		Messages: []sdk.MessageParam{
			sdk.NewUserMessage(sdk.NewTextBlock(userPrompt)),
		},
		Temperature: sdk.Float(0.2),
	})
	if err != nil {
		return nil, 0, 0, fmt.Errorf("anthropic messages: %w", err)
	}

	inputTokens := int(msg.Usage.InputTokens)
	outputTokens := int(msg.Usage.OutputTokens)

	// Extract text from response
	var text string
	for _, block := range msg.Content {
		if block.Type == "text" {
			text = block.Text
			break
		}
	}

	if text == "" {
		return nil, inputTokens, outputTokens, fmt.Errorf("no text in response")
	}

	result, err := parseAnalysisResult(text)
	if err != nil {
		return nil, inputTokens, outputTokens, fmt.Errorf("parse response: %w (raw: %s)", err, text)
	}

	return result, inputTokens, outputTokens, nil
}

func parseAnalysisResult(text string) (*AnalysisResult, error) {
	var result AnalysisResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, err
	}
	if !ValidCategories[result.Category] {
		return nil, fmt.Errorf("invalid category: %s", result.Category)
	}
	if result.Confidence < 0 || result.Confidence > 1 {
		return nil, fmt.Errorf("confidence out of range: %f", result.Confidence)
	}
	return &result, nil
}
