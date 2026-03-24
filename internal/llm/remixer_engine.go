package llm

import (
	"context"
	"fmt"
	"math/rand/v2"
)

type remixerMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type remixerChatRequest struct {
	Messages    []remixerMessage `json:"messages"`
	Temperature *float64         `json:"temperature,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
}

type remixerChatResponse struct {
	Content string `json:"content"`
	Model   string `json:"model"`
}

type remixerChatResult struct {
	remixerChatResponse
	SelectedModel string `json:"selected_model"`
}

type remixerProvider interface {
	Chat(ctx context.Context, req remixerChatRequest) (remixerChatResponse, error)
}

type weightedSelector struct {
	entries     []selectorEntry
	totalWeight int
}

type selectorEntry struct {
	name      string
	providers []remixerProvider
	upper     int
}

type selectorResult struct {
	ModelName string
	Provider  remixerProvider
}

type remixerEngine struct {
	selector *weightedSelector
}

func newRemixerEngine(configPath string) (*remixerEngine, error) {
	cfg, err := loadRemixerConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	selector, err := newWeightedSelector(cfg.Models)
	if err != nil {
		return nil, fmt.Errorf("creating selector: %w", err)
	}

	return &remixerEngine{selector: selector}, nil
}

func (r *remixerEngine) Chat(ctx context.Context, req remixerChatRequest) (remixerChatResult, error) {
	selected := r.selector.Select()

	resp, err := selected.Provider.Chat(ctx, req)
	if err != nil {
		return remixerChatResult{}, fmt.Errorf("model %q: %w", selected.ModelName, err)
	}

	return remixerChatResult{
		remixerChatResponse: resp,
		SelectedModel:       selected.ModelName,
	}, nil
}

func newWeightedSelector(models []remixerModelConfig) (*weightedSelector, error) {
	entries := make([]selectorEntry, 0, len(models))
	cumulative := 0

	for _, model := range models {
		providers := make([]remixerProvider, 0, len(model.Providers))
		for _, providerCfg := range model.Providers {
			provider, err := newRemixerProvider(providerCfg)
			if err != nil {
				return nil, err
			}
			providers = append(providers, provider)
		}

		cumulative += model.Weight
		entries = append(entries, selectorEntry{
			name:      model.Name,
			providers: providers,
			upper:     cumulative,
		})
	}

	return &weightedSelector{
		entries:     entries,
		totalWeight: cumulative,
	}, nil
}

func (ws *weightedSelector) Select() selectorResult {
	r := rand.IntN(ws.totalWeight)
	for _, entry := range ws.entries {
		if r < entry.upper {
			return selectorResult{
				ModelName: entry.name,
				Provider:  entry.providers[0],
			}
		}
	}

	last := ws.entries[len(ws.entries)-1]
	return selectorResult{
		ModelName: last.name,
		Provider:  last.providers[0],
	}
}
