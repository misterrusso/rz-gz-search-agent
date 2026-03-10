//internal/service/checker.go

package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"rz_gz_search_agent/internal/config"
	"rz_gz_search_agent/internal/files"
	"rz_gz_search_agent/internal/filter"
	"rz_gz_search_agent/internal/goszakup"
	"rz_gz_search_agent/internal/model"
	"rz_gz_search_agent/internal/state"
	"rz_gz_search_agent/internal/telegram"
	"rz_gz_search_agent/internal/util"
)

type Classifier interface {
	ClassifyTranslationRelevance(ctx context.Context, input model.ClassificationInput) (model.ClassificationResult, error)
}

type CheckerService struct {
	cfg        config.Config
	logger     *slog.Logger
	ows        goszakup.Client
	stateStore state.Store
	classifier Classifier
	telegram   telegram.Client
}

func NewCheckerService(
	cfg config.Config,
	logger *slog.Logger,
	ows goszakup.Client,
	stateStore state.Store,
	classifier Classifier,
	tg telegram.Client,
) *CheckerService {
	return &CheckerService{
		cfg:        cfg,
		logger:     logger,
		ows:        ows,
		stateStore: stateStore,
		classifier: classifier,
		telegram:   tg,
	}
}

func (s *CheckerService) RunCheck(ctx context.Context) model.CheckRunResult {
	now := time.Now().UTC()
	from := now.Add(-time.Duration(s.cfg.SearchWindowMinutes) * time.Minute)

	result := model.CheckRunResult{
		OK:          true,
		CheckedFrom: from,
		CheckedTo:   now,
		Keywords:    s.cfg.SearchKeywords,
	}

	searchCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.HTTPTimeoutSeconds)*time.Second)
	lots, err := s.ows.SearchLots(searchCtx, s.cfg.SearchKeywords, from, now, s.cfg.MaxLotsPerRun)
	cancel()
	if err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{
			Stage: "search_lots",
			Error: err.Error(),
		})
		return result
	}

	result.FoundLots = len(lots)

	for _, lot := range lots {
		if strings.TrimSpace(lot.ID) == "" {
			result.Errors = append(result.Errors, model.LotError{
				Stage: "validate_lot",
				Error: "lot has empty ID",
			})
			result.OK = false
			continue
		}
		if err := s.processLot(ctx, lot, &result); err != nil {
			s.logger.Error("lot processing failed", "lot_id", lot.ID, "error", err.Error())
		}
	}

	return result
}

func (s *CheckerService) processLot(ctx context.Context, lot model.Lot, result *model.CheckRunResult) error {
	processed, err := s.stateStore.IsProcessed(ctx, lot.ID)
	if err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{
			LotID: lot.ID,
			Stage: "dedup_check",
			Error: err.Error(),
		})
		return err
	}
	if processed {
		result.SkippedAsDuplicate++
		return nil
	}

	docListCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.HTTPTimeoutSeconds)*time.Second)
	docs, err := s.ows.GetLotDocuments(docListCtx, lot.ID)
	cancel()
	if err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{
			LotID: lot.ID,
			Stage: "get_documents",
			Error: err.Error(),
		})
		return err
	}

	spec, ok := selectTechnicalSpecificationOnly(docs)
	if !ok {
		s.logger.Info("technical specification not found, skipping lot", "lot_id", lot.ID)
		return nil
	}

	var docData []byte
	var docMIME string

	err = util.Retry(ctx, 3, 800*time.Millisecond, func() error {
		downloadCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.HTTPTimeoutSeconds)*time.Second)
		defer cancel()

		b, mime, err := s.ows.DownloadDocument(downloadCtx, spec)
		if err != nil {
			return err
		}
		docData = b
		docMIME = mime
		return nil
	})
	if err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{
			LotID: lot.ID,
			Stage: "download_document",
			Error: err.Error(),
		})
		return err
	}

	text, err := files.ExtractText(ctx, spec.Name, docMIME, docData)
	if err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{
			LotID: lot.ID,
			Stage: "extract_text",
			Error: err.Error(),
		})
		return err
	}
	text = s.truncateTextIfNeeded(lot.ID, text)

	classifyCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.HTTPTimeoutSeconds)*time.Second)
	classification, err := s.classifier.ClassifyTranslationRelevance(classifyCtx, model.ClassificationInput{
		Lot:           lot,
		Filename:      spec.Name,
		ExtractedText: text,
		Keywords:      s.cfg.SearchKeywords,
	})
	cancel()
	if err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{
			LotID: lot.ID,
			Stage: "classify",
			Error: err.Error(),
		})
		return err
	}

	if err := s.stateStore.MarkChecked(ctx, lot.ID, spec.ID, spec.URL, time.Now().UTC()); err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{
			LotID: lot.ID,
			Stage: "state_mark_checked",
			Error: err.Error(),
		})
	}
	if err := s.stateStore.SaveVerdict(ctx, lot.ID, classification.IsTranslationRelated, classification.Confidence, classification.Reason); err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{
			LotID: lot.ID,
			Stage: "state_save_verdict",
			Error: err.Error(),
		})
	}

	result.ProcessedLots++

	if classification.IsTranslationRelated && classification.Confidence >= 0.60 {
		msg := formatTelegramMessage(lot, classification)

		sendCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.HTTPTimeoutSeconds)*time.Second)
		errMsg := s.telegram.SendMessage(sendCtx, msg)
		cancel()
		if errMsg != nil {
			result.OK = false
			result.Errors = append(result.Errors, model.LotError{
				LotID: lot.ID,
				Stage: "telegram_message",
				Error: errMsg.Error(),
			})
			return errMsg
		}

		sendDocCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.HTTPTimeoutSeconds)*time.Second)
		errDoc := s.telegram.SendDocument(sendDocCtx, safeFilename(spec.Name, spec.ID), docData, "Technical specification")
		cancel()
		if errDoc != nil {
			result.OK = false
			result.Errors = append(result.Errors, model.LotError{
				LotID: lot.ID,
				Stage: "telegram_document",
				Error: errDoc.Error(),
			})
			return errDoc
		}

		if err := s.stateStore.MarkSent(ctx, lot.ID, time.Now().UTC()); err != nil {
			result.OK = false
			result.Errors = append(result.Errors, model.LotError{
				LotID: lot.ID,
				Stage: "state_mark_sent",
				Error: err.Error(),
			})
		}
		result.SentToTelegram++
	}

	return nil
}

