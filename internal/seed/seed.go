package seed

import "sync"

// TestCase represents a single execution command and its expected outcome.
type TestCase struct {
	RunningCommand string `json:"running command"`
	ExpectedResult string `json:"expected result"`
}

// Seed represents a single test case for the fuzzer.
// It contains the source code and a set of test cases.
type Seed struct {
	ID        string // Unique identifier for the seed
	Content   string // C source code (source.c)
	TestCases []TestCase
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
