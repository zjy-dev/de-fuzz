package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/viper"
)

// Config holds the top-level configuration for the application.
type Config struct {
	LLM      LLMConfig      `mapstructure:"llm"`
	ISA      string         `mapstructure:"isa"`
	Strategy string         `mapstructure:"strategy"`
	LogLevel string         `mapstructure:"log_level"`
	LogDir   string         `mapstructure:"log_dir"`
	Compiler CompilerConfig `mapstructure:"compiler"`
}

// FuzzConfig holds the configuration for the fuzzing process.
// These values serve as defaults and can be overridden by command line flags.
type FuzzConfig struct {
	// OutputRootDir is the root directory for fuzzing artifacts (default: "fuzz_out")
	// Actual output will be at {OutputRootDir}/{isa}/{strategy}
	OutputRootDir string `mapstructure:"output_root_dir"`

	// MaxIterations is the maximum number of fuzzing iterations (0 = unlimited)
	MaxIterations int `mapstructure:"max_iterations"`

	// MaxNewSeeds is the maximum new seeds to generate per interesting seed
	MaxNewSeeds int `mapstructure:"max_new_seeds"`

	// MaxTestCases is the maximum number of test cases to generate per seed
	// If 0, test cases will not be generated (useful for oracles like canary that don't need test cases)
	MaxTestCases int `mapstructure:"max_test_cases"`

	// FunctionTemplate is the path to a C code template file (optional)
	// If provided, LLM will only generate the function body, and the result will be merged with the template
	// This is useful for strategies like canary where we need specific program structure
	FunctionTemplate string `mapstructure:"function_template"`

	// Timeout is the execution timeout in seconds
	Timeout int `mapstructure:"timeout"`

	// UseQEMU enables QEMU for cross-architecture execution
	UseQEMU bool `mapstructure:"use_qemu"`

	// QEMUPath is the path to QEMU user-mode executable
	QEMUPath string `mapstructure:"qemu_path"`

	// QEMUSysroot is the sysroot path for QEMU (-L argument)
	QEMUSysroot string `mapstructure:"qemu_sysroot"`

	// CFGFilePath is the path to the GCC CFG dump file (optional)
	// Used for CFG-guided coverage analysis and target function tracking
	// Example: "/path/to/gcc-build/gcc/cfgexpand.cc.015t.cfg"
	CFGFilePath string `mapstructure:"cfg_file_path"`

	// MappingPath is the path to store/load coverage mapping (optional)
	// If empty, defaults to {output_dir}/state/coverage_mapping.json
	MappingPath string `mapstructure:"mapping_path"`

	// MaxConstraintRetries is the maximum number of divergence analysis retries
	// per target basic block when constraint solving fails (default: 3)
	MaxConstraintRetries int `mapstructure:"max_constraint_retries"`

	// WeightDecayFactor is the multiplier applied to BB weight after failed iteration
	// Valid range: (0, 1], default: 0.8
	WeightDecayFactor float64 `mapstructure:"weight_decay_factor"`
}

// InternalLLMConfig is used for unmarshaling the config.yaml which only contains the provider string
type InternalLLMConfig struct {
	Provider string `mapstructure:"llm"`
}

// CompilerInfo holds basic compiler identification from the main config.
type CompilerInfo struct {
	Name    string `mapstructure:"name"`
	Version string `mapstructure:"version"`
}

// OracleConfig holds the configuration for the oracle.
type OracleConfig struct {
	// Type specifies the name of the oracle plugin to use (e.g., "llm", "crash", "diff")
	Type string `mapstructure:"type"`

	// Options holds arbitrary configuration for the specific oracle implementation
	Options map[string]interface{} `mapstructure:"options"`
}

// LLMConfig holds the configuration for the Large Language Model.
type LLMConfig struct {
	Provider    string  `mapstructure:"provider"`
	Model       string  `mapstructure:"model"`
	APIKey      string  `mapstructure:"api_key"`
	Endpoint    string  `mapstructure:"endpoint"`
	Temperature float64 `mapstructure:"temperature"`
}

