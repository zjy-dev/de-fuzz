package llm

import (
	"context"
	"fmt"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicProvider struct {
	client anthropic.Client
	model  string
}

func newRemixerProvider(cfg remixerProviderConfig) (remixerProvider, error) {
	switch cfg.Type {
	case "openai":
		return newOpenAIProvider(cfg)
	case "anthropic":
		return newAnthropicProvider(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", cfg.Type)
	}
}

func newAnthropicProvider(cfg remixerProviderConfig) *anthropicProvider {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.Endpoint != "" {
		opts = append(opts, option.WithBaseURL(cfg.Endpoint))
	}

	return &anthropicProvider{
		client: anthropic.NewClient(opts...),
		model:  cfg.Model,
	}
}

func (p *anthropicProvider) Chat(ctx context.Context, req remixerChatRequest) (remixerChatResponse, error) {
	var systemBlocks []anthropic.TextBlockParam
	var msgParams []anthropic.MessageParam

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			systemBlocks = append(systemBlocks, anthropic.TextBlockParam{Text: m.Content})
		case "user":
			msgParams = append(msgParams, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case "assistant":
			msgParams = append(msgParams, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		default:
			return remixerChatResponse{}, fmt.Errorf("unsupported message role: %s", m.Role)
		}
	}

	if len(msgParams) == 0 {
		return remixerChatResponse{}, fmt.Errorf("at least one non-system message is required")
	}

	params := anthropic.MessageNewParams{
		Model:    anthropic.Model(p.model),
		Messages: msgParams,
	}

	maxTokens := 4096
	if req.MaxTokens != nil {
		maxTokens = *req.MaxTokens
	}
	params.MaxTokens = int64(maxTokens)

	if len(systemBlocks) > 0 {
		params.System = systemBlocks
	}
	if req.Temperature != nil {
		params.Temperature = anthropic.Float(*req.Temperature)
	}

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return remixerChatResponse{}, fmt.Errorf("anthropic messages: %w", err)
	}

	var content string
	for _, block := range resp.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	return remixerChatResponse{
		Content: content,
		Model:   string(resp.Model),
	}, nil
}
