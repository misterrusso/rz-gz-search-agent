package goszakup

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestParsePortalRows(t *testing.T) {
	html := `
<html><body>
<table id="search-result">
<tbody>
<tr>
  <td>16531241-1</td>
  <td><a href="/ru/announce/index/16531241">Услуги перевода с русского на казахский</a><br><small>ТОО Тест</small></td>
  <td>ЗЦП</td>
  <td>2026-03-10 10:00</td>
  <td>2026-03-11 10:00</td>
  <td>150 000 ₸</td>
  <td>Опубликовано</td>
</tr>
</tbody>
</table>
<div>Показано c 1 по 1 из 15 записей</div>
</body></html>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("goquery parse: %v", err)
	}

	rows := parsePortalRows(doc)
	if len(rows) != 1 {
		t.Fatalf("unexpected row count: %d", len(rows))
	}
	if rows[0].AnnounceID != "16531241" {
		t.Fatalf("unexpected announce id: %s", rows[0].AnnounceID)
	}
	if rows[0].NumberAnno != "16531241-1" {
		t.Fatalf("unexpected numberAnno: %s", rows[0].NumberAnno)
	}
	if rows[0].AnnounceURL != "https://goszakup.gov.kz/ru/announce/index/16531241" {
		t.Fatalf("unexpected url: %s", rows[0].AnnounceURL)
	}

	total := parsePortalTotalCount(doc)
	if total != 15 {
		t.Fatalf("unexpected total: %d", total)
	}
}

func TestDocumentLinkDetection(t *testing.T) {
	if !isDocumentLikeLink("https://goszakup.gov.kz/ru/announce/file/123?download=1", "документ") {
		t.Fatal("expected file link recognized")
	}
	if !isDocumentLikeLink("https://example.local/spec.pdf", "spec") {
		t.Fatal("expected pdf link recognized")
	}
	if isDocumentLikeLink("https://goszakup.gov.kz/ru/announce/index/123", "объявление") {
		t.Fatal("did not expect announce page recognized as document")
	}
}
