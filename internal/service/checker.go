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
		result.Errors = append(result.Errors, model.LotError{Stage: "search_lots", Error: err.Error()})
		return result
	}
	result.FoundLots = len(lots)

	for _, lot := range lots {
		if strings.TrimSpace(lot.ID) == "" {
			result.Errors = append(result.Errors, model.LotError{Stage: "validate_lot", Error: "lot has empty ID"})
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
		result.Errors = append(result.Errors, model.LotError{LotID: lot.ID, Stage: "dedup_check", Error: err.Error()})
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
		result.Errors = append(result.Errors, model.LotError{LotID: lot.ID, Stage: "get_documents", Error: err.Error()})
		return err
	}
	spec, ok := filter.SelectBestSpecDocument(docs)
	if !ok {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{LotID: lot.ID, Stage: "select_spec", Error: "no supported document found"})
		return fmt.Errorf("no supported document for lot=%s", lot.ID)
	}

	var docData []byte
	var docMIME string
	err = util.Retry(ctx, 3, 600*time.Millisecond, func() error {
		downloadCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.HTTPTimeoutSeconds)*time.Second)
		defer cancel()
		b, mime, downloadErr := s.ows.DownloadDocument(downloadCtx, spec)
		if downloadErr != nil {
			return downloadErr
		}
		docData = b
		docMIME = mime
		return nil
	})
	if err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{LotID: lot.ID, Stage: "download_document", Error: err.Error()})
		return err
	}

	text, err := files.ExtractText(ctx, spec.Name, docMIME, docData)
	if err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{LotID: lot.ID, Stage: "extract_text", Error: err.Error()})
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
		result.Errors = append(result.Errors, model.LotError{LotID: lot.ID, Stage: "classify", Error: err.Error()})
		return err
	}

	if err := s.stateStore.MarkChecked(ctx, lot.ID, spec.ID, spec.URL, time.Now().UTC()); err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{LotID: lot.ID, Stage: "state_mark_checked", Error: err.Error()})
	}
	if err := s.stateStore.SaveVerdict(ctx, lot.ID, classification.IsTranslationRelated, classification.Confidence, classification.Reason); err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{LotID: lot.ID, Stage: "state_save_verdict", Error: err.Error()})
	}

	result.ProcessedLots++
	if !classification.IsTranslationRelated {
		return nil
	}

	msg := formatTelegramMessage(lot, classification)
	sendCtx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.HTTPTimeoutSeconds)*time.Second)
	errMsg := s.telegram.SendMessage(sendCtx, msg)
	cancel()
	if errMsg != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{LotID: lot.ID, Stage: "telegram_message", Error: errMsg.Error()})
		return errMsg
	}

	if err := s.stateStore.MarkSent(ctx, lot.ID, time.Now().UTC()); err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{LotID: lot.ID, Stage: "state_mark_sent", Error: err.Error()})
	}
	result.SentToTelegram++
	return nil
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
	if len([]rune(desc)) > 320 {
		desc = string([]rune(desc)[:320]) + "..."
	}

	amountCurrency := strings.TrimSpace(l.Currency)
	if amountCurrency == "" {
		amountCurrency = "KZT"
	}

	docLinks := "—"
	if len(l.DocumentURLs) > 0 {
		docLinks = strings.Join(l.DocumentURLs, "\n")
	}

	return fmt.Sprintf(
		"Relevant lot found\n\nНазвание: %s\nНомер объявления: %s\nОрганизатор: %s\nСпособ: %s\nНачало приема: %s\nОкончание приема: %s\nСумма: %.2f %s\nСтатус: %s\nLot ID: %s\nСсылка на объявление: %s\nСсылки на документы:\n%s\nКлючевые слова: %s\nAI reason: %s",
		name,
		l.LotNumber,
		customer,
		l.Method,
		l.StartDateTime,
		l.EndDateTime,
		l.Amount,
		amountCurrency,
		firstNonEmpty(l.StatusText, l.StatusNameRu),
		l.ID,
		l.URL,
		docLinks,
		strings.Join(l.MatchedKeywords, ", "),
		cls.Reason,
	)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
