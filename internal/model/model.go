package model

import "time"

type Lot struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Customer    string    `json:"customer"`
	Amount      float64   `json:"amount"`
	Currency    string    `json:"currency"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"published_at"`
}

type DocumentRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	MIMEType string `json:"mime_type"`
}

type ClassificationInput struct {
	Lot           Lot      `json:"lot"`
	Filename      string   `json:"filename"`
	ExtractedText string   `json:"extracted_text"`
	Keywords      []string `json:"keywords"`
}

type ClassificationResult struct {
	IsTranslationRelated bool     `json:"is_translation_related"`
	Confidence           float64  `json:"confidence"`
	Reason               string   `json:"reason"`
	Signals              []string `json:"signals"`
}

type LotError struct {
	LotID string `json:"lot_id"`
	Stage string `json:"stage"`
	Error string `json:"error"`
}

type CheckRunResult struct {
	OK                 bool       `json:"ok"`
	CheckedFrom        time.Time  `json:"checked_from"`
	CheckedTo          time.Time  `json:"checked_to"`
	Keywords           []string   `json:"keywords"`
	FoundLots          int        `json:"found_lots"`
	ProcessedLots      int        `json:"processed_lots"`
	SentToTelegram     int        `json:"sent_to_telegram"`
	SkippedAsDuplicate int        `json:"skipped_as_duplicate"`
	Errors             []LotError `json:"errors"`
}

