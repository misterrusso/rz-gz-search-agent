package openai

import (
	"context"
	"strings"

	"rz_gz_search_agent/internal/model"
)

type FakeClassifier struct{}

func NewFakeClassifier() *FakeClassifier {
	return &FakeClassifier{}
}

func (f *FakeClassifier) ClassifyTranslationRelevance(_ context.Context, input model.ClassificationInput) (model.ClassificationResult, error) {
	text := strings.ToLower(input.Lot.Title + " " + input.ExtractedText)
	signals := []string{}
	keywords := []string{"translation", "interpret", "localiz", "notar", "linguistic"}
	matched := 0
	for _, k := range keywords {
		if strings.Contains(text, k) {
			signals = append(signals, k)
			matched++
		}
	}
	conf := 0.2 + float64(matched)*0.2
	if conf > 0.95 {
		conf = 0.95
	}
	return model.ClassificationResult{
		IsTranslationRelated: matched > 0,
		Confidence:           conf,
		Reason:               "fake classifier used in local mode",
		Signals:              signals,
	}, nil
}