// TargetFunction specifies a source file and the functions within it to track for coverage.
// This is used for fine-grained coverage analysis and CFG-based fuzzing.
type TargetFunction struct {
	// File is the relative path to the source file (e.g., "gcc-releases-gcc-12.2.0/gcc/cfgexpand.cc")
	File string `mapstructure:"file"`

	// Functions is the list of function names to track within this file
	Functions []string `mapstructure:"functions"`
}

// CompilerConfig holds the configuration for the target compiler.
// Note: The compiler config file may contain additional top-level fields (like 'targets')
// that are used by external tools (e.g., gcovr-json-util) and are not parsed here.
type CompilerConfig struct {
	// Path is the path to the compiler executable (e.g., /path/to/gcc)
	Path string `mapstructure:"path"`

	// GcovrExecPath is the path to gcovr executable for coverage analysis
	GcovrExecPath string `mapstructure:"gcovr_exec_path"`

	// SourceParentPath is the parent directory of source files for coverage reporting
	SourceParentPath string `mapstructure:"source_parent_path"`

	// GcovrCommand is the complete gcovr command template (optional)
	// If empty, a default command will be constructed from other config values
	GcovrCommand string `mapstructure:"gcovr_command"`

	// CFlags are additional compiler flags to pass to GCC
	// Example: ["-fstack-protector-strong", "-O0", "-B/path/to/lib"]
	CFlags []string `mapstructure:"cflags"`

	// TotalReportPath is the path to store accumulated coverage report (optional)
	// If empty, defaults to {output_dir}/state/total.json for resume capability
	// This file is critical for checkpointing: it stores accumulated coverage data
	// that allows the fuzzer to resume from where it left off after interruption
	TotalReportPath string `mapstructure:"total_report_path"`

	// Fuzz holds the fuzzing configuration for this compiler/ISA/strategy combination
	Fuzz FuzzConfig `mapstructure:"fuzz"`

	// Oracle holds the oracle configuration for this compiler/ISA/strategy combination
	Oracle OracleConfig `mapstructure:"oracle"`

	// Targets specifies the source files and functions to focus on for coverage-guided fuzzing.
	// This enables fine-grained control over which code paths the fuzzer should explore.
	Targets []TargetFunction `mapstructure:"targets"`
}

// envVarPattern matches environment variable placeholders: ${VAR_NAME} or $VAR_NAME
var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// resolveEnvVars replaces environment variable placeholders in a string with their values.
// Supports two formats:
//   - ${VAR_NAME}: Braced format
//   - $VAR_NAME: Simple format (must start with letter or underscore)
//
// If an environment variable is not set, it is left as-is in the string.
func resolveEnvVars(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		// Extract variable name (remove ${} or $)
		varName := match
		if strings.HasPrefix(match, "${") && strings.HasSuffix(match, "}") {
			varName = match[2 : len(match)-1]
		} else if strings.HasPrefix(match, "$") {
			varName = match[1:]
		}

		// Get environment variable value
		if value, ok := os.LookupEnv(varName); ok {
			return value
		}

		// Variable not found, return original placeholder
		return match
	})
}

// LoadEnvFromDotEnv loads environment variables from a .env file in the specified directory.
// The .env file should contain KEY=value pairs, one per line.
// Lines starting with # are treated as comments and ignored.
// This function is useful for local development and testing without requiring a full environment setup.
func LoadEnvFromDotEnv(dir string) error {
	envPath := filepath.Join(dir, ".env")

	// Check if .env file exists
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		// .env file is optional, just return without error
		return nil
	}

	// Read .env file
	data, err := os.ReadFile(envPath)
	if err != nil {
		return fmt.Errorf("failed to read .env file: %w", err)
	}

	// Parse and set environment variables
	lines := strings.Split(string(data), "\n")
	for lineNum, line := range lines {
		// Skip empty lines and comments
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=value
		idx := strings.Index(line, "=")
		if idx < 0 {
			return fmt.Errorf("invalid line in .env file at line %d: missing '='", lineNum+1)
		}

		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])

		// Remove quotes if present
		if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
			value = value[1 : len(value)-1]
		} else if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
			value = value[1 : len(value)-1]
		}

		// Only set if not already present
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}

	return nil
}

