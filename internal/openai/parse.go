package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"rz_gz_search_agent/internal/model"
)

func ParseClassificationResult(raw string) (model.ClassificationResult, error) {
	jsonPart, err := extractJSON(raw)
	if err != nil {
		return model.ClassificationResult{}, err
	}
	var out model.ClassificationResult
	if err := json.Unmarshal([]byte(jsonPart), &out); err != nil {
		return model.ClassificationResult{}, fmt.Errorf("invalid classifier JSON: %w", err)
	}
	if out.Confidence < 0 {
		out.Confidence = 0
	}
	if out.Confidence > 1 {
		out.Confidence = 1
	}
	if strings.TrimSpace(out.Reason) == "" {
		return model.ClassificationResult{}, errors.New("classifier JSON missing reason")
	}
	return out, nil
}

func extractJSON(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("empty model response")
	}
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimSuffix(trimmed, "```")
		trimmed = strings.TrimSpace(trimmed)
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end < 0 || end <= start {
		return "", errors.New("no JSON object found in model response")
	}
	return trimmed[start : end+1], nil
}

