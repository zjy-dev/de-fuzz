package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

const (
	openAIChatCompletionsPath   = "/chat/completions"
	openAIResponsesPath         = "/responses"
	defaultResponsesInstruction = "You are a helpful assistant."
)

type openAIProvider struct {
	client     *openai.Client
	httpClient *http.Client
	apiKey     string
	baseURL    string
	model      string
	protocol   string
}

type openAIResponsesRequest struct {
	Model        string                        `json:"model"`
	Instructions string                        `json:"instructions,omitempty"`
	Input        []openAIResponsesInputMessage `json:"input,omitempty"`
	Temperature  *float64                      `json:"temperature,omitempty"`
}

type openAIResponsesInputMessage struct {
	Role    string                      `json:"role"`
	Content []openAIResponsesInputBlock `json:"content"`
}

type openAIResponsesInputBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openAIResponsesResponse struct {
	Model      string                  `json:"model"`
	Output     []openAIResponsesOutput `json:"output"`
	OutputText json.RawMessage         `json:"output_text"`
}

type openAIResponsesOutput struct {
	Type    string                       `json:"type"`
	Content []openAIResponsesOutputBlock `json:"content"`
}

type openAIResponsesOutputBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openAIErrorEnvelope struct {
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func newOpenAIProvider(cfg remixerProviderConfig) (*openAIProvider, error) {
	baseURL, err := normalizeOpenAIBaseURL(cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	protocol := cfg.Protocol
	if protocol == "" {
		protocol = openAIProtocolAuto
	}

	openAIConfig := openai.DefaultConfig(cfg.APIKey)
	openAIConfig.BaseURL = baseURL

	return &openAIProvider{
		client:     openai.NewClientWithConfig(openAIConfig),
		httpClient: http.DefaultClient,
		apiKey:     cfg.APIKey,
		baseURL:    baseURL,
		model:      cfg.Model,
		protocol:   protocol,
	}, nil
}

func (p *openAIProvider) Chat(ctx context.Context, req remixerChatRequest) (remixerChatResponse, error) {
	switch p.protocol {
	case openAIProtocolAuto, openAIProtocolChatCompletions:
		return p.chatCompletions(ctx, req)
	case openAIProtocolResponses:
		return p.responses(ctx, req)
	default:
		return remixerChatResponse{}, fmt.Errorf("openai provider: unsupported protocol %q", p.protocol)
	}
}

func (p *openAIProvider) chatCompletions(ctx context.Context, req remixerChatRequest) (remixerChatResponse, error) {
	messages := make([]openai.ChatCompletionMessage, 0, len(req.Messages))
	for _, message := range req.Messages {
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    message.Role,
			Content: message.Content,
		})
	}

	openAIRequest := openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: messages,
	}
	if req.Temperature != nil {
		openAIRequest.Temperature = float32(*req.Temperature)
	}
	if req.MaxTokens != nil {
		openAIRequest.MaxTokens = *req.MaxTokens
	}

	resp, err := p.client.CreateChatCompletion(ctx, openAIRequest)
	if err != nil {
		return remixerChatResponse{}, fmt.Errorf("openai chat completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return remixerChatResponse{}, fmt.Errorf("openai chat completion: no choices returned")
	}

	return remixerChatResponse{
		Content: resp.Choices[0].Message.Content,
		Model:   resp.Model,
	}, nil
}

func (p *openAIProvider) responses(ctx context.Context, req remixerChatRequest) (remixerChatResponse, error) {
	instructions, input := buildResponsesInput(req.Messages)
	if instructions == "" {
		instructions = defaultResponsesInstruction
	}

	responseReq := openAIResponsesRequest{
		Model:        p.model,
		Instructions: instructions,
		Input:        input,
	}

	// The current OpenAI-compatible gateway streams responses and rejects
	// max_output_tokens, while DeFuzz does not depend on hard output caps here.

	// GPT-5 style Responses backends commonly reject temperature overrides, so
	// we omit them on that family to keep the compatibility path stable.
	if req.Temperature != nil && allowsResponsesTemperature(p.model) {
		responseReq.Temperature = req.Temperature
	}

	body, err := json.Marshal(responseReq)
	if err != nil {
		return remixerChatResponse{}, fmt.Errorf("openai responses: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		p.baseURL+openAIResponsesPath,
		bytes.NewReader(body),
	)
	if err != nil {
		return remixerChatResponse{}, fmt.Errorf("openai responses: build request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return remixerChatResponse{}, fmt.Errorf("openai responses: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return remixerChatResponse{}, decodeOpenAIError("openai responses", resp.Body, resp.StatusCode)
	}

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return remixerChatResponse{}, fmt.Errorf("openai responses: read response: %w", err)
	}

	response, err := decodeResponsesPayload(payload)
	if err != nil {
		return remixerChatResponse{}, fmt.Errorf("openai responses: decode response: %w", err)
	}

	content := extractResponsesText(response)
	if content == "" {
		return remixerChatResponse{}, fmt.Errorf("openai responses: no text content returned")
	}

	model := response.Model
	if model == "" {
		model = p.model
	}

	return remixerChatResponse{
		Content: content,
		Model:   model,
	}, nil
}

func buildResponsesInput(messages []remixerMessage) (string, []openAIResponsesInputMessage) {
	systemMessages := make([]string, 0, len(messages))
	input := make([]openAIResponsesInputMessage, 0, len(messages))

	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}

		if message.Role == "system" {
			// Responses handles system guidance more consistently via the
			// top-level instructions field than as a synthetic chat message.
			systemMessages = append(systemMessages, content)
			continue
		}

		input = append(input, openAIResponsesInputMessage{
			Role: message.Role,
			Content: []openAIResponsesInputBlock{
				{
					Type: "input_text",
					Text: content,
				},
			},
		})
	}

	return strings.Join(systemMessages, "\n\n"), input
}

