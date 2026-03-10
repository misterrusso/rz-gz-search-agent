package goszakup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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

	graphqlSearchLotsQuery = `
query SearchLots($limit: Int, $after: Int, $filter: LotsFiltersInput) {
  Lots(filter: $filter, limit: $limit, after: $after) {
    id
    lotNumber
    nameRu
    descriptionRu
    customerNameRu
    trdBuyNumberAnno
    trdBuyId
    amount
    lastUpdateDate
  }
}`

	graphqlSearchLotsMinimalQuery = `
query SearchLotsMinimal($limit: Int) {
  Lots(limit: $limit) {
    id
    nameRu
    descriptionRu
    customerNameRu
    amount
    lastUpdateDate
  }
}`
)

var (
	defaultSearchKeywords = []string{
		"услуги перевода",
		"письменный перевод",
		"устный перевод",
		"синхронный перевод",
		"последовательный перевод",
		"нотариальный перевод",
		"перевод документов",
		"перевод текстов",
		"localization",
		"translation",
		"interpretation",
		"notarized translation",
	}
	blacklistTokens = []string{
		"стрелоч",
		"лесного фонда",
		"земли других категорий",
		"землеустро",
		"земельн",
	}
	relevanceTokens = []string{
		"услуги по письменному переводу",
		"письменный перевод",
		"перевод документов",
		"синхронный перевод",
		"последовательный перевод",
		"устный перевод",
		"нотариальный перевод",
		"локализац",
		"translation",
		"interpretation",
		"localization",
		"notarized translation",
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
	}
}

func (g *GraphQLClient) SearchLots(ctx context.Context, keywords []string, from time.Time, to time.Time, limit int) ([]model.Lot, error) {
	_ = from
	_ = to

	maxResults := limit
	if maxResults <= 0 {
		maxResults = 50
	}
	queryKeywords := normalizeKeywords(keywords)
	if len(queryKeywords) == 0 {
		queryKeywords = append([]string{}, defaultSearchKeywords...)
	}

	if g.queryMode == queryModeMinimal {
		return g.searchLotsMinimal(ctx, maxResults)
	}
	return g.searchLotsByKeywords(ctx, queryKeywords, maxResults)
}

func (g *GraphQLClient) searchLotsMinimal(ctx context.Context, maxResults int) ([]model.Lot, error) {
	const pageLimit = 20
	vars := map[string]any{"limit": pageLimit}
	page, pageInfo, err := g.queryLotsPage(ctx, "SearchLotsMinimal", "Lots", graphqlSearchLotsMinimalQuery, vars)
	if err != nil {
		return nil, err
	}
	g.logger.Info("ows lots page", "mode", queryModeMinimal, "results", len(page), "has_next_page", pageInfo.HasNextPage, "last_id", pageInfo.LastID)

	filtered := make([]model.Lot, 0, len(page))
	for _, lot := range page {
		if isBlacklistedLot(lot) || !isRelevantTranslationLot(lot) {
			continue
		}
		filtered = append(filtered, lot)
		g.cacheLotText(lot)
		if len(filtered) >= maxResults {
			break
		}
	}
	return filtered, nil
}

func (g *GraphQLClient) searchLotsByKeywords(ctx context.Context, keywords []string, maxResults int) ([]model.Lot, error) {
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
				"filter": map[string]any{
					"nameDescriptionRu": keyword,
				},
			}
			pageLots, pageInfo, err := g.queryLotsPage(ctx, "SearchLots", "Lots", graphqlSearchLotsQuery, vars)
			if err != nil {
				return nil, err
			}
			g.logger.Info("ows keyword page",
				"keyword", keyword,
				"page", pageNum,
				"results", len(pageLots),
				"last_id", pageInfo.LastID,
				"has_next_page", pageInfo.HasNextPage,
			)

			filteredCount := 0
			for _, lot := range pageLots {
				if isBlacklistedLot(lot) || !isRelevantTranslationLot(lot) {
					continue
				}
				filteredCount++

				entry, exists := collected[lot.ID]
				if !exists {
					copyLot := lot
					entry = &matchedLot{lot: copyLot, matched: map[string]struct{}{}}
					collected[lot.ID] = entry
					g.cacheLotText(lot)
				}
				entry.matched[keyword] = struct{}{}
			}

			g.logger.Info("ows keyword page filtered",
				"keyword", keyword,
				"filtered", filteredCount,
				"dedup_total", len(collected),
			)

			if len(collected) >= maxResults {
				return finalizeMatchedLots(collected, maxResults), nil
			}
			if !pageInfo.HasNextPage {
				break
			}
			if pageInfo.LastID <= 0 {
				g.logger.Warn("stopping pagination due to invalid last_id", "keyword", keyword, "last_id", pageInfo.LastID)
				break
			}
			if _, seen := seenAfter[pageInfo.LastID]; seen {
				g.logger.Warn("stopping pagination due to repeated last_id", "keyword", keyword, "last_id", pageInfo.LastID)
				break
			}
			seenAfter[pageInfo.LastID] = struct{}{}
			after = pageInfo.LastID
		}
	}

	return finalizeMatchedLots(collected, maxResults), nil
}

