package anthropic

import (
	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type Client struct {
	sdk   sdk.Client
	model string
}

func NewClient(apiKey, model string) *Client {
	return &Client{
		sdk:   sdk.NewClient(option.WithAPIKey(apiKey)),
		model: model,
	}
}

func (c *Client) Model() string {
	return c.model
}
