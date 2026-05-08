//go:build test

package mechanism

// NewFakeContract returns a no-op Contract suitable for use in tests that
// exercise prompt-building logic without depending on a real mechanism.
func NewFakeContract() Contract {
	return &fakeContract{}
}

type fakeContract struct{}

func (f *fakeContract) OracleType() string                    { return "fake" }
func (f *fakeContract) FunctionTemplatePath(isa string) string { return "" }
func (f *fakeContract) PlaceholderFunctionName() string        { return "seed" }
func (f *fakeContract) RequiredMarkers() []string              { return nil }
func (f *fakeContract) FuzzTimePromptExample() string          { return "" }
func (f *fakeContract) CriticalRulesAddendum() string          { return "" }
