// internal/goszakup/graphql.go
package goszakup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"rz_gz_search_agent/internal/model"
)

const (
	queryModeNormal  = "normal"
	queryModeMinimal = "minimal"

	graphqlSearchTrdBuyQuery = `
query SearchTrdBuy($limit: Int, $after: Int, $filter: TrdBuyFiltersInput) {
  TrdBuy(filter: $filter, limit: $limit, after: $after) {
    id
    numberAnno
    nameRu
    customerNameRu
    publishDate
    refBuyStatusId
    RefBuyStatus {
      id
      code
      nameRu
      nameKz
    }
  }
}`

	graphqlSearchTrdBuyMinimalQuery = `
query SearchTrdBuyMinimal($limit: Int) {
  TrdBuy(limit: $limit) {
    id
    numberAnno
    nameRu
    customerNameRu
    publishDate
    refBuyStatusId
    RefBuyStatus {
      id
      code
      nameRu
      nameKz
    }
  }
}`
)

var (
	defaultSearchKeywords = []string{
		"услуги перевода",
		"услуги по переводу",
		"письменный перевод",
		"устный перевод",
		"синхронный перевод",
		"последовательный перевод",
		"нотариальный перевод",
		"перевод документов",
		"перевод текстов",
	}
	blacklistTokens = []string{
		"стрелоч",
		"перевод стрелочный",
		"лесного фонда",
		"земли других категорий",
		"землеустро",
		"земельн",
		"марка 1/9",
		"марка 1/7",
		"р-65",
		"р-50",
	}
	relevanceTokens = []string{
		"услуги по письменному переводу",
		"услуги перевода",
		"услуги по переводу",
		"письменный перевод",
		"перевод документов",
		"перевод текстов",
		"синхронный перевод",
		"последовательный перевод",
		"устный перевод",
		"нотариальный перевод",
		"локализац",
		"перевод",
		"переводчес",
	}
	allowedAnnouncementStatusCodes = map[string]struct{}{
		"Published":            {},
		"PublishedPriceOffers": {},
		"PublishedBidAccept":   {},
		"PublishedBidAdditional": {},
		"PublishedAuction":     {},
	}
)

type GraphQLClient struct {
	url        string
	token      string
	queryMode  string
	logger     *slog.Logger
	httpClient *http.Client

	lotTextMu sync.RWMutex
	lotText   map[string]string

	lotDocsMu sync.RWMutex
	lotDocs   map[string][]model.DocumentRef
}

type gqlPageInfo struct {
	LimitPage     int
	TotalCount    int
	HasNextPage   bool
	LastID        int
	LastIndexDate string
}

type matchedLot struct {
	lot     model.Lot
	matched map[string]struct{}
}

type gqlEnvelope struct {
	Data       map[string]any `json:"data"`
	Errors     any            `json:"errors"`
	Extensions map[string]any `json:"extensions"`
}

func NewGraphQLClient(url, token, queryMode string, timeout time.Duration, logger *slog.Logger) *GraphQLClient {
	if logger == nil {
		logger = slog.Default()
	}
	if queryMode != queryModeMinimal {
		queryMode = queryModeNormal
	}
	return &GraphQLClient{
		url:       url,
		token:     token,
		queryMode: queryMode,
		logger:    logger,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		lotText: make(map[string]string),
		lotDocs: make(map[string][]model.DocumentRef),
	}
}

func (g *GraphQLClient) SearchLots(ctx context.Context, keywords []string, from time.Time, to time.Time, limit int) ([]model.Lot, error) {
	maxResults := limit
	if maxResults <= 0 {
		maxResults = 50
	}

	queryKeywords := normalizeKeywords(keywords)
	if len(queryKeywords) == 0 {
		queryKeywords = append([]string{}, defaultSearchKeywords...)
	}

	if g.queryMode == queryModeMinimal {
		return g.searchAnnouncementsMinimal(ctx, from, to, maxResults)
	}
	return g.searchAnnouncementsByKeywords(ctx, queryKeywords, from, to, maxResults)
}

func (g *GraphQLClient) searchAnnouncementsMinimal(ctx context.Context, from time.Time, to time.Time, maxResults int) ([]model.Lot, error) {
	const pageLimit = 20

	vars := map[string]any{"limit": pageLimit}
	page, statusByID, pageInfo, err := g.queryTrdBuyPage(ctx, "SearchTrdBuyMinimal", "TrdBuy", graphqlSearchTrdBuyMinimalQuery, vars)
	if err != nil {
		return nil, err
	}

	g.logger.Info("ows announcements page",
		"mode", queryModeMinimal,
		"results", len(page),
		"has_next_page", pageInfo.HasNextPage,
		"last_id", pageInfo.LastID,
	)

	filtered := make([]model.Lot, 0, len(page))
	for _, item := range page {
		if !isAllowedAnnouncementStatusCode(statusByID[item.ID]) {
			continue
		}
		if isBlacklistedLot(item) || !isRelevantAnnouncement(item) {
			continue
		}
		if !withinWindow(item.PublishedAt, from, to) {
			continue
		}
		filtered = append(filtered, item)
		g.cacheLotText(item)
		if len(filtered) >= maxResults {
			break
		}
	}
	return filtered, nil
}

