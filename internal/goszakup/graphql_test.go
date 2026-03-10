package goszakup

import (
	"testing"

	"rz_gz_search_agent/internal/model"
)

func TestParseLotsFromData(t *testing.T) {
	data := map[string]any{
		"Lots": []any{
			map[string]any{
				"id":               7079567,
				"lotNumber":        "22465767-ЗЦПнеГЗ1",
				"nameRu":           "Услуги по письменному переводу",
				"descriptionRu":    "Перевод документов",
				"customerNameRu":   "АО Тест",
				"trdBuyNumberAnno": "3090163-1",
				"trdBuyId":         3090163,
				"amount":           150000,
				"lastUpdateDate":   "2019-02-10 21:53:23",
			},
		},
	}

	lots, err := parseLotsFromData(data)
	if err != nil {
		t.Fatalf("parseLotsFromData() error: %v", err)
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
	if lots[0].TrdBuyID != "3090163" {
		t.Fatalf("unexpected trdBuyId: %s", lots[0].TrdBuyID)
	}
}

func TestBlacklistFiltering(t *testing.T) {
	bad := modelLot("Перевод стрелочный", "Работы по переводу стрелок")
	if !isBlacklistedLot(bad) {
		t.Fatal("expected blacklisted lot")
	}

	good := modelLot("Услуги по письменному переводу", "Перевод документов и локализация")
	if isBlacklistedLot(good) {
		t.Fatal("did not expect blacklist on relevant lot")
	}
	if !isRelevantTranslationLot(good) {
		t.Fatal("expected relevant translation lot")
	}
}

func modelLot(name, description string) model.Lot {
	return model.Lot{
		NameRu:        name,
		DescriptionRu: description,
		Title:         name,
	}
}
