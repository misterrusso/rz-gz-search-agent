package filter

import (
	"testing"

	"rz_gz_search_agent/internal/model"
)

func TestSelectBestSpecDocument(t *testing.T) {
	docs := []model.DocumentRef{
		{ID: "1", Name: "price_list.pdf"},
		{ID: "2", Name: "technical_specification.docx"},
		{ID: "3", Name: "notes.txt"},
	}
	got, ok := SelectBestSpecDocument(docs)
	if !ok {
		t.Fatal("expected selected document")
	}
	if got.ID != "2" {
		t.Fatalf("unexpected document selected: %s", got.ID)
	}
}
