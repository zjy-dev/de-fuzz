package llm

// import "defuzz/internal/seed"

// // Analysis holds the structured analysis from the LLM about a seed's execution.
// type Analysis struct {
// 	IsBug         bool   // IsBug is true if the LLM identified a bug.
// 	Description   string // Description contains an explanation of the bug.
// 	ShouldDiscard bool   // ShouldDiscard is true if the LLM suggests discarding the seed.
// }

// // LLM defines the interface for interacting with a Large Language Model.
// type LLM interface {
// 	// UnderstandPrompt sends the initial, detailed prompt to the LLM and returns
// 	// a unique context identifier for the conversation.
// 	UnderstandPrompt(prompt string) (string, error)

// 	// GenerateInitialSeeds asks the LLM to generate n initial seeds based on the
// 	// provided context.
// 	GenerateInitialSeeds(ctxID string, n int) ([]seed.Seed, error)

// 	// AnalyzeFeedback sends the seed and its execution feedback to the LLM for analysis.
// 	AnalyzeFeedback(ctxID string, s seed.Seed, feedback string) (*Analysis, error)

// 	// MutateSeed asks the LLM to mutate a given seed to create a new variant.
// 	MutateSeed(ctxID string, s seed.Seed) (*seed.Seed, error)
// }
