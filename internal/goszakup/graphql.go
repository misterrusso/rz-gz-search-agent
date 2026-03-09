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
	"time"

	"rz_gz_search_agent/internal/model"
)

const (
	searchLotsQuery = `
query SearchLots($filter: TrdBuyFiltersInput, $limit: Int, $after: Int) {
  TrdBuy(filter: $filter, limit: $limit, after: $after) {
    id
    name
    customerName
    sumTruNoNds
    currencyCode
    publishDate
  }
}`
	searchLotsMinimalQuery = `
query SearchLotsMinimal($filter: TrdBuyFiltersInput, $limit: Int, $after: Int) {
  TrdBuy(filter: $filter, limit: $limit, after: $after) {
    id
  }
}`
	getLotDocumentsQuery = `
query GetLotDocuments($filter: LotsFiltersInput, $limit: Int, $after: Int) {
  Lots(filter: $filter, limit: $limit, after: $after) {
    id
    files {
      id
      name
      url
      mime
    }
  }
}`
	getLotDocumentsAltQuery = `
query GetLotDocumentsAlt($filter: LotsFiltersInput, $limit: Int, $after: Int) {
  Lots(filter: $filter, limit: $limit, after: $after) {
    id
    documents {
      id
      name
      url
      mimeType
    }
  }
}`
)

type GraphQLClient struct {
	url        string
	token      string
	logger     *slog.Logger
	httpClient *http.Client
}

func NewGraphQLClient(url, token string, timeout time.Duration, logger *slog.Logger) *GraphQLClient {
	if logger == nil {
		logger = slog.Default()
	}
	return &GraphQLClient{
		url:   url,
		token: token,
		logger: logger,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (g *GraphQLClient) SearchLots(ctx context.Context, keywords []string, from time.Time, to time.Time, limit int) ([]model.Lot, error) {
	_ = keywords
	_ = from
	_ = to
	var resp map[string]any
	err := g.query(ctx, "SearchLots", "TrdBuy", searchLotsQuery, map[string]any{
		"filter": nil,
		"limit":  limit,
		"after":  0,
	}, &resp)
	if err != nil {
		var fallback map[string]any
		fallbackErr := g.query(ctx, "SearchLotsMinimal", "TrdBuy", searchLotsMinimalQuery, map[string]any{
			"filter": nil,
			"limit":  limit,
			"after":  0,
		}, &fallback)
		if fallbackErr != nil {
			return nil, err
		}
		resp = fallback
	}
	return parseLotsFlexible(resp), nil
}

func (g *GraphQLClient) GetLotDocuments(ctx context.Context, lotID string) ([]model.DocumentRef, error) {
	vars := map[string]any{
		"filter": map[string]any{"id": lotID},
		"limit":  1,
		"after":  0,
	}
	var resp map[string]any
	err := g.query(ctx, "GetLotDocuments", "Lots", getLotDocumentsQuery, vars, &resp)
	if err != nil {
		var alt map[string]any
		altErr := g.query(ctx, "GetLotDocumentsAlt", "Lots", getLotDocumentsAltQuery, vars, &alt)
		if altErr != nil {
			return nil, err
		}
		resp = alt
	}
	return parseDocumentsFlexible(resp), nil
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
	return data, res.Header.Get("Content-Type"), nil
}

func (g *GraphQLClient) query(ctx context.Context, operationName, rootField, query string, variables map[string]any, out *map[string]any) error {
	if strings.TrimSpace(g.url) == "" {
		return fmt.Errorf("ows graphql url is empty")
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
		return fmt.Errorf("marshal gql request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("create gql request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}

	res, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute gql request: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("read gql response: %w", err)
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("gql status=%d body=%s", res.StatusCode, string(body))
	}

	var envelope struct {
		Data   map[string]any `json:"data"`
		Errors any            `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode gql response: %w", err)
	}
	if envelope.Errors != nil {
		return fmt.Errorf("gql returned errors: %v", envelope.Errors)
	}
	*out = envelope.Data
	return nil
}

func variableKeys(vars map[string]any) []string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func parseLotsFlexible(data map[string]any) []model.Lot {
	nodes := collectObjectArrayNodes(data)
	out := make([]model.Lot, 0)
	for _, node := range nodes {
		id := getStringAny(node, "id", "lotId")
		if id == "" {
			continue
		}
		published := parseTimeAny(getStringAny(node, "publishedAt", "publishDate", "createdAt"))
		out = append(out, model.Lot{
			ID:          id,
			Title:       getStringAny(node, "title", "name", "buyName", "numberAnno"),
			Customer:    getStringAny(node, "customer", "customerName"),
			Amount:      getFloatAny(node, "amount", "price", "sum", "sumTruNoNds"),
			Currency:    getStringAny(node, "currency", "currencyCode"),
			URL:         getStringAny(node, "url", "link"),
			PublishedAt: published,
		})
	}
	return dedupLots(out)
}

func parseDocumentsFlexible(data map[string]any) []model.DocumentRef {
	nodes := collectObjectArrayNodes(data)
	out := make([]model.DocumentRef, 0)
	for _, node := range nodes {
		id := getStringAny(node, "id", "docId")
		url := getStringAny(node, "url", "downloadUrl", "href")
		name := getStringAny(node, "name", "title", "fileName")
		if url == "" {
			continue
		}
		out = append(out, model.DocumentRef{
			ID:       id,
			Name:     name,
			URL:      url,
			MIMEType: getStringAny(node, "mimeType", "mime", "contentType"),
		})
	}
	return dedupDocs(out)
}

func collectObjectArrayNodes(input any) []map[string]any {
	out := make([]map[string]any, 0)
	var walk func(v any)
	walk = func(v any) {
		switch vv := v.(type) {
		case map[string]any:
			for _, child := range vv {
				walk(child)
			}
		case []any:
			for _, item := range vv {
				if m, ok := item.(map[string]any); ok {
					out = append(out, m)
				}
				walk(item)
			}
		}
	}
	walk(input)
	return out
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

func dedupLots(items []model.Lot) []model.Lot {
	seen := make(map[string]struct{}, len(items))
	out := make([]model.Lot, 0, len(items))
	for _, l := range items {
		if l.ID == "" {
			continue
		}
		if _, ok := seen[l.ID]; ok {
			continue
		}
		seen[l.ID] = struct{}{}
		out = append(out, l)
	}
	return out
}

func dedupDocs(items []model.DocumentRef) []model.DocumentRef {
	seen := make(map[string]struct{}, len(items))
	out := make([]model.DocumentRef, 0, len(items))
	for _, d := range items {
		key := d.ID + "|" + d.URL + "|" + d.Name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, d)
	}
	return out
}

func errorsf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