// LoadEnvFromDotEnvRecursive searches for .env file in the specified directory and its parents.
// It returns without error if no .env file is found (file is optional).
func LoadEnvFromDotEnvRecursive(startDir string) error {
	// First try startDir and its parents
	dir := startDir
	for i := 0; i < 5; i++ {
		envPath := filepath.Join(dir, ".env")
		if _, err := os.Stat(envPath); err == nil {
			// Found .env file, load it
			return LoadEnvFromDotEnv(dir)
		}

		// Move to parent directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			break
		}
		dir = parent
	}

	// If not found, try absolute paths from common project root locations
	// This handles tests running in subdirectories
	wd, _ := os.Getwd()

	// Build list of possible roots - go up from wd until we find .env or reach root
	for i := 0; i < 10; i++ {
		envPath := filepath.Join(wd, ".env")
		if _, err := os.Stat(envPath); err == nil {
			return LoadEnvFromDotEnv(wd)
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}
		wd = parent
	}

	return nil
}

// applyEnvResolution recursively applies environment variable resolution to a struct.
// Only string fields with mapstructure tags are processed.
func applyEnvResolution(v *viper.Viper) {
	// Get all settings as a flat map
	settings := v.AllSettings()

	// Traverse and resolve env vars in string values
	resolveInMap(settings)

	// Reconfigure viper with resolved values
	v = viper.New()
	for key, value := range settings {
		v.Set(key, value)
	}
}

// resolveInMap recursively resolves environment variables in map values.
func resolveInMap(m map[string]interface{}) {
	for k, v := range m {
		switch val := v.(type) {
		case string:
			resolved := resolveEnvVars(val)
			if resolved != val {
				m[k] = resolved
			}
		case map[string]interface{}:
			resolveInMap(val)
		case []interface{}:
			resolveInSlice(val)
		}
	}
}

// resolveInSlice resolves environment variables in slice elements.
func resolveInSlice(s []interface{}) {
	for i, v := range s {
		switch val := v.(type) {
		case string:
			s[i] = resolveEnvVars(val)
		case map[string]interface{}:
			resolveInMap(val)
		}
	}
}

// Load reads a configuration file from the "configs" directory into a struct.
// The configFileName parameter should be the base name of the file without the extension (e.g., "llm").
// The result parameter should be a pointer to a struct that the configuration will be unmarshaled into.
//
// For the main config.yaml file, this function expects a 'config' top-level object and will
// unmarshal it into the Config struct. For compiler config files, it will unmarshal the
// 'compiler' top-level object into CompilerConfig.
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

	// For Config struct, unmarshal from 'config' top-level object
	if cfg, ok := result.(*Config); ok {
		if v.IsSet("config") {
			if err := v.UnmarshalKey("config", cfg); err != nil {
				return fmt.Errorf("failed to unmarshal config data: %w", err)
			}
		} else {
			// Fallback: try to unmarshal the whole file (for backwards compatibility)
			if err := v.Unmarshal(cfg); err != nil {
				return fmt.Errorf("failed to unmarshal config data: %w", err)
			}
		}
		return nil
	}

	// For CompilerConfig struct, unmarshal from 'compiler' top-level object
	if compCfg, ok := result.(*CompilerConfig); ok {
		if v.IsSet("compiler") {
			if err := v.UnmarshalKey("compiler", compCfg); err != nil {
				return fmt.Errorf("failed to unmarshal compiler config: %w", err)
			}
		} else {
			// Fallback: try to unmarshal the whole file
			if err := v.Unmarshal(compCfg); err != nil {
				return fmt.Errorf("failed to unmarshal compiler config: %w", err)
			}
		}
		// Also parse top-level 'targets' field for CFG-guided fuzzing
		// The 'targets' field specifies which source files and functions to focus on
		if v.IsSet("targets") {
			var targets []TargetFunction
			if err := v.UnmarshalKey("targets", &targets); err != nil {
				return fmt.Errorf("failed to unmarshal targets config: %w", err)
			}
			compCfg.Targets = targets
		}
		return nil
	}

	// For other types, unmarshal the whole file
	if err := v.Unmarshal(result); err != nil {
		return fmt.Errorf("failed to unmarshal config data: %w", err)
	}

	return nil
}

