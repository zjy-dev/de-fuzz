package config

import (
	"fmt"

	"github.com/spf13/viper"
)

// LLMConfig holds the configuration for the Large Language Model.
type LLMConfig struct {
	Provider string `mapstructure:"provider"`
	Model    string `mapstructure:"model"`
	APIKey   string `mapstructure:"api_key"`
	Endpoint string `mapstructure:"endpoint"`
}

// Load reads a configuration file from the "configs" directory into a struct.
// The configName parameter should be the base name of the file without the extension (e.g., "llm").
// The result parameter should be a pointer to a struct that the configuration will be unmarshaled into.
func Load(configName string, result interface{}) error {
	v := viper.New()
	v.SetConfigName(configName)
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
