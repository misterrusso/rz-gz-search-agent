package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"time"
)

type Client interface {
	SendMessage(ctx context.Context, text string) error
	SendDocument(ctx context.Context, filename string, data []byte, caption string) error
}

type BotClient struct {
	token      string
	chatID     string
	enabled    bool
	logger     *slog.Logger
	httpClient *http.Client
}

func NewBotClient(token, chatID string, enabled bool, timeout time.Duration, logger *slog.Logger) *BotClient {
	return &BotClient{
		token:   token,
		chatID:  chatID,
		enabled: enabled,
		logger:  logger,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (b *BotClient) SendMessage(ctx context.Context, text string) error {
	if !b.enabled {
		b.logger.Info("telegram disabled: skipping SendMessage", "text", text)
		return nil
	}
	body := map[string]any{
		"chat_id": b.chatID,
		"text":    text,
	}
	payload, _ := json.Marshal(body)
	u := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return b.do(req)
}

func (b *BotClient) SendDocument(ctx context.Context, filename string, data []byte, caption string) error {
	if !b.enabled {
		b.logger.Info("telegram disabled: skipping SendDocument", "filename", filename, "size_bytes", len(data))
		return nil
	}
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	_ = w.WriteField("chat_id", b.chatID)
	_ = w.WriteField("caption", caption)
	part, err := w.CreateFormFile("document", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	u := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", b.token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return b.do(req)
}

func (b *BotClient) do(req *http.Request) error {
	res, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram request failed: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode >= 300 {
		return fmt.Errorf("telegram status=%d body=%s", res.StatusCode, string(body))
	}
	return nil
}

