// Command manco runs the Manco comic downloader server.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hyaeve/manco/internal/api"
	"github.com/hyaeve/manco/internal/config"
	"github.com/hyaeve/manco/internal/downloader"
	"github.com/hyaeve/manco/internal/logbuf"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/scheduler"
	"github.com/hyaeve/manco/internal/secret"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
	"github.com/hyaeve/manco/internal/store"
)

//go:embed all:web/dist
var embedded embed.FS

func main() {
	logs := logbuf.New(500, os.Stdout)
	logger := log.New(logs, "", log.LstdFlags|log.Lmsgprefix)
	if err := run(logger, logs); err != nil {
		logger.Fatalf("manco: %v", err)
	}
}

// downloadQueue lets the scheduler enqueue chapters through the store while
// waking the download engine immediately.
type downloadQueue struct {
	store  *store.Store
	engine *downloader.Engine
}

func (q downloadQueue) CreateDownloadJob(ctx context.Context, job model.DownloadJob) (model.DownloadJob, error) {
	return q.store.CreateDownloadJob(ctx, job)
}

func (q downloadQueue) Notify() { q.engine.Notify() }

func run(logger *log.Logger, logs *logbuf.Buffer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	repository, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer repository.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	box := secret.New(cfg.Secret)
	client := source.NewHTTPClient()
	if rawProxy, err := repository.Setting(ctx, "proxy"); err == nil {
		proxyUsername, _ := repository.Setting(ctx, "proxy_username")
		proxyPassword, _ := repository.Setting(ctx, "proxy_password")
		if err := source.SetHTTPClientProxyCredentials(client, rawProxy, proxyUsername, proxyPassword); err != nil {
			logger.Printf("manco: apply proxy: %v", err)
		}
	}
	registry := sources.NewRegistry(client, box, repository)
	engine := downloader.NewEngine(registry, repository, cfg.DownloadDir, cfg.MaxChapterConcurrency, cfg.MaxPageConcurrency, logger)
	scanner := scheduler.New(registry, repository, downloadQueue{store: repository, engine: engine}, cfg.ScanInterval, logger)

	assets, err := fs.Sub(embedded, "web/dist")
	if err != nil {
		return err
	}
	server := api.New(api.Options{
		Config:    cfg,
		Store:     repository,
		Box:       box,
		Registry:  registry,
		Engine:    engine,
		Scheduler: scanner,
		Logger:    logger,
		Logs:      logs,
		Assets:    assets,
	})
	settings, err := server.Settings(ctx)
	if err != nil {
		logger.Printf("manco: load settings: %v", err)
	} else {
		engine.SetSourceConcurrency(settings.SourceConcurrency)
		engine.SetDownloadPolicy(settings.BatchSize, settings.BatchIntervalMinutes, settings.ConvertToSimplified)
	}
	_ = repository.CleanupSessions(ctx, time.Now())
	// A process restart invalidates all browser sessions, so every client has
	// to authenticate again after the service comes back up.
	if err := repository.DeleteAllSessions(ctx); err != nil {
		return err
	}
	if err := repository.RequeueRunningJobs(ctx); err != nil {
		return err
	}
	engine.Start(ctx)
	scanner.Start(ctx)
	go server.RunSubscriptionMaintenance(ctx, 30*time.Minute)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       120 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}
	shutdownDone := make(chan struct{})
	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-signals:
			cancel()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer shutdownCancel()
			_ = httpServer.Shutdown(shutdownCtx)
		case <-shutdownDone:
		}
	}()

	logger.Printf("manco listening on %s (downloads: %s)", cfg.Addr, cfg.DownloadDir)
	err = httpServer.ListenAndServe()
	close(shutdownDone)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
