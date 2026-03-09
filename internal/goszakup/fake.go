package goszakup

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"rz_gz_search_agent/internal/model"
)

type FakeClient struct{}

func NewFakeClient() *FakeClient {
	return &FakeClient{}
}

func (f *FakeClient) SearchLots(_ context.Context, keywords []string, from time.Time, _ time.Time, limit int) ([]model.Lot, error) {
	lots := []model.Lot{
		{
			ID:          "fake-lot-1",
			Title:       "Written and oral translation services",
			Customer:    "Demo customer",
			Amount:      2500000,
			Currency:    "KZT",
			URL:         "https://example.local/lots/fake-lot-1",
			PublishedAt: from.Add(10 * time.Minute),
		},
		{
			ID:          "fake-lot-2",
			Title:       "Office paper supply",
			Customer:    "Demo customer 2",
			Amount:      300000,
			Currency:    "KZT",
			URL:         "https://example.local/lots/fake-lot-2",
			PublishedAt: from.Add(20 * time.Minute),
		},
	}
	if len(lots) > limit {
		lots = lots[:limit]
	}
	_ = keywords
	return lots, nil
}

func (f *FakeClient) GetLotDocuments(_ context.Context, lotID string) ([]model.DocumentRef, error) {
	if lotID == "fake-lot-1" {
		return []model.DocumentRef{
			{ID: "doc-1", Name: "technical_specification.docx", URL: "fake://docx/spec1", MIMEType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		}, nil
	}
	return []model.DocumentRef{
		{ID: "doc-2", Name: "commercial_offer.docx", URL: "fake://docx/spec2", MIMEType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	}, nil
}

func (f *FakeClient) DownloadDocument(_ context.Context, doc model.DocumentRef) ([]byte, string, error) {
	var text string
	switch doc.ID {
	case "doc-1":
		text = "Terms of reference: translation of documents, oral interpretation, and localization services."
	case "doc-2":
		text = "Terms of reference: office paper A4 supply."
	default:
		return nil, "", fmt.Errorf("unknown fake doc id: %s", doc.ID)
	}
	b, err := buildSimpleDOCX(text)
	if err != nil {
		return nil, "", err
	}
	return b, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", nil
}

func buildSimpleDOCX(text string) ([]byte, error) {
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)

	contentTypes := `<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`
	rels := `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>` + xmlEscape(text) + `</w:t></w:r></w:p></w:body></w:document>`

	files := map[string]string{
		"[Content_Types].xml": contentTypes,
		"_rels/.rels":         rels,
		"word/document.xml":   doc,
	}
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(body)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func xmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(s)
}