func (g *GraphQLClient) searchAnnouncementsByKeywords(ctx context.Context, keywords []string, from time.Time, to time.Time, maxResults int) ([]model.Lot, error) {
	const (
		pageLimit = 20
		maxPages  = 50
	)

	collected := make(map[string]*matchedLot)

	for _, keyword := range keywords {
		after := 0
		seenAfter := map[int]struct{}{0: {}}

		for pageNum := 1; pageNum <= maxPages; pageNum++ {
			vars := map[string]any{
				"limit": pageLimit,
				"after": after,
			}

			pageItems, statusByID, pageInfo, err := g.queryTrdBuyPage(ctx, "SearchTrdBuy", "TrdBuy", graphqlSearchTrdBuyQuery, vars)
			if err != nil {
				return nil, err
			}

			g.logger.Info("ows announcement keyword page",
				"keyword", keyword,
				"page", pageNum,
				"results", len(pageItems),
				"last_id", pageInfo.LastID,
				"has_next_page", pageInfo.HasNextPage,
			)

			filteredCount := 0
			for _, item := range pageItems {
				if !isAllowedAnnouncementStatusCode(statusByID[item.ID]) {
					continue
				}
				if isBlacklistedLot(item) || !isRelevantAnnouncement(item) {
					continue
				}
				if !announcementContainsKeyword(item, keyword) {
					continue
				}
				if !withinWindow(item.PublishedAt, from, to) {
					continue
				}

				filteredCount++

				entry, exists := collected[item.ID]
				if !exists {
					copyItem := item
					entry = &matchedLot{
						lot:     copyItem,
						matched: map[string]struct{}{},
					}
					collected[item.ID] = entry
					g.cacheLotText(item)
				}
				entry.matched[keyword] = struct{}{}
			}

			g.logger.Info("ows announcement keyword page filtered",
				"keyword", keyword,
				"filtered", filteredCount,
				"dedup_total", len(collected),
			)

			if !pageInfo.HasNextPage {
				break
			}
			if pageInfo.LastID <= 0 {
				g.logger.Warn("stopping pagination due to invalid last_id",
					"keyword", keyword,
					"last_id", pageInfo.LastID,
				)
				break
			}
			if _, seen := seenAfter[pageInfo.LastID]; seen {
				g.logger.Warn("stopping pagination due to repeated last_id",
					"keyword", keyword,
					"last_id", pageInfo.LastID,
				)
				break
			}

			seenAfter[pageInfo.LastID] = struct{}{}
			after = pageInfo.LastID
		}
	}

	items := finalizeMatchedLots(collected, maxResults)
	if len(items) == 0 {
		return items, nil
	}
	if len(items) > maxResults {
		items = items[:maxResults]
	}
	return items, nil
}

func finalizeMatchedLots(collected map[string]*matchedLot, maxResults int) []model.Lot {
	out := make([]model.Lot, 0, len(collected))
	for _, item := range collected {
		lot := item.lot
		lot.MatchedKeywords = sortedSetKeys(item.matched)
		out = append(out, lot)
	}
	sort.Slice(out, func(i, j int) bool {
		ii, errI := strconv.Atoi(out[i].ID)
		jj, errJ := strconv.Atoi(out[j].ID)
		if errI == nil && errJ == nil {
			return ii < jj
		}
		return out[i].ID < out[j].ID
	})
	if maxResults > 0 && len(out) > maxResults {
		out = out[:maxResults]
	}
	return out
}

func isAllowedAnnouncementStatusCode(code string) bool {
	_, ok := allowedAnnouncementStatusCodes[strings.TrimSpace(code)]
	return ok
}

func withinWindow(t time.Time, from time.Time, to time.Time) bool {
	if t.IsZero() {
		return true
	}
	if !from.IsZero() && t.Before(from) {
		return false
	}
	if !to.IsZero() && t.After(to) {
		return false
	}
	return true
}

