package goszakup

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"

	"rz_gz_search_agent/internal/model"
)

const (
	portalSearchURL      = "https://goszakup.gov.kz/ru/search/announce"
	portalDefaultPerPage = 50
	portalMaxPages       = 20
)

var (
	portalStatuses = []string{"210", "220", "230", "240", "280"}
	totalCountRe   = regexp.MustCompile(`(?i)Показано\s+[сc]\s+\d+\s+по\s+\d+\s+из\s+(\d+)\s+запис`)
)

type PortalSearchClient struct {
	token      string
	logger     *slog.Logger
	httpClient *http.Client

	announceMu sync.RWMutex
	announce   map[string]portalAnnounce
}

type portalAnnounce struct {
	NumberAnno    string
	AnnounceID    string
	Title         string
	Organizer     string
	Method        string
	StartDateTime string
	EndDateTime   string
	AmountRaw     string
	AmountValue   float64
	StatusText    string
	AnnounceURL   string
	DocumentURLs  []string
}

func NewPortalSearchClient(token string, timeout time.Duration, logger *slog.Logger) *PortalSearchClient {
	if logger == nil {
		logger = slog.Default()
	}
	return &PortalSearchClient{
		token:  strings.TrimSpace(token),
		logger: logger,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		announce: make(map[string]portalAnnounce),
	}
}

func (p *PortalSearchClient) SearchLots(ctx context.Context, keywords []string, from time.Time, to time.Time, limit int) ([]model.Lot, error) {
	_ = from
	_ = to

	maxResults := limit
	if maxResults <= 0 {
		maxResults = portalDefaultPerPage
	}

	normalizedKeywords := normalizeKeywords(keywords)
	if len(normalizedKeywords) == 0 {
		normalizedKeywords = append([]string{}, defaultSearchKeywords...)
	}

	dedup := make(map[string]model.Lot)
	for _, keyword := range normalizedKeywords {
		p.logger.Info("portal search keyword", "keyword", keyword, "statuses", strings.Join(portalStatuses, ","))
		for page := 1; page <= portalMaxPages; page++ {
			announces, totalCount, err := p.fetchSearchPage(ctx, keyword, page, portalDefaultPerPage)
			if err != nil {
				return nil, err
			}

			p.logger.Info("portal search page", "keyword", keyword, "page", page, "count_on_page", len(announces), "total_count", totalCount)
			if len(announces) == 0 {
				break
			}

			passedBlacklist := 0
			for _, announce := range announces {
				titleLower := strings.ToLower(strings.TrimSpace(announce.Title))
				if !strings.Contains(titleLower, strings.ToLower(keyword)) {
					continue
				}

				lot := announceToLot(announce)
				if isBlacklistedLot(lot) {
					continue
				}
				passedBlacklist++

				existing, exists := dedup[lot.ID]
				if exists {
					existing.MatchedKeywords = appendUnique(existing.MatchedKeywords, keyword)
					if len(existing.DocumentURLs) == 0 && len(lot.DocumentURLs) > 0 {
						existing.DocumentURLs = lot.DocumentURLs
					}
					dedup[lot.ID] = existing
					continue
				}
				lot.MatchedKeywords = []string{keyword}
				docURLs, err := p.fetchAnnouncementDocuments(ctx, announce.AnnounceURL)
				if err != nil {
					p.logger.Warn("portal announce documents parse failed", "announce_id", announce.AnnounceID, "error", err.Error())
				} else {
					announce.DocumentURLs = append([]string{}, docURLs...)
					lot.DocumentURLs = append([]string{}, docURLs...)
				}
				dedup[lot.ID] = lot
				p.cacheAnnounce(announce)
			}

			p.logger.Info("portal search filtered", "keyword", keyword, "page", page, "passed_blacklist", passedBlacklist, "dedup_total", len(dedup))
			if len(dedup) >= maxResults {
				return sliceLots(dedup, maxResults), nil
			}

			if page*portalDefaultPerPage >= totalCount {
				break
			}
		}
	}

	return sliceLots(dedup, maxResults), nil
}

func (p *PortalSearchClient) GetLotDocuments(ctx context.Context, lotID string) ([]model.DocumentRef, error) {
	announce, ok := p.getAnnounce(lotID)
	if !ok {
		return nil, fmt.Errorf("announcement cache miss for lot_id=%s", lotID)
	}

	docs := []model.DocumentRef{
		{
			ID:       "inline-" + lotID,
			Name:     "announce_" + lotID + ".txt",
			URL:      "inline://announce/" + lotID,
			MIMEType: "text/plain; charset=utf-8",
		},
	}

	for i, docURL := range announce.DocumentURLs {
		name := path.Base(docURL)
		if name == "." || name == "/" || strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("document_%d", i+1)
		}
		docs = append(docs, model.DocumentRef{
			ID:       fmt.Sprintf("doc-%s-%d", lotID, i+1),
			Name:     name,
			URL:      docURL,
			MIMEType: detectDocumentMIME(name),
		})
	}

	p.logger.Info("portal docs resolved", "lot_id", lotID, "docs_count", len(docs))
	return docs, nil
}

func (p *PortalSearchClient) DownloadDocument(ctx context.Context, doc model.DocumentRef) ([]byte, string, error) {
	if strings.HasPrefix(doc.URL, "inline://announce/") {
		lotID := strings.TrimPrefix(doc.URL, "inline://announce/")
		announce, ok := p.getAnnounce(lotID)
		if !ok {
			return nil, "", fmt.Errorf("inline announcement not found for lot_id=%s", lotID)
		}
		text := buildAnnouncementText(announce)
		return []byte(text), "text/plain; charset=utf-8", nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, doc.URL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create portal doc request: %w", err)
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	res, err := p.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download portal doc: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read portal doc response: %w", err)
	}
	if res.StatusCode >= 300 {
		return nil, "", fmt.Errorf("portal doc status=%d body=%s", res.StatusCode, string(body))
	}
	return body, res.Header.Get("Content-Type"), nil
}

func (p *PortalSearchClient) fetchSearchPage(ctx context.Context, keyword string, page int, perPage int) ([]portalAnnounce, int, error) {
	u, err := url.Parse(portalSearchURL)
	if err != nil {
		return nil, 0, err
	}
	q := u.Query()
	q.Set("filter[name]", keyword)
	q.Set("count_record", strconv.Itoa(perPage))
	q.Set("page", strconv.Itoa(page))
	for _, status := range portalStatuses {
		q.Add("filter[status][]", status)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("create portal search request: %w", err)
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}

	res, err := p.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("execute portal search: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		p.logger.Error("portal search non-200", "status", res.StatusCode, "page", page)
		return nil, 0, fmt.Errorf("portal search status=%d body=%s", res.StatusCode, string(body))
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("parse portal html: %w", err)
	}

	totalCount := parsePortalTotalCount(doc)
	results := parsePortalRows(doc)
	return results, totalCount, nil
}

func parsePortalRows(doc *goquery.Document) []portalAnnounce {
	out := make([]portalAnnounce, 0)
	doc.Find("#search-result tbody tr").Each(func(_ int, row *goquery.Selection) {
		cols := row.Find("td")
		if cols.Length() < 7 {
			return
		}

		numberAnno := strings.TrimSpace(cols.Eq(0).Text())
		titleCell := cols.Eq(1)
		link := titleCell.Find("a").First()
		title := strings.TrimSpace(link.Text())
		href, _ := link.Attr("href")
		announceURL := makeAbsolutePortalURL(href)
		announceID := parseAnnounceIDFromURL(announceURL)
		organizer := extractOrganizer(titleCell, title)
		method := strings.TrimSpace(cols.Eq(2).Text())
		startDateTime := strings.TrimSpace(cols.Eq(3).Text())
		endDateTime := strings.TrimSpace(cols.Eq(4).Text())
		amountRaw := strings.TrimSpace(cols.Eq(5).Text())
		statusText := strings.TrimSpace(cols.Eq(6).Text())

		if announceID == "" {
			return
		}

		out = append(out, portalAnnounce{
			NumberAnno:    numberAnno,
			AnnounceID:    announceID,
			Title:         title,
			Organizer:     organizer,
			Method:        method,
			StartDateTime: startDateTime,
			EndDateTime:   endDateTime,
			AmountRaw:     amountRaw,
			AmountValue:   parseAmount(amountRaw),
			StatusText:    statusText,
			AnnounceURL:   announceURL,
		})
	})
	return out
}

func parsePortalTotalCount(doc *goquery.Document) int {
	text := strings.Join(doc.Find("body").Map(func(_ int, s *goquery.Selection) string {
		return s.Text()
	}), " ")
	matches := totalCountRe.FindStringSubmatch(strings.ReplaceAll(text, "\n", " "))
	if len(matches) != 2 {
		return 0
	}
	n, _ := strconv.Atoi(matches[1])
	return n
}

func announceToLot(a portalAnnounce) model.Lot {
	return model.Lot{
		ID:               a.AnnounceID,
		Title:            a.Title,
		Customer:         a.Organizer,
		Amount:           a.AmountValue,
		Currency:         "KZT",
		URL:              a.AnnounceURL,
		PublishedAt:      parseTimeAny(a.StartDateTime),
		LotNumber:        a.NumberAnno,
		NameRu:           a.Title,
		DescriptionRu:    a.Title,
		CustomerNameRu:   a.Organizer,
		TrdBuyNumberAnno: a.NumberAnno,
		TrdBuyID:         a.AnnounceID,
		LastUpdateDate:   a.EndDateTime,
		Method:           a.Method,
		StartDateTime:    a.StartDateTime,
		EndDateTime:      a.EndDateTime,
		StatusText:       a.StatusText,
		DocumentURLs:     append([]string{}, a.DocumentURLs...),
	}
}

