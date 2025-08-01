package llm

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"defuzz/internal/seed"
)

// BenchmarkDeepSeekClient_GetCompletion benchmarks the GetCompletion method
func BenchmarkDeepSeekClient_GetCompletion(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "benchmark response"}}]}`))
	}))
	defer server.Close()

	client := NewDeepSeekClient("test_key", "test_model", server.URL)

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

	client := NewDeepSeekClient("test_key", "test_model", server.URL)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.Generate("generate code", seed.SeedTypeC)
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

	client := NewDeepSeekClient("test_key", "test_model", server.URL)
	testSeed := &seed.Seed{
		ID:      "bench-seed",
		Type:    "c",
		Content: "int main() { return 0; }",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.Analyze("analyze this", testSeed, "feedback")
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

	client := NewDeepSeekClient("test_key", "test_model", server.URL)
	testSeed := &seed.Seed{
		ID:       "bench-seed",
		Type:     "c",
		Content:  "int main() { return 0; }",
		Makefile: "all:\n\tgcc source.c -o prog",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.Mutate("mutate this", testSeed)
		if err != nil {
			b.Fatal(err)
		}
	}
}
