package oracle

import (
	"fmt"

	"github.com/zjy-dev/de-fuzz/internal/llm"
	"github.com/zjy-dev/de-fuzz/internal/prompt"
)

// OracleFactory is a function that creates a new Oracle instance.
// It receives the configuration options and necessary dependencies.
// Note: Dependencies like LLM and PromptBuilder might be nil if the oracle doesn't need them.
type OracleFactory func(options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error)

var (
	registry = make(map[string]OracleFactory)
)

// Register adds an oracle factory to the registry.
func Register(name string, factory OracleFactory) {
	registry[name] = factory
}

// New creates an oracle instance by name.
func New(name string, options map[string]interface{}, l llm.LLM, prompter *prompt.Builder, context string) (Oracle, error) {
	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("oracle plugin not found: %s", name)
	}
	return factory(options, l, prompter, context)
}