// LoadConfig loads the entire application configuration from all sources.
func LoadConfig() (*Config, error) {
	var cfg Config

	// Load environment variables from .env file if present
	// Search in current directory and parent directories (for tests in subdirectories)
	if err := LoadEnvFromDotEnvRecursive("."); err != nil {
		return nil, fmt.Errorf("failed to load .env file: %w", err)
	}

	// Load main config file - read from 'config' top-level object
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("configs")
	v.AddConfigPath("../configs")
	v.AddConfigPath("../../configs")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to load main config: %w", err)
	}

	// Apply environment variable resolution to main config values
	applyEnvResolution(v)

	// Parse the main config fields (ISA, Strategy, Compiler info)
	// Note: We can't unmarshal the whole 'config' object directly because 'llm' is a string in config.yaml
	// but a struct in our Config type. So we parse fields individually.

	cfg.ISA = v.GetString("config.isa")
	cfg.Strategy = v.GetString("config.strategy")
	cfg.LogLevel = v.GetString("config.log_level")
	cfg.LogDir = v.GetString("config.log_dir")

	// Parse compiler name and version from config.yaml
	var compilerInfo CompilerInfo
	if err := v.UnmarshalKey("config.compiler", &compilerInfo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal compiler info: %w", err)
	}

	// Get the LLM provider from config.yaml (it's just a string there)
	llmProvider := v.GetString("config.llm")
	if llmProvider == "" {
		// For backwards compatibility, try common providers
		llmProvider = "deepseek"
	}

	// Load LLM config from llm.yaml
	llmViper := viper.New()
	llmViper.SetConfigName("llm")
	llmViper.SetConfigType("yaml")
	llmViper.AddConfigPath("configs")
	llmViper.AddConfigPath("../configs")
	llmViper.AddConfigPath("../../configs")

	if err := llmViper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to load llm config: %w", err)
	}

	// llm.yaml has an array structure: llms: [...]
	// Get all settings and resolve env vars in the llms array
	allSettings := llmViper.AllSettings()
	llmSettings := allSettings["llms"].([]interface{})

	// Parse each LLM config and resolve env vars
	var llms []LLMConfig
	for _, llmItem := range llmSettings {
		llmMap, ok := llmItem.(map[string]interface{})
		if !ok {
			continue
		}

		llmCfg := LLMConfig{}

		// Resolve env vars for each field
		if provider, ok := llmMap["provider"].(string); ok {
			llmCfg.Provider = resolveEnvVars(provider)
		}
		if model, ok := llmMap["model"].(string); ok {
			llmCfg.Model = resolveEnvVars(model)
		}
		if apiKey, ok := llmMap["api_key"].(string); ok {
			llmCfg.APIKey = resolveEnvVars(apiKey)
		}
		if endpoint, ok := llmMap["endpoint"].(string); ok {
			llmCfg.Endpoint = resolveEnvVars(endpoint)
		}
		if temp, ok := llmMap["temperature"].(float64); ok {
			llmCfg.Temperature = temp
		}

		llms = append(llms, llmCfg)
	}

	// Find the matching provider
	found := false
	for _, llmCfg := range llms {
		if llmCfg.Provider == llmProvider {
			cfg.LLM = llmCfg
			found = true
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("llm provider %s not found in llm.yaml", llmProvider)
	}

	// Load compiler-specific config based on the pattern
	// Only load the 'compiler' top-level object
	compilerConfigName := GetCompilerConfigName(&cfg)
	compilerViper := viper.New()
	compilerViper.SetConfigName(compilerConfigName)
	compilerViper.SetConfigType("yaml")
	compilerViper.AddConfigPath("configs")
	compilerViper.AddConfigPath("../configs")
	compilerViper.AddConfigPath("../../configs")

	if err := compilerViper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to load compiler config %s: %w", compilerConfigName, err)
	}

	// Apply environment variable resolution to compiler config values
	applyEnvResolution(compilerViper)

	// Only unmarshal the 'compiler' top-level object
	// Other top-level objects (like 'targets') are ignored as they're for external tools
	if err := compilerViper.UnmarshalKey("compiler", &cfg.Compiler); err != nil {
		return nil, fmt.Errorf("failed to unmarshal compiler config: %w", err)
	}

	// Also parse top-level 'targets' field for CFG-guided fuzzing
	// The 'targets' field specifies which source files and functions to focus on
	if compilerViper.IsSet("targets") {
		var targets []TargetFunction
		if err := compilerViper.UnmarshalKey("targets", &targets); err != nil {
			return nil, fmt.Errorf("failed to unmarshal targets config: %w", err)
		}
		cfg.Compiler.Targets = targets
	}

	// Set defaults for fuzz config if not specified
	if cfg.Compiler.Fuzz.OutputRootDir == "" {
		cfg.Compiler.Fuzz.OutputRootDir = "fuzz_out"
	}
	if cfg.Compiler.Fuzz.MaxIterations == 0 {
		cfg.Compiler.Fuzz.MaxIterations = 0 // 0 means unlimited
	}
	if cfg.Compiler.Fuzz.MaxNewSeeds == 0 {
		cfg.Compiler.Fuzz.MaxNewSeeds = 3
	}
	if cfg.Compiler.Fuzz.Timeout == 0 {
		cfg.Compiler.Fuzz.Timeout = 30
	}
	if cfg.Compiler.Fuzz.QEMUPath == "" {
		cfg.Compiler.Fuzz.QEMUPath = "qemu-aarch64"
	}
	if cfg.Compiler.Fuzz.MaxConstraintRetries == 0 {
		cfg.Compiler.Fuzz.MaxConstraintRetries = 32
	}
	if cfg.Compiler.Fuzz.WeightDecayFactor <= 0 || cfg.Compiler.Fuzz.WeightDecayFactor > 1 {
		cfg.Compiler.Fuzz.WeightDecayFactor = 0.8
	}

	// Set defaults for oracle config if not specified
	if cfg.Compiler.Oracle.Type == "" {
		cfg.Compiler.Oracle.Type = "llm"
	}
	if cfg.Compiler.Oracle.Options == nil {
		cfg.Compiler.Oracle.Options = make(map[string]interface{})
	}

	return &cfg, nil
} // GetCompilerConfigName returns the compiler config filename based on the pattern:
// {compiler.name}-v{compiler.version}-{isa}-{strategy}
// For example: gcc-v12.2.0-x64-canary
func GetCompilerConfigName(cfg *Config) string {
	var compilerInfo CompilerInfo
	// Re-load just the compiler section from main config to get name and version
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath("configs")
	v.AddConfigPath("../configs")
	v.AddConfigPath("../../configs")

	if err := v.ReadInConfig(); err == nil {
		// Read from 'config.compiler' path
		v.UnmarshalKey("config.compiler", &compilerInfo)
	}

	return fmt.Sprintf("%s-v%s-%s-%s",
		compilerInfo.Name,
		compilerInfo.Version,
		cfg.ISA,
		cfg.Strategy,
	)
}

// GetCompilerConfigPath returns the full path to the compiler configuration file.
// The path follows the pattern: configs/{compiler}-v{version}-{isa}-{strategy}.yaml
func GetCompilerConfigPath(cfg *Config) (string, error) {
	configName := GetCompilerConfigName(cfg)
	configFile := configName + ".yaml"

	// Try to find the config file in the known paths
	searchPaths := []string{
		"configs",
		"../configs",
		"../../configs",
	}

	for _, basePath := range searchPaths {
		fullPath := filepath.Join(basePath, configFile)
		if _, err := os.Stat(fullPath); err == nil {
			return fullPath, nil
		}
	}

	return "", fmt.Errorf("compiler config file not found: %s", configFile)
}
