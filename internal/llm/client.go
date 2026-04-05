package llm

import (
	"context"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

const groqBaseURL = "https://api.groq.com/openai/v1"

// Client wraps Groq's OpenAI-compatible API.
type Client struct {
	inner *openai.Client
	model string
}

func NewClient(apiKey, model string) *Client {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = groqBaseURL
	return &Client{
		inner: openai.NewClientWithConfig(cfg),
		model: model,
	}
}

// Model returns the configured model name — useful for logging.
func (c *Client) Model() string {
	return c.model
}

// Ping sends a minimal request to confirm the API key works.
// Called once at startup; not part of the investigation path.
func (c *Client) Ping(ctx context.Context) error {
	req := openai.ChatCompletionRequest{
		Model:     c.model,
		MaxTokens: 5,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: "ping"},
		},
	}
	if _, err := c.inner.CreateChatCompletion(ctx, req); err != nil {
		return fmt.Errorf("groq ping: %w", err)
	}
	return nil
}
