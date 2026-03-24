package seed

// TestCase represents a single execution command and its expected outcome.
type TestCase struct {
	RunningCommand string `json:"running command"`
	ExpectedResult string `json:"expected result"`
}

// Seed represents a single test case for the fuzzer.
// It contains the source code and a set of test cases.
type Seed struct {
	Meta             Metadata     // Metadata for lineage tracking and resume
	Content          string       // C source code (source.c)
	TestCases        []TestCase   // Test cases with running commands and expected results
	CFlags           []string     // Additional compiler flags specified by LLM
	FlagProfile      *FlagProfile // Selected compiler flag profile for this seed
	AppliedLLMCFlags []string     // LLM flags that survived conflict filtering for this compile
	DroppedLLMCFlags []string     // LLM flags removed due to profile conflicts for this compile
	LLMCFlagsApplied bool         // Whether CFlags were actually applied during compilation
}
