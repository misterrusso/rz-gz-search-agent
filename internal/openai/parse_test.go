package openai

import "testing"

func TestParseClassificationResult(t *testing.T) {
	raw := "```json\n{\"is_translation_related\":true,\"confidence\":0.72,\"reason\":\"contains translation scope\",\"signals\":[\"translation\",\"localization\"]}\n```"
	got, err := ParseClassificationResult(raw)
	if err != nil {
		t.Fatalf("ParseClassificationResult() error: %v", err)
	}
	if !got.IsTranslationRelated {
		t.Fatal("expected IsTranslationRelated=true")
	}
	if got.Confidence != 0.72 {
		t.Fatalf("unexpected confidence: %v", got.Confidence)
	}
}

func TestParseClassificationResultInvalid(t *testing.T) {
	_, err := ParseClassificationResult("not a json")
	if err == nil {
		t.Fatal("expected parse error")
	}
}