func finalizeMatchedLots(collected map[string]*matchedLot, maxResults int) []model.Lot {
	out := make([]model.Lot, 0, len(collected))
	for _, item := range collected {
		lot := item.lot
		lot.MatchedKeywords = sortedSetKeys(item.matched)
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

func (g *GraphQLClient) queryLotsPage(ctx context.Context, operationName, rootField, query string, variables map[string]any) ([]model.Lot, gqlPageInfo, error) {
	envelope, err := g.queryEnvelope(ctx, operationName, rootField, query, variables)
	if err != nil {
		return nil, gqlPageInfo{}, err
	}
	lots, err := parseLotsFromData(envelope.Data)
	if err != nil {
		return nil, gqlPageInfo{}, fmt.Errorf("parse lots: %w", err)
	}
	pageInfo := parsePageInfo(envelope.Extensions)
	return lots, pageInfo, nil
}

func (g *GraphQLClient) GetLotDocuments(_ context.Context, lotID string) ([]model.DocumentRef, error) {
	g.lotTextMu.RLock()
	text, ok := g.lotText[lotID]
	g.lotTextMu.RUnlock()
	if !ok || strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("no cached lot text for lot_id=%s", lotID)
	}
	return []model.DocumentRef{
		{
			ID:       "inline-" + lotID,
			Name:     "lot_" + lotID + "_summary.txt",
			URL:      "inline://lot/" + lotID,
			MIMEType: "text/plain",
		},
	}, nil
}

func (g *GraphQLClient) DownloadDocument(ctx context.Context, doc model.DocumentRef) ([]byte, string, error) {
	if strings.HasPrefix(doc.URL, "inline://lot/") {
		lotID := strings.TrimPrefix(doc.URL, "inline://lot/")
		g.lotTextMu.RLock()
		text := g.lotText[lotID]
		g.lotTextMu.RUnlock()
		if strings.TrimSpace(text) == "" {
			return nil, "", fmt.Errorf("inline lot text not found for lot_id=%s", lotID)
		}
		return []byte(text), "text/plain; charset=utf-8", nil
	}

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
	return data, res.Header.Get("Content-Type"), nil
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

func parseLotsFromData(data map[string]any) ([]model.Lot, error) {
	rawLots, ok := data["Lots"]
	if !ok {
		return nil, errorsf("response data has no Lots field")
	}
	items, ok := rawLots.([]any)
	if !ok {
		return nil, errorsf("Lots field has unexpected type %T", rawLots)
	}

	out := make([]model.Lot, 0, len(items))
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
		descriptionRu := getStringAny(node, "descriptionRu")
		customerNameRu := getStringAny(node, "customerNameRu")
		amount := getFloatAny(node, "amount")
		lastUpdate := getStringAny(node, "lastUpdateDate")
		lot := model.Lot{
			ID:               id,
			Title:            firstNonEmpty(nameRu, getStringAny(node, "name")),
			Customer:         firstNonEmpty(customerNameRu, getStringAny(node, "customerName")),
			Amount:           amount,
			Currency:         "KZT",
			URL:              buildLotURL(getStringAny(node, "trdBuyId"), id),
			PublishedAt:      parseTimeAny(lastUpdate),
			LotNumber:        getStringAny(node, "lotNumber"),
			NameRu:           nameRu,
			DescriptionRu:    descriptionRu,
			CustomerNameRu:   customerNameRu,
			TrdBuyNumberAnno: getStringAny(node, "trdBuyNumberAnno"),
			TrdBuyID:         getStringAny(node, "trdBuyId"),
			LastUpdateDate:   lastUpdate,
		}
		if lot.Title == "" {
			lot.Title = descriptionRu
		}
		out = append(out, lot)
	}
	return out, nil
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

func isRelevantTranslationLot(lot model.Lot) bool {
	text := strings.ToLower(lot.NameRu + " " + lot.DescriptionRu + " " + lot.Title)
	score := 0
	for _, token := range relevanceTokens {
		if strings.Contains(text, token) {
			score++
		}
	}
	return score > 0
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

func buildLotURL(trdBuyID, lotID string) string {
	id := strings.TrimSpace(trdBuyID)
	if id != "" {
		return "https://goszakup.gov.kz/ru/announce/index/" + id
	}
	if strings.TrimSpace(lotID) == "" {
		return ""
	}
	return "https://ows.goszakup.gov.kz/ru/lots/" + lotID
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

type gqlEnvelope struct {
	Data       map[string]any `json:"data"`
	Errors     any            `json:"errors"`
	Extensions map[string]any `json:"extensions"`
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
