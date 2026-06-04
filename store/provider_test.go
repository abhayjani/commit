package store

import (
	"path/filepath"
	"testing"
)

// Each provider keeps its own key and model, independently.
func TestProviderAwareKeysAndModels(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if db.GetProvider() != ProviderAnthropic {
		t.Errorf("default provider = %q, want anthropic", db.GetProvider())
	}
	if db.GetModel() != DefaultModel {
		t.Errorf("default model = %q, want %q", db.GetModel(), DefaultModel)
	}

	if err := db.SetAPIKey("sk-ant-xxx"); err != nil {
		t.Fatal(err)
	}

	db.SetProvider(ProviderOpenAI)
	if db.GetModel() != DefaultOpenAIModel {
		t.Errorf("openai default model = %q, want %q", db.GetModel(), DefaultOpenAIModel)
	}
	if db.GetAPIKey() != "" {
		t.Errorf("openai key should start empty, got %q", db.GetAPIKey())
	}
	if err := db.SetAPIKey("sk-oai-yyy"); err != nil {
		t.Fatal(err)
	}
	db.SetModel("gpt-4o")
	if db.GetAPIKey() != "sk-oai-yyy" || db.GetModel() != "gpt-4o" {
		t.Errorf("openai key/model = %q/%q", db.GetAPIKey(), db.GetModel())
	}

	// Switching back preserves Anthropic's key + model independently.
	db.SetProvider(ProviderAnthropic)
	if db.GetAPIKey() != "sk-ant-xxx" {
		t.Errorf("anthropic key after round-trip = %q", db.GetAPIKey())
	}
	if db.GetModel() != DefaultModel {
		t.Errorf("anthropic model after round-trip = %q", db.GetModel())
	}
}
