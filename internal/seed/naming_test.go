package seed

import (
	"testing"
)

func TestDefaultNamingStrategy(t *testing.T) {
	namer := NewDefaultNamingStrategy()

	t.Run("should generate correct filename", func(t *testing.T) {
		meta := &Metadata{
			ID:          123,
			ParentID:    42,
			CovIncrease: 132,
		}
		content := "int main() { return 0; }"

		filename := namer.GenerateFilename(meta, content)

		// Check format: id-XXXXXX-src-XXXXXX-cov-XXXXX-XXXXXXXX.seed
		if len(filename) == 0 {
			t.Error("filename should not be empty")
		}

		// Parse it back
		parsed, err := namer.ParseFilename(filename)
		if err != nil {
			t.Errorf("failed to parse generated filename: %v", err)
		}

		if parsed.ID != 123 {
			t.Errorf("expected ID 123, got %d", parsed.ID)
		}
		if parsed.ParentID != 42 {
			t.Errorf("expected ParentID 42, got %d", parsed.ParentID)
		}
		if parsed.CovIncrease != 132 {
			t.Errorf("expected CovIncrease 132, got %d", parsed.CovIncrease)
		}
	})

	t.Run("should parse valid filename", func(t *testing.T) {
		filename := "id-000123-src-000042-cov-00132-a1b2c3d4.seed"

		meta, err := namer.ParseFilename(filename)
		if err != nil {
			t.Errorf("failed to parse filename: %v", err)
		}

		if meta.ID != 123 {
			t.Errorf("expected ID 123, got %d", meta.ID)
		}
		if meta.ParentID != 42 {
			t.Errorf("expected ParentID 42, got %d", meta.ParentID)
		}
		if meta.CovIncrease != 132 {
			t.Errorf("expected CovIncrease 132, got %d", meta.CovIncrease)
		}
		if meta.ContentHash != "a1b2c3d4" {
			t.Errorf("expected ContentHash a1b2c3d4, got %s", meta.ContentHash)
		}
	})

	t.Run("should fail on invalid filename", func(t *testing.T) {
		invalidNames := []string{
			"invalid.seed",
			"id-123-src-42-cov-132-abc.seed",              // Wrong padding
			"id-000123-src-000042-cov-00132.seed",         // Missing hash
			"id-000123-src-000042-cov-00132-a1b2c3d4.txt", // Wrong extension
		}

		for _, name := range invalidNames {
			_, err := namer.ParseFilename(name)
			if err == nil {
				t.Errorf("expected error for invalid filename: %s", name)
			}
		}
	})

	t.Run("should generate consistent hash", func(t *testing.T) {
		content := "int main() { return 0; }"
		hash1 := GenerateContentHash(content)
		hash2 := GenerateContentHash(content)

		if hash1 != hash2 {
			t.Errorf("hash should be consistent: %s != %s", hash1, hash2)
		}

		// Different content should produce different hash
		hash3 := GenerateContentHash("int main() { return 1; }")
		if hash1 == hash3 {
			t.Error("different content should produce different hash")
		}
	})
}
