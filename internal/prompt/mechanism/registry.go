package mechanism

import "fmt"

var registry = map[string]Contract{}

// Register adds c to the global contract registry.
// Panics if a contract with the same OracleType has already been registered.
func Register(c Contract) {
	name := c.OracleType()
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("mechanism: contract %q already registered", name))
	}
	registry[name] = c
}

// Get looks up the contract for the given oracle-type name.
// Returns (nil, false) if no contract is registered under that name.
func Get(name string) (Contract, bool) {
	c, ok := registry[name]
	return c, ok
}

// MustGet returns the contract for name or panics with an informative message.
func MustGet(name string) Contract {
	c, ok := Get(name)
	if !ok {
		panic(fmt.Sprintf("mechanism: no contract registered for %q", name))
	}
	return c
}
