package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"rz_gz_search_agent/internal/model"
)

type Client struct {
	apiKey     string
	model      string
	httpClient *http.Client
	logger     *slog.Logger
}

func NewClient(apiKey, model string, timeout time.Duration, logger *slog.Logger) *Client {
	return &Client{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		logger: logger,
	}
}

func (c *Client) ClassifyTranslationRelevance(ctx context.Context, input model.ClassificationInput) (model.ClassificationResult, error) {
	if strings.TrimSpace(c.apiKey) == "" {
		return model.ClassificationResult{}, fmt.Errorf("OPENAI_API_KEY is empty")
	}

	systemPrompt := `You classify procurement lots.
Return strictly JSON with keys:
is_translation_related (bool), confidence (0..1 number), reason (string), signals (array of short strings).
Relevant categories: written translation, oral interpretation, localization, translation of documents, notarized translation, multilingual language services.
Not relevant: unrelated goods/services.`
	userPayload := map[string]any{
		"lot": map[string]any{
			"id":           input.Lot.ID,
			"title":        input.Lot.Title,
			"customer":     input.Lot.Customer,
			"amount":       input.Lot.Amount,
			"currency":     input.Lot.Currency,
			"url":          input.Lot.URL,
			"published_at": input.Lot.PublishedAt,
		},
		"filename":       input.Filename,
		"keywords":       input.Keywords,
		"extracted_text": input.ExtractedText,
	}
	userJSON, _ := json.Marshal(userPayload)

	reqBody := map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": string(userJSON)},
		},
		"temperature": 0.1,
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return model.ClassificationResult{}, fmt.Errorf("marshal openai request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		return model.ClassificationResult{}, fmt.Errorf("create openai request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return model.ClassificationResult{}, fmt.Errorf("openai request failed: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return model.ClassificationResult{}, fmt.Errorf("read openai response: %w", err)
	}
	if res.StatusCode >= 300 {
		return model.ClassificationResult{}, fmt.Errorf("openai status=%d body=%s", res.StatusCode, string(body))
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &chatResp); err != nil {
		return model.ClassificationResult{}, fmt.Errorf("decode openai response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return model.ClassificationResult{}, fmt.Errorf("openai response has no choices")
	}
	raw := chatResp.Choices[0].Message.Content
	parsed, err := ParseClassificationResult(raw)
	if err != nil {
		c.logger.Error("openai invalid classification json", "error", err.Error(), "raw_response", raw)
		return model.ClassificationResult{}, err
	}
	return parsed, nil
}

