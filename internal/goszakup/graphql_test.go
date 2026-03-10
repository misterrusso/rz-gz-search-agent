package goszakup

import (
	"testing"

	"rz_gz_search_agent/internal/model"
)

func TestParseTrdBuyFromData(t *testing.T) {
	data := map[string]any{
		"TrdBuy": []any{
			map[string]any{
				"id":             7079567,
				"numberAnno":     "22465767-TEST-1",
				"nameRu":         "Услуги по письменному переводу",
				"customerNameRu": "АО Тест",
				"publishDate":    "2019-02-10 21:53:23",
				"RefBuyStatus": map[string]any{
					"code": "PublishedPriceOffers",
				},
			},
		},
	}

	lots, statusByID, err := parseTrdBuyFromData(data)
	if err != nil {
		t.Fatalf("parseTrdBuyFromData() error: %v", err)
	}
	if len(lots) != 1 {
		t.Fatalf("unexpected lots count: %d", len(lots))
	}
	if lots[0].ID != "7079567" {
		t.Fatalf("unexpected lot id: %s", lots[0].ID)
	}
	if lots[0].NameRu != "Услуги по письменному переводу" {
		t.Fatalf("unexpected name: %s", lots[0].NameRu)
	}
	if statusByID["7079567"] != "PublishedPriceOffers" {
		t.Fatalf("unexpected status code: %s", statusByID["7079567"])
	}
}

func TestBlacklistAndRelevanceFiltering(t *testing.T) {
	bad := model.Lot{
		NameRu:        "Перевод стрелочный",
		DescriptionRu: "Работы по переводу стрелок",
		Title:         "Перевод стрелочный",
	}
	if !isBlacklistedLot(bad) {
		t.Fatal("expected blacklisted lot")
	}

	good := model.Lot{
		NameRu:        "Услуги по письменному переводу",
		DescriptionRu: "Перевод документов и локализация",
		Title:         "Услуги по письменному переводу",
	}
	if isBlacklistedLot(good) {
		t.Fatal("did not expect blacklist on relevant lot")
	}
	if !isRelevantAnnouncement(good) {
		t.Fatal("expected relevant translation lot")
	}
}