func selectTechnicalSpecificationOnly(docs []model.DocumentRef) (model.DocumentRef, bool) {
	if len(docs) == 0 {
		return model.DocumentRef{}, false
	}

	if spec, ok := filter.SelectBestSpecDocument(docs); ok && isTechnicalSpecificationName(spec.Name) {
		return spec, true
	}

	for _, doc := range docs {
		if isTechnicalSpecificationName(doc.Name) {
			return doc, true
		}
	}

	return model.DocumentRef{}, false
}

func isTechnicalSpecificationName(name string) bool {
	n := normalizeDocumentName(name)
	if n == "" {
		return false
	}

	positive := []string{
		"техническая спецификация",
		"тех спецификация",
		"техспецификация",
		"тех. спецификация",
		"technical specification",
	}

	for _, token := range positive {
		if strings.Contains(n, token) {
			return true
		}
	}
	return false
}

func normalizeDocumentName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, "_", " ")
	n = strings.ReplaceAll(n, "-", " ")
	for strings.Contains(n, "  ") {
		n = strings.ReplaceAll(n, "  ", " ")
	}
	return n
}

func (s *CheckerService) truncateTextIfNeeded(lotID string, text string) string {
	runes := []rune(text)
	if len(runes) <= s.cfg.OpenAIMaxTextChars {
		return text
	}
	s.logger.Info("truncating extracted text", "lot_id", lotID, "original_chars", len(runes), "limit", s.cfg.OpenAIMaxTextChars)
	return string(runes[:s.cfg.OpenAIMaxTextChars])
}

func formatTelegramMessage(l model.Lot, cls model.ClassificationResult) string {
	name := firstNonEmpty(l.NameRu, l.Title)
	customer := firstNonEmpty(l.CustomerNameRu, l.Customer)
	desc := strings.TrimSpace(l.DescriptionRu)
	if len([]rune(desc)) > 280 {
		desc = string([]rune(desc)[:280]) + "..."
	}
	amountCurrency := strings.TrimSpace(l.Currency)
	if amountCurrency == "" {
		amountCurrency = "KZT"
	}

	return fmt.Sprintf(
		"Relevant lot found\n\nLot ID: %s\nLot number: %s\nName: %s\nDescription: %s\nCustomer: %s\nTrdBuy number: %s\nTrdBuy ID: %s\nAmount: %.2f %s\nLast update: %s\nURL: %s\nMatched keywords: %s\nConfidence: %.2f\nAI summary: %s",
		l.ID,
		l.LotNumber,
		name,
		desc,
		customer,
		l.TrdBuyNumberAnno,
		l.TrdBuyID,
		l.Amount,
		amountCurrency,
		l.LastUpdateDate,
		l.URL,
		strings.Join(l.MatchedKeywords, ", "),
		cls.Confidence,
		cls.Reason,
	)
}

func safeFilename(name, fallbackID string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		return fallbackID + ".bin"
	}
	return n
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}