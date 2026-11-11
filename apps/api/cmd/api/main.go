package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/noeulnight/memehub/apps/api/internal/analysis"
	"github.com/noeulnight/memehub/apps/api/internal/cleanup"
	"github.com/noeulnight/memehub/apps/api/internal/config"
	"github.com/noeulnight/memehub/apps/api/internal/embedding"
	"github.com/noeulnight/memehub/apps/api/internal/server"
	"github.com/noeulnight/memehub/apps/api/internal/storage"
)

func main() {
	if err := run(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func run() error {
	envFile := flag.String("env-file", ".env", "Environment file path")
	flag.Parse()
	cfg, err := config.Load(*envFile)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	startup, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	pool, err := pgxpool.New(startup, cfg.DatabaseURL)
	if err != nil {
		return errors.New("database configuration failed")
	}
	defer pool.Close()
	if err = pool.Ping(startup); err != nil {
		return errors.New("database connection failed")
	}
	app, err := server.New(startup, cfg, pool)
	if err != nil {
		return err
	}
	if err := app.SeedDemo(startup); err != nil {
		return errors.New("cannot initialize demo samples")
	}
	store := storage.New(cfg)
	if cfg.DemoMode {
		store, err = storage.NewLocal(cfg)
		if err != nil {
			return errors.New("cannot initialize demo data directory")
		}
	}
	if store != nil {
		cleaner := &cleanup.Worker{DB: pool, Store: store, DeletedRetention: cfg.DeletedImageRetention, UploadRetention: cfg.IncompleteUploadRetention}
		cleanupDone := make(chan struct{})
		go func() { defer close(cleanupDone); cleaner.Run(ctx) }()
		defer func() { cancel(); <-cleanupDone }()
	}
	if cfg.OpenAIAPIKey != "" {
		worker := &analysis.Worker{DB: pool, Analyzer: analysis.NewPipeline(cfg.OpenAIAPIKey, cfg.OpenAITimeout), Timeout: cfg.OpenAITimeout, MaxImageBytes: cfg.UploadMaxBytes}
		if store != nil {
			worker.Store = store
		}
		workerDone := make(chan struct{})
		go func() {
			defer close(workerDone)
			slog.Info("in-process analysis worker started", "concurrency", cfg.WorkerConcurrency)
			worker.Run(ctx, cfg.WorkerConcurrency)
		}()
		defer func() {
			cancel()
			<-workerDone
		}()
	} else {
		slog.Warn("automatic analysis disabled: OPENAI_API_KEY is not configured")
	}
	if cfg.VectorSearchEnabled {
		worker := &embedding.Worker{DB: pool, Embedder: embedding.NewClient(cfg.OpenAIAPIKey)}
		done := make(chan struct{})
		go func() { defer close(done); worker.Run(ctx) }()
		defer func() { cancel(); <-done }()
	}
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 25 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
	done := make(chan error, 1)
	go func() { slog.Info("API listening", "address", cfg.HTTPAddr); done <- srv.ListenAndServe() }()
	select {
	case err = <-done:
		if err != nil && err != http.ErrServerClosed {
			return errors.New("HTTP server failed")
		}
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err = srv.Shutdown(shutdown); err != nil {
			_ = srv.Close()
			slog.Error("HTTP shutdown failed")
		}
	}
	return nil
}