func extractResponsesText(resp openAIResponsesResponse) string {
	if text := strings.TrimSpace(extractRawOutputText(resp.OutputText)); text != "" {
		return text
	}

	parts := make([]string, 0, len(resp.Output))
	for _, item := range resp.Output {
		for _, block := range item.Content {
			if block.Text == "" {
				continue
			}
			switch block.Type {
			case "output_text", "text", "input_text":
				parts = append(parts, strings.TrimSpace(block.Text))
			}
		}
	}

	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func extractRawOutputText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	var items []string
	if err := json.Unmarshal(raw, &items); err == nil {
		return strings.Join(items, "\n\n")
	}

	return ""
}

func decodeResponsesPayload(payload []byte) (openAIResponsesResponse, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return openAIResponsesResponse{}, fmt.Errorf("empty response body")
	}

	if bytes.HasPrefix(trimmed, []byte("{")) {
		var response openAIResponsesResponse
		if err := json.Unmarshal(trimmed, &response); err != nil {
			return openAIResponsesResponse{}, err
		}
		return response, nil
	}

	return decodeResponsesEventStream(trimmed)
}

func decodeResponsesEventStream(payload []byte) (openAIResponsesResponse, error) {
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	dataLines := make([]string, 0, 4)

	processEvent := func() (openAIResponsesResponse, bool, error) {
		if len(dataLines) == 0 {
			return openAIResponsesResponse{}, false, nil
		}

		raw := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]

		var event struct {
			Type     string                  `json:"type"`
			Response openAIResponsesResponse `json:"response"`
		}
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return openAIResponsesResponse{}, false, err
		}
		if event.Type == "response.completed" {
			return event.Response, true, nil
		}

		return openAIResponsesResponse{}, false, nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if response, done, err := processEvent(); err != nil {
				return openAIResponsesResponse{}, err
			} else if done {
				return response, nil
			}
			continue
		}

		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}

	if err := scanner.Err(); err != nil {
		return openAIResponsesResponse{}, err
	}

	if response, done, err := processEvent(); err != nil {
		return openAIResponsesResponse{}, err
	} else if done {
		return response, nil
	}

	return openAIResponsesResponse{}, fmt.Errorf("response.completed event not found")
}

func decodeOpenAIError(prefix string, body io.Reader, statusCode int) error {
	payload, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("%s: status %d", prefix, statusCode)
	}

	var errResp openAIErrorEnvelope
	if err := json.Unmarshal(payload, &errResp); err == nil && errResp.Error != nil && errResp.Error.Message != "" {
		return fmt.Errorf("%s: status %d: %s", prefix, statusCode, errResp.Error.Message)
	}

	return fmt.Errorf("%s: status %d: %s", prefix, statusCode, strings.TrimSpace(string(payload)))
}

func normalizeOpenAIBaseURL(endpoint string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", fmt.Errorf("openai provider: parse endpoint: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("openai provider: invalid endpoint %q", endpoint)
	}

	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case path == "":
		path = "/v1"
	case strings.HasSuffix(path, openAIChatCompletionsPath):
		path = strings.TrimSuffix(path, openAIChatCompletionsPath)
	case strings.HasSuffix(path, openAIResponsesPath):
		path = strings.TrimSuffix(path, openAIResponsesPath)
	}

	path = strings.TrimRight(path, "/")
	if path == "" {
		path = "/v1"
	} else if !strings.HasSuffix(path, "/v1") {
		path += "/v1"
	}

	parsed.Path = path
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func allowsResponsesTemperature(model string) bool {
	return !strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-5")
}