func (g *GraphQLClient) queryTrdBuyPage(ctx context.Context, operationName, rootField, query string, variables map[string]any) ([]model.Lot, map[string]string, gqlPageInfo, error) {
	envelope, err := g.queryEnvelope(ctx, operationName, rootField, query, variables)
	if err != nil {
		return nil, nil, gqlPageInfo{}, err
	}

	items, statusByID, err := parseTrdBuyFromData(envelope.Data)
	if err != nil {
		return nil, nil, gqlPageInfo{}, fmt.Errorf("parse trdbuy: %w", err)
	}

	pageInfo := parsePageInfo(envelope.Extensions)
	return items, statusByID, pageInfo, nil
}

func (g *GraphQLClient) GetLotDocuments(_ context.Context, lotID string) ([]model.DocumentRef, error) {
	g.lotDocsMu.RLock()
	docs := g.lotDocs[lotID]
	g.lotDocsMu.RUnlock()

	if len(docs) == 0 {
		return nil, fmt.Errorf("no documents found for lot_id=%s", lotID)
	}

	out := make([]model.DocumentRef, len(docs))
	copy(out, docs)
	return out, nil
}

func (g *GraphQLClient) DownloadDocument(ctx context.Context, doc model.DocumentRef) ([]byte, string, error) {
	if strings.TrimSpace(doc.URL) == "" {
		return nil, "", errorsf("document URL is empty for doc_id=%s", doc.ID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, doc.URL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create doc download request: %w", err)
	}

	res, err := g.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download doc: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return nil, "", fmt.Errorf("download doc status=%d body=%s", res.StatusCode, string(body))
	}

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read downloaded doc: %w", err)
	}

	contentType := strings.TrimSpace(res.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = strings.TrimSpace(doc.MIMEType)
	}
	if contentType == "" {
		contentType = mimeFromFilename(doc.Name)
	}

	return data, contentType, nil
}