func buildAnnouncementText(a portalAnnounce) string {
	lines := []string{
		"Объявление о закупке",
		"Номер: " + a.NumberAnno,
		"ID: " + a.AnnounceID,
		"Название: " + a.Title,
		"Организатор: " + a.Organizer,
		"Способ: " + a.Method,
		"Начало приема: " + a.StartDateTime,
		"Окончание приема: " + a.EndDateTime,
		"Сумма: " + a.AmountRaw,
		"Статус: " + a.StatusText,
		"Ссылка: " + a.AnnounceURL,
	}
	if len(a.DocumentURLs) > 0 {
		lines = append(lines, "Документы:")
		for _, docURL := range a.DocumentURLs {
			lines = append(lines, "- "+docURL)
		}
	}
	return strings.Join(lines, "\n")
}

func detectDocumentMIME(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return "application/pdf"
	case strings.HasSuffix(lower, ".docx"):
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case strings.HasSuffix(lower, ".doc"):
		return "application/msword"
	default:
		return "application/octet-stream"
	}
}

func parseAmount(raw string) float64 {
	normalized := strings.ToLower(raw)
	normalized = strings.ReplaceAll(normalized, "\u00a0", "")
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "₸", "")
	normalized = strings.ReplaceAll(normalized, "тг", "")
	normalized = strings.ReplaceAll(normalized, ",", ".")
	re := regexp.MustCompile(`[-+]?\d+(\.\d+)?`)
	value := re.FindString(normalized)
	if value == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(value, 64)
	return f
}

func makeAbsolutePortalURL(href string) string {
	trimmed := strings.TrimSpace(href)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	return "https://goszakup.gov.kz" + trimmed
}

func parseAnnounceIDFromURL(announceURL string) string {
	if announceURL == "" {
		return ""
	}
	u, err := url.Parse(announceURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

func extractOrganizer(cell *goquery.Selection, title string) string {
	organizer := strings.TrimSpace(cell.Find("small").First().Text())
	if organizer != "" {
		return organizer
	}
	full := strings.TrimSpace(cell.Text())
	full = strings.ReplaceAll(full, title, "")
	full = strings.TrimSpace(strings.ReplaceAll(full, "\n", " "))
	return strings.Join(strings.Fields(full), " ")
}

func (p *PortalSearchClient) fetchAnnouncementDocuments(ctx context.Context, announceURL string) ([]string, error) {
	if strings.TrimSpace(announceURL) == "" {
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, announceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create announce request: %w", err)
	}
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	res, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute announce request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return nil, fmt.Errorf("announce status=%d body=%s", res.StatusCode, string(body))
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		return nil, fmt.Errorf("parse announce html: %w", err)
	}

	links := make(map[string]struct{})
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		abs := makeAbsolutePortalURL(href)
		anchorText := strings.ToLower(strings.TrimSpace(a.Text()))
		if isDocumentLikeLink(abs, anchorText) {
			links[abs] = struct{}{}
		}
	})
	out := make([]string, 0, len(links))
	for link := range links {
		out = append(out, link)
	}
	sort.Strings(out)
	return out, nil
}

func isDocumentLikeLink(u, anchorText string) bool {
	lowerURL := strings.ToLower(strings.TrimSpace(u))
	if lowerURL == "" {
		return false
	}
	if strings.Contains(lowerURL, "/download/") || strings.Contains(lowerURL, "/file/") || strings.Contains(lowerURL, "/files/") {
		return true
	}
	for _, ext := range []string{".pdf", ".doc", ".docx", ".xls", ".xlsx", ".zip", ".rar"} {
		if strings.Contains(lowerURL, ext) {
			return true
		}
	}
	return strings.Contains(anchorText, "тех") ||
		strings.Contains(anchorText, "специ") ||
		strings.Contains(anchorText, "документ")
}

func appendUnique(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func sliceLots(dedup map[string]model.Lot, maxResults int) []model.Lot {
	out := make([]model.Lot, 0, len(dedup))
	for _, lot := range dedup {
		sort.Strings(lot.MatchedKeywords)
		out = append(out, lot)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	if len(out) > maxResults {
		out = out[:maxResults]
	}
	return out
}

func (p *PortalSearchClient) cacheAnnounce(a portalAnnounce) {
	p.announceMu.Lock()
	p.announce[a.AnnounceID] = a
	p.announceMu.Unlock()
}

func (p *PortalSearchClient) getAnnounce(lotID string) (portalAnnounce, bool) {
	p.announceMu.RLock()
	a, ok := p.announce[lotID]
	p.announceMu.RUnlock()
	return a, ok
}
