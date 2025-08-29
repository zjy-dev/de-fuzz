package config

import (
	"fmt"

	"github.com/spf13/viper"
)

// Config holds the top-level configuration for the application.
type Config struct {
	LLM    LLMConfig    `mapstructure:"llm"`
	Fuzzer FuzzerConfig `mapstructure:"fuzzer"`
}

// LLMConfig holds the configuration for the Large Language Model.
type LLMConfig struct {
	Provider string `mapstructure:"provider"`
	Model    string `mapstructure:"model"`
	APIKey   string `mapstructure:"api_key"`
	Endpoint string `mapstructure:"endpoint"`
}

// FuzzerConfig holds the configuration for the fuzzer itself.
type FuzzerConfig struct {
	ISA          string `mapstructure:"isa"`
	Strategy     string `mapstructure:"strategy"`
	InitialSeeds int    `mapstructure:"initial_seeds"`
	BugQuota     int    `mapstructure:"bug_quota"`
}

// Load reads a configuration file from the "configs" directory into a struct.
// The configFileName parameter should be the base name of the file without the extension (e.g., "llm").
// The result parameter should be a pointer to a struct that the configuration will be unmarshaled into.
func Load(configFileName string, result interface{}) error {
	v := viper.New()
	v.SetConfigName(configFileName)
	v.SetConfigType("yaml")
	// 支持多路径查找
	v.AddConfigPath("configs")       // 当前工作目录下的configs
	v.AddConfigPath("../configs")    // 父目录下的configs（适配go test包内运行）
	v.AddConfigPath("../../configs") // 适配更深层次的包

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := v.Unmarshal(result); err != nil {
		return fmt.Errorf("failed to unmarshal config data: %w", err)
	}

	return nil
}

// LoadConfig loads the entire application configuration from all sources.
func LoadConfig() (*Config, error) {
	var cfg Config
	if err := Load("llm", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
