package llm

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// setupBenchRemixer creates a mock OpenAI server and a temporary remixer config,
// then returns a RemixerClient and a cleanup function.
func setupBenchRemixer(b *testing.B) (*RemixerClient, func()) {
	b.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "int main() { return 0; }"}}]}`))
	}))

	tmpDir, err := os.MkdirTemp("", "bench_remixer_*")
	if err != nil {
		b.Fatal(err)
	}
	configContent := "models:\n  - name: \"mock\"\n    weight: 1\n    providers:\n      - type: \"openai\"\n        endpoint: \"" + server.URL + "\"\n        model: \"mock\"\n        api_key: \"test-key\"\n"
	configPath := filepath.Join(tmpDir, "remixer.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		b.Fatal(err)
	}

	client, err := NewRemixerClient(configPath, 0.7)
	if err != nil {
		b.Fatal(err)
	}

	cleanup := func() {
		server.Close()
		os.RemoveAll(tmpDir)
	}
	return client, cleanup
}

func BenchmarkRemixerClient_GetCompletion(b *testing.B) {
	client, cleanup := setupBenchRemixer(b)
	defer cleanup()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.GetCompletion("benchmark prompt")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRemixerClient_Generate(b *testing.B) {
	client, cleanup := setupBenchRemixer(b)
	defer cleanup()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.Generate("system understanding", "generate code")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRemixerClient_Analyze(b *testing.B) {
	client, cleanup := setupBenchRemixer(b)
	defer cleanup()

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 1},
		Content: "int main() { return 0; }",
		TestCases: []seed.TestCase{
			{RunningCommand: "./test", ExpectedResult: "success"},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.Analyze("system understanding", "analyze this", testSeed, "feedback")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRemixerClient_Mutate(b *testing.B) {
	client, cleanup := setupBenchRemixer(b)
	defer cleanup()

	testSeed := &seed.Seed{
		Meta:    seed.Metadata{ID: 1},
		Content: "int main() { return 0; }",
		TestCases: []seed.TestCase{
			{RunningCommand: "./prog", ExpectedResult: "output"},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.Mutate("system understanding", "mutate this", testSeed)
		if err != nil {
			b.Fatal(err)
		}
	}
}
