package seed

import "sync"

// SeedType defines the type of a seed.
type SeedType string

const (
	// SeedTypeC represents a C source file compiled directly to a binary.
	SeedTypeC SeedType = "c"
	// SeedTypeCAsm represents a C source file compiled to assembly,
	// which is then fine-tuned by the LLM before being compiled to a binary.
	SeedTypeCAsm SeedType = "c-asm"
	// SeedTypeAsm represents an assembly source file compiled to a binary.
	SeedTypeAsm SeedType = "asm"
)

// Seed represents a single test case for the fuzzer.
type Seed struct {
	ID       string
	Type     SeedType
	Content  string
	Makefile string
}

// Pool manages the collection of seeds for a fuzzing session.
type Pool interface {
	// Add adds a new seed to the pool.
	Add(s *Seed)
	// Next retrieves the next seed from the pool. Returns nil if the pool is empty.
	Next() *Seed
	// Len returns the current number of seeds in the pool.
	Len() int
}

// InMemoryPool is a simple, in-memory implementation of the Pool interface.
// It is safe for concurrent use.
type InMemoryPool struct {
	mu    sync.Mutex
	seeds []*Seed
}

// NewInMemoryPool creates a new, empty in-memory seed pool.
func NewInMemoryPool() *InMemoryPool {
	return &InMemoryPool{
		seeds: make([]*Seed, 0),
	}
}

// Add adds a new seed to the pool.
func (p *InMemoryPool) Add(s *Seed) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seeds = append(p.seeds, s)
}

// Next retrieves and removes the next seed from the pool (FIFO).
// It returns nil if the pool is empty.
func (p *InMemoryPool) Next() *Seed {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.seeds) == 0 {
		return nil
	}
	s := p.seeds[0]
	p.seeds = p.seeds[1:]
	return s
}

// Len returns the current number of seeds in the pool.
func (p *InMemoryPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.seeds)
}
