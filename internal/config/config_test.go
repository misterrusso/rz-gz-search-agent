//internal/config/config_test.go
package config

import (
	"os"
	"reflect"
	"testing"
)

func TestParseKeywords(t *testing.T) {
	got := ParseKeywords("перевод")
	want := []string{"перевод"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected keywords: got=%v want=%v", got, want)
	}
}

func TestLoad(t *testing.T) {
	t.Setenv("SEARCH_KEYWORDS", "перевод, письменный перевод")
	t.Setenv("STATE_BACKEND", "sqlite")
	t.Setenv("PORT", "9090")
	t.Setenv("OPENAI_MODEL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Port != "9090" {
		t.Fatalf("unexpected port: %s", cfg.Port)
	}
	if cfg.OpenAIModel != "gpt-4o-mini" {
		t.Fatalf("unexpected default model: %s", cfg.OpenAIModel)
	}
}

func TestLoadRequiresKeywords(t *testing.T) {
	_ = os.Unsetenv("SEARCH_KEYWORDS")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error when SEARCH_KEYWORDS missing")
	}
}
