package llm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

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

func (c *Client) Model() string { return c.model }

// Ping sends a minimal request to confirm the API key and connectivity.
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

// ChatResponse is what the ReAct loop receives from each Groq call.
type ChatResponse struct {
	// Content is non-empty when the LLM produces a text reply (final answer).
	Content string

	// ToolCalls is non-empty when the LLM wants to call one or more tools.
	ToolCalls []openai.ToolCall

	// Usage contains token counts for observability.
	Usage openai.Usage
}

// internal/llm/client.go — replace the Chat method
func (c *Client) Chat(
	ctx context.Context,
	messages []openai.ChatCompletionMessage,
	tools []openai.Tool,
) (*ChatResponse, error) {
	// Disable parallel tool calls — Groq models are more reliable
	// with one tool call at a time
	noParallel := false

	req := openai.ChatCompletionRequest{
		Model:             c.model,
		MaxTokens:         1024,
		Messages:          messages,
		Tools:             tools,
		ParallelToolCalls: &noParallel,
	}

	backoff := 6 * time.Second
	for attempt := range 3 {
		resp, err := c.inner.CreateChatCompletion(ctx, req)
		if err != nil {
			errStr := err.Error()
			isRateLimit := strings.Contains(errStr, "429")
			isToolUseFailed := strings.Contains(errStr, "tool_use_failed")

			if (isRateLimit || isToolUseFailed) && attempt < 2 {
				slog.Warn("groq error, retrying",
					"attempt", attempt+1,
					"rate_limit", isRateLimit,
					"tool_use_failed", isToolUseFailed,
					"backoff_seconds", backoff.Seconds(),
				)
				select {
				case <-time.After(backoff):
					backoff *= 2
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, fmt.Errorf("groq chat: %w", err)
		}

		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("groq returned no choices")
		}

		choice := resp.Choices[0]
		return &ChatResponse{
			Content:   choice.Message.Content,
			ToolCalls: choice.Message.ToolCalls,
			Usage:     resp.Usage,
		}, nil
	}
	return nil, fmt.Errorf("groq: exhausted retries")
}
