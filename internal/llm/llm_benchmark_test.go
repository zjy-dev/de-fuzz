package llm

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zjy-dev/de-fuzz/internal/seed"
)

// BenchmarkDeepSeekClient_GetCompletion benchmarks the GetCompletion method
func BenchmarkDeepSeekClient_GetCompletion(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "benchmark response"}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL, 0.7)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.GetCompletion("benchmark prompt")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDeepSeekClient_Generate benchmarks the Generate method
func BenchmarkDeepSeekClient_Generate(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "int main() { return 0; }"}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL, 0.7)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.Generate("system understanding", "generate code")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDeepSeekClient_Analyze benchmarks the Analyze method
func BenchmarkDeepSeekClient_Analyze(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "analysis result"}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL, 0.7)
	testCases := []seed.TestCase{
		{RunningCommand: "./test", ExpectedResult: "success"},
	}
	testSeed := &seed.Seed{
		ID:        "bench-seed",
		Content:   "int main() { return 0; }",
		TestCases: testCases,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.Analyze("system understanding", "analyze this", testSeed, "feedback")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDeepSeekClient_Mutate benchmarks the Mutate method
func BenchmarkDeepSeekClient_Mutate(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "mutated code"}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL, 0.7)
	mutateTestCases := []seed.TestCase{
		{RunningCommand: "./prog", ExpectedResult: "output"},
	}
	testSeed := &seed.Seed{
		ID:        "bench-seed",
		Content:   "int main() { return 0; }",
		TestCases: mutateTestCases,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.Mutate("system understanding", "mutate this", testSeed)
		if err != nil {
			b.Fatal(err)
		}
	}
}
