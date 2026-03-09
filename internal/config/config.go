package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	OWSToken            string
	OWSGraphQLURL       string
	GoszakupMode        string
	OWSQueryMode        string
	OpenAIAPIKey        string
	OpenAIModel         string
	TelegramBotToken    string
	TelegramChatID      string
	SearchKeywords      []string
	Port                string
	StateBackend        string
	SQLitePath          string
	SearchWindowMinutes int
	MaxLotsPerRun       int
	HTTPTimeoutSeconds  int
	OpenAIMaxTextChars  int
	TelegramEnabled     bool
	DryRun              bool
}

func Load() (Config, error) {
	cfg := Config{
		OWSToken:            strings.TrimSpace(os.Getenv("OWS_TOKEN")),
		OWSGraphQLURL:       strings.TrimSpace(os.Getenv("OWS_GRAPHQL_URL")),
		GoszakupMode:        strings.ToLower(getOrDefault("GOSZAKUP_MODE", "real")),
		OWSQueryMode:        strings.ToLower(getOrDefault("OWS_QUERY_MODE", "normal")),
		OpenAIAPIKey:        strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		OpenAIModel:         getOrDefault("OPENAI_MODEL", "gpt-4o-mini"),
		TelegramBotToken:    strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		TelegramChatID:      strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")),
		SearchKeywords:      ParseKeywords(os.Getenv("SEARCH_KEYWORDS")),
		Port:                getOrDefault("PORT", "8080"),
		StateBackend:        strings.ToLower(getOrDefault("STATE_BACKEND", "sqlite")),
		SQLitePath:          getOrDefault("SQLITE_PATH", "./data/rz_gz.db"),
		SearchWindowMinutes: parseIntWithDefault("SEARCH_WINDOW_MINUTES", 60),
		MaxLotsPerRun:       parseIntWithDefault("MAX_LOTS_PER_RUN", 50),
		HTTPTimeoutSeconds:  parseIntWithDefault("HTTP_TIMEOUT_SECONDS", 20),
		OpenAIMaxTextChars:  parseIntWithDefault("OPENAI_MAX_TEXT_CHARS", 20000),
		TelegramEnabled:     parseBoolWithDefault("TELEGRAM_ENABLED", true),
		DryRun:              parseBoolWithDefault("DRY_RUN", false),
	}

	if len(cfg.SearchKeywords) == 0 {
		return Config{}, errors.New("SEARCH_KEYWORDS is required")
	}
	if cfg.Port == "" {
		return Config{}, errors.New("PORT cannot be empty")
	}
	if cfg.StateBackend != "sqlite" && cfg.StateBackend != "memory" {
		return Config{}, errors.New("STATE_BACKEND must be sqlite or memory")
	}
	if cfg.GoszakupMode != "fake" && cfg.GoszakupMode != "real" {
		return Config{}, errors.New("GOSZAKUP_MODE must be fake or real")
	}
	if cfg.OWSQueryMode != "minimal" && cfg.OWSQueryMode != "normal" {
		return Config{}, errors.New("OWS_QUERY_MODE must be minimal or normal")
	}

	return cfg, nil
}

func ParseKeywords(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		kw := strings.ToLower(strings.TrimSpace(p))
		if kw == "" {
			continue
		}
		if _, ok := seen[kw]; ok {
			continue
		}
		seen[kw] = struct{}{}
		out = append(out, kw)
	}
	return out
}

func getOrDefault(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func parseIntWithDefault(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func parseBoolWithDefault(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
