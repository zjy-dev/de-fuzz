package llm

import (
	"fmt"
	"os"
	"strings"

	appconfig "github.com/zjy-dev/de-fuzz/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	openAIProtocolAuto            = "auto"
	openAIProtocolChatCompletions = "chat_completions"
	openAIProtocolResponses       = "responses"
)

type remixerConfig struct {
	Models []remixerModelConfig `yaml:"models"`
}

type remixerModelConfig struct {
	Name      string                  `yaml:"name"`
	Weight    int                     `yaml:"weight"`
	Providers []remixerProviderConfig `yaml:"providers"`
}

type remixerProviderConfig struct {
	Type     string `yaml:"type"`
	Endpoint string `yaml:"endpoint"`
	Model    string `yaml:"model"`
	APIKey   string `yaml:"api_key"`
	Protocol string `yaml:"protocol,omitempty"`
}

func loadRemixerConfig(path string) (*remixerConfig, error) {
	// Mirror the main config loader so remixer.yaml can keep using the same
	// local .env workflow in both tests and normal runs.
	if err := appconfig.LoadEnvFromDotEnvRecursive("."); err != nil {
		return nil, fmt.Errorf("load .env: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	expanded := os.Expand(string(data), func(key string) string {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		return "${" + key + "}"
	})

	var cfg remixerConfig
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parsing config YAML: %w", err)
	}

	if err := validateRemixerConfig(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func validateRemixerConfig(cfg *remixerConfig) error {
	if len(cfg.Models) == 0 {
		return fmt.Errorf("at least one model must be configured")
	}

	names := make(map[string]bool)
	for i, model := range cfg.Models {
		if model.Name == "" {
			return fmt.Errorf("model[%d]: name is required", i)
		}
		if names[model.Name] {
			return fmt.Errorf("model[%d]: duplicate name %q", i, model.Name)
		}
		names[model.Name] = true

		if model.Weight <= 0 {
			return fmt.Errorf("model %q: weight must be positive", model.Name)
		}
		if len(model.Providers) == 0 {
			return fmt.Errorf("model %q: at least one provider is required", model.Name)
		}

		for j, provider := range model.Providers {
			if err := validateProviderType(provider.Type); err != nil {
				return fmt.Errorf("model %q provider[%d]: %w", model.Name, j, err)
			}
			if provider.Endpoint == "" {
				return fmt.Errorf("model %q provider[%d]: endpoint is required", model.Name, j)
			}
			if provider.Model == "" {
				return fmt.Errorf("model %q provider[%d]: model is required", model.Name, j)
			}
			if provider.APIKey == "" || strings.HasPrefix(provider.APIKey, "${") {
				return fmt.Errorf("model %q provider[%d]: api_key is required (check your .env)", model.Name, j)
			}

			if provider.Type == "openai" {
				if provider.Protocol == "" {
					cfg.Models[i].Providers[j].Protocol = openAIProtocolAuto
				} else if err := validateOpenAIProtocol(provider.Protocol); err != nil {
					return fmt.Errorf("model %q provider[%d]: %w", model.Name, j, err)
				}
			} else if provider.Protocol != "" {
				return fmt.Errorf("model %q provider[%d]: protocol is only supported for openai providers", model.Name, j)
			}
		}
	}

	return nil
}

func validateProviderType(providerType string) error {
	switch providerType {
	case "openai", "anthropic":
		return nil
	default:
		return fmt.Errorf("unsupported provider type %q (supported: openai, anthropic)", providerType)
	}
}

func validateOpenAIProtocol(protocol string) error {
	switch protocol {
	case openAIProtocolAuto, openAIProtocolChatCompletions, openAIProtocolResponses:
		return nil
	default:
		return fmt.Errorf("unsupported openai protocol %q (supported: auto, chat_completions, responses)", protocol)
	}
}