func (g *GraphQLClient) queryEnvelope(ctx context.Context, operationName, rootField, query string, variables map[string]any) (gqlEnvelope, error) {
	if strings.TrimSpace(g.url) == "" {
		return gqlEnvelope{}, fmt.Errorf("ows graphql url is empty")
	}

	g.logger.Info("ows graphql request",
		"operation", operationName,
		"root_field", rootField,
		"variables_keys", variableKeys(variables),
	)

	reqBody := map[string]any{
		"query":     query,
		"variables": variables,
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return gqlEnvelope{}, fmt.Errorf("marshal gql request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.url, bytes.NewReader(b))
	if err != nil {
		return gqlEnvelope{}, fmt.Errorf("create gql request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}

	res, err := g.httpClient.Do(req)
	if err != nil {
		return gqlEnvelope{}, fmt.Errorf("execute gql request: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return gqlEnvelope{}, fmt.Errorf("read gql response: %w", err)
	}

	if res.StatusCode >= 300 {
		g.logger.Error("ows graphql non-200", "status", res.StatusCode, "operation", operationName)
		return gqlEnvelope{}, fmt.Errorf("gql status=%d body=%s", res.StatusCode, string(body))
	}

	var envelope gqlEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		g.logger.Error("ows graphql parse error", "operation", operationName, "error", err.Error())
		return gqlEnvelope{}, fmt.Errorf("decode gql response: %w", err)
	}

	if envelope.Errors != nil {
		return gqlEnvelope{}, fmt.Errorf("gql returned errors: %v", envelope.Errors)
	}

	return envelope, nil
}

func parseTrdBuyFromData(data map[string]any) ([]model.Lot, map[string]string, error) {
	rawItems, ok := data["TrdBuy"]
	if !ok {
		return nil, nil, errorsf("response data has no TrdBuy field")
	}

	if rawItems == nil {
		return []model.Lot{}, map[string]string{}, nil
	}

	items, ok := rawItems.([]any)
	if !ok {
		return nil, nil, errorsf("TrdBuy field has unexpected type %T", rawItems)
	}

	out := make([]model.Lot, 0, len(items))
	statusByID := make(map[string]string, len(items))

	for _, item := range items {
		node, ok := item.(map[string]any)
		if !ok {
			continue
		}

		id := strings.TrimSpace(fmt.Sprintf("%v", node["id"]))
		if id == "" || id == "<nil>" {
			continue
		}

		nameRu := getStringAny(node, "nameRu")
		customerNameRu := getStringAny(node, "customerNameRu")
		publishDate := getStringAny(node, "publishDate")
		numberAnno := getStringAny(node, "numberAnno")

		announcement := model.Lot{
			ID:               id,
			Title:            nameRu,
			Customer:         customerNameRu,
			Amount:           0,
			Currency:         "KZT",
			URL:              buildAnnouncementURL(id),
			PublishedAt:      parseTimeAny(publishDate),
			LotNumber:        numberAnno,
			NameRu:           nameRu,
			DescriptionRu:    "",
			CustomerNameRu:   customerNameRu,
			TrdBuyNumberAnno: numberAnno,
			TrdBuyID:         id,
			LastUpdateDate:   publishDate,
		}

		if announcement.Title == "" {
			announcement.Title = numberAnno
		}

		if ref, ok := node["RefBuyStatus"].(map[string]any); ok {
			statusByID[id] = getStringAny(ref, "code")
		}

		out = append(out, announcement)
	}

	return out, statusByID, nil
}

func parsePageInfo(extensions map[string]any) gqlPageInfo {
	page := gqlPageInfo{}
	if len(extensions) == 0 {
		return page
	}

	rawPage, ok := extensions["pageInfo"]
	if !ok {
		return page
	}

	node, ok := rawPage.(map[string]any)
	if !ok {
		return page
	}

	page.LimitPage = int(getFloatAny(node, "limitPage"))
	page.TotalCount = int(getFloatAny(node, "totalCount"))
	page.HasNextPage = getBoolAny(node, "hasNextPage")
	page.LastID = int(getFloatAny(node, "lastId"))
	page.LastIndexDate = getStringAny(node, "lastIndexDate")
	return page
}

func isBlacklistedLot(lot model.Lot) bool {
	text := strings.ToLower(lot.NameRu + " " + lot.DescriptionRu + " " + lot.Title)
	for _, token := range blacklistTokens {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func isRelevantAnnouncement(item model.Lot) bool {
	text := strings.ToLower(strings.TrimSpace(item.NameRu + " " + item.Title))
	for _, token := range relevanceTokens {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func announcementContainsKeyword(item model.Lot, keyword string) bool {
	k := strings.ToLower(strings.TrimSpace(keyword))
	if k == "" {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(item.NameRu + " " + item.Title))
	return strings.Contains(text, k)
}

func normalizeKeywords(keywords []string) []string {
	out := make([]string, 0, len(keywords))
	seen := make(map[string]struct{}, len(keywords))

	for _, keyword := range keywords {
		k := strings.ToLower(strings.TrimSpace(keyword))
		if k == "" {
			continue
		}
		if _, exists := seen[k]; exists {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}

	return out
}

func buildAnnouncementURL(trdBuyID string) string {
	id := strings.TrimSpace(trdBuyID)
	if id == "" {
		return ""
	}
	return "https://goszakup.gov.kz/ru/announce/index/" + id
}

func buildFileURL(filePath string) string {
	p := strings.TrimSpace(filePath)
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return p
	}
	if strings.HasPrefix(p, "/") {
		return "https://ows.goszakup.gov.kz" + p
	}
	return "https://ows.goszakup.gov.kz/" + p
}

func mimeFromFilename(name string) string {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(name)))
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".doc":
		return "application/msword"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func (g *GraphQLClient) cacheLotText(lot model.Lot) {
	text := strings.TrimSpace(strings.Join([]string{
		lot.NameRu,
		lot.DescriptionRu,
		lot.CustomerNameRu,
		lot.TrdBuyNumberAnno,
	}, "\n"))
	if text == "" {
		return
	}
	g.lotTextMu.Lock()
	g.lotText[lot.ID] = text
	g.lotTextMu.Unlock()
}

func (g *GraphQLClient) cacheLotDocuments(lotID string, docs []model.DocumentRef) {
	if strings.TrimSpace(lotID) == "" || len(docs) == 0 {
		return
	}
	cp := make([]model.DocumentRef, len(docs))
	copy(cp, docs)

	g.lotDocsMu.Lock()
	g.lotDocs[lotID] = cp
	g.lotDocsMu.Unlock()
}

func sortedSetKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func variableKeys(vars map[string]any) []string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func getStringAny(m map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch vv := v.(type) {
		case string:
			return strings.TrimSpace(vv)
		case fmt.Stringer:
			return strings.TrimSpace(vv.String())
		default:
			s := strings.TrimSpace(fmt.Sprintf("%v", vv))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func getFloatAny(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch vv := v.(type) {
		case float64:
			return vv
		case float32:
			return float64(vv)
		case int:
			return float64(vv)
		case int64:
			return float64(vv)
		case json.Number:
			f, _ := vv.Float64()
			return f
		case string:
			f, _ := strconv.ParseFloat(strings.ReplaceAll(vv, ",", "."), 64)
			return f
		}
	}
	return 0
}

func getBoolAny(m map[string]any, keys ...string) bool {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch vv := v.(type) {
		case bool:
			return vv
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(vv))
			if err == nil {
				return b
			}
		}
	}
	return false
}

func parseTimeAny(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	layouts := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"}
	for _, l := range layouts {
		if t, err := time.Parse(l, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

func errorsf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}