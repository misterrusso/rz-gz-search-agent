package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rz_gz_search_agent/internal/config"
	"rz_gz_search_agent/internal/goszakup"
	httpapi "rz_gz_search_agent/internal/http"
	"rz_gz_search_agent/internal/logging"
	"rz_gz_search_agent/internal/openai"
	"rz_gz_search_agent/internal/service"
	"rz_gz_search_agent/internal/state"
	"rz_gz_search_agent/internal/telegram"
)

func main() {
	logger := logging.NewLogger()
	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err.Error())
		os.Exit(1)
	}

	store, err := buildStore(cfg)
	if err != nil {
		logger.Error("failed to initialize state store", "error", err.Error())
		os.Exit(1)
	}
	defer func() {
		if cerr := store.Close(); cerr != nil {
			logger.Error("failed to close state store", "error", cerr.Error())
		}
	}()

	owsClient := buildOWSClient(cfg, logger)
	classifier := buildClassifier(cfg, logger)
	tgClient := telegram.NewBotClient(
		cfg.TelegramBotToken,
		cfg.TelegramChatID,
		cfg.TelegramEnabled,
		time.Duration(cfg.HTTPTimeoutSeconds)*time.Second,
		logger,
	)

	checker := service.NewCheckerService(cfg, logger, owsClient, store, classifier, tgClient)
	server := httpapi.NewServer(cfg.Port, logger, checker)
	go func() {
		logger.Info("server started", "port", cfg.Port)
		if err := server.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err.Error())
			os.Exit(1)
		}
	}()

	waitShutdown(logger, server)
}

func buildStore(cfg config.Config) (state.Store, error) {
	if cfg.StateBackend == "memory" {
		return state.NewMemoryStore(), nil
	}
	return state.NewSQLiteStore(cfg.SQLitePath)
}

func buildOWSClient(cfg config.Config, logger *slog.Logger) goszakup.Client {
	if cfg.DryRun || cfg.OWSGraphQLURL == "" {
		logger.Info("using fake OWS provider", "dry_run", cfg.DryRun, "ows_graphql_url_empty", cfg.OWSGraphQLURL == "")
		return goszakup.NewFakeClient()
	}
	return goszakup.NewGraphQLClient(cfg.OWSGraphQLURL, cfg.OWSToken, time.Duration(cfg.HTTPTimeoutSeconds)*time.Second)
}

func buildClassifier(cfg config.Config, logger *slog.Logger) service.Classifier {
	if cfg.DryRun || cfg.OpenAIAPIKey == "" {
		logger.Info("using fake OpenAI classifier", "dry_run", cfg.DryRun, "openai_api_key_empty", cfg.OpenAIAPIKey == "")
		return openai.NewFakeClassifier()
	}
	return openai.NewClient(cfg.OpenAIAPIKey, cfg.OpenAIModel, time.Duration(cfg.HTTPTimeoutSeconds)*time.Second, logger)
}

func waitShutdown(logger *slog.Logger, srv *httpapi.Server) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logger.Info("shutdown signal received")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("server shutdown failed", "error", err.Error())
	}
}

