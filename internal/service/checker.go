//internal/service/checker.go

package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"rz_gz_search_agent/internal/config"
	"rz_gz_search_agent/internal/goszakup"
	"rz_gz_search_agent/internal/model"
	"rz_gz_search_agent/internal/state"
	"rz_gz_search_agent/internal/telegram"
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

	msg := formatTelegramMessage(lot)

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

	now := time.Now().UTC()
	if err := s.stateStore.MarkChecked(ctx, lot.ID, "", lot.URL, now); err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{
			LotID: lot.ID,
			Stage: "state_mark_checked",
			Error: err.Error(),
		})
	}
	if err := s.stateStore.MarkSent(ctx, lot.ID, now); err != nil {
		result.OK = false
		result.Errors = append(result.Errors, model.LotError{
			LotID: lot.ID,
			Stage: "state_mark_sent",
			Error: err.Error(),
		})
	}

	result.ProcessedLots++
	result.SentToTelegram++
	return nil
}

func formatTelegramMessage(l model.Lot) string {
	name := firstNonEmpty(l.NameRu, l.Title)
	customer := firstNonEmpty(l.CustomerNameRu, l.Customer)
	desc := strings.TrimSpace(l.DescriptionRu)
	if len([]rune(desc)) > 500 {
		desc = string([]rune(desc)[:500]) + "..."
	}
	amountCurrency := strings.TrimSpace(l.Currency)
	if amountCurrency == "" {
		amountCurrency = "KZT"
	}

	return fmt.Sprintf(
		"Найден релевантный лот\n\nLot ID: %s\nНомер лота: %s\nНаименование: %s\nОписание: %s\nЗаказчик: %s\nНомер объявления: %s\nTrdBuy ID: %s\nСумма: %.2f %s\nДата обновления: %s\nURL: %s\nКлючевые слова: %s",
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