package oracle

import (
	"fmt"
)

// OracleFactory is a function that creates a new Oracle instance.
// It receives the configuration options for the oracle.
//
// Oracles are deterministic by contract: no LLM or prompt dependencies are
// injected. Any verdict must derive solely from static/dynamic analysis of
// the build artifacts.
type OracleFactory func(options map[string]interface{}) (Oracle, error)

var (
	registry = make(map[string]OracleFactory)
)

// Register adds an oracle factory to the registry.
func Register(name string, factory OracleFactory) {
	registry[name] = factory
}

// New creates an oracle instance by name.
func New(name string, options map[string]interface{}) (Oracle, error) {
	factory, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("oracle plugin not found: %s", name)
	}
	return factory(options)
}
