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
	"path/filepath"
	"syscall"
	"time"

	"github.com/hyaeve/manco/internal/api"
	"github.com/hyaeve/manco/internal/config"
	"github.com/hyaeve/manco/internal/configstore"
	"github.com/hyaeve/manco/internal/discovercache"
	"github.com/hyaeve/manco/internal/diskcache"
	"github.com/hyaeve/manco/internal/downloader"
	"github.com/hyaeve/manco/internal/iconcache"
	"github.com/hyaeve/manco/internal/logbuf"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/pluginruntime"
	"github.com/hyaeve/manco/internal/scheduler"
	"github.com/hyaeve/manco/internal/secret"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
	"github.com/hyaeve/manco/internal/store"
	"github.com/hyaeve/manco/internal/telegram"
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

func (q downloadQueue) EnsureSeriesMetadata(ctx context.Context, subscription model.Subscription) (bool, error) {
	return q.engine.EnsureSeriesMetadata(ctx, subscription)
}

func (q downloadQueue) RetryFailedByComic(ctx context.Context, sourceID, comicID string) (int, error) {
	return q.engine.RetryFailedByComic(ctx, sourceID, comicID)
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

	configFiles, err := configstore.New(cfg.ConfigDir)
	if err != nil {
		return err
	}
	if err := configFiles.Restore(context.Background(), repository); err != nil {
		return err
	}
	if err := configFiles.PersistAll(context.Background(), repository); err != nil {
		return err
	}
	discoverCache, err := diskcache.Open(filepath.Join(cfg.DataDir, "response-cache"), 30*time.Minute)
	if err != nil {
		return err
	}
	discoverItems, err := discovercache.Open(filepath.Join(cfg.DataDir, "discover"), 30*24*time.Hour)
	if err != nil {
		return err
	}

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
	pluginManager, err := pluginruntime.New(pluginruntime.Options{
		ConfigDir: cfg.ConfigDir,
		DataDir:   cfg.DataDir,
		Client:    client,
		Logger:    logger,
	})
	if err != nil {
		logger.Printf("manco: initialize Kototoro runtime: %v", err)
	} else {
		defer pluginManager.Close()
		rawProxy, _ := repository.Setting(ctx, "proxy")
		proxyUsername, _ := repository.Setting(ctx, "proxy_username")
		proxyPassword, _ := repository.Setting(ctx, "proxy_password")
		if err := pluginManager.ConfigureProxy(rawProxy, proxyUsername, proxyPassword); err != nil {
			logger.Printf("manco: configure Kototoro proxy: %v", err)
		}
	}
	icons, err := iconcache.New(filepath.Join(cfg.DataDir, "icon"), client)
	if err != nil {
		return err
	}
	registry := sources.NewRegistry(client, box, repository, pluginManager)
	engine := downloader.NewEngine(registry, repository, cfg.DownloadDir, cfg.MaxChapterConcurrency, cfg.MaxPageConcurrency, logger)
	engine.SetCacheDir(filepath.Join(cfg.DataDir, "download-cache"))
	scanner := scheduler.New(registry, repository, downloadQueue{store: repository, engine: engine}, cfg.ScanInterval, logger)
	bot := telegram.New(logger)
	// TG 复用应用统一创建的 HTTP 客户端，因此直接跟随系统设置里的代理。
	bot.SetHTTPClient(client)

	assets, err := fs.Sub(embedded, "web/dist")
	if err != nil {
		return err
	}
	server := api.New(api.Options{
		Config:    cfg,
		Store:     repository,
		Configs:   configFiles,
		Cache:     discoverCache,
		Discover:  discoverItems,
		Box:       box,
		Registry:  registry,
		Runtime:   pluginManager,
		Engine:    engine,
		Icons:     icons,
		Scheduler: scanner,
		Logger:    logger,
		Logs:      logs,
		Assets:    assets,
		Telegram:  bot,
	})
	bot.SetApp(server)
	// 旧的「打开添加内容源就把所有仓库枚举一遍」会把几百个扩展留在运行时里，
	// 每次重启 sidecar 都要全部加载，整站都会跟着变慢；启动时先清一次。
	if removed, err := server.PruneUnusedPlugins(ctx); err != nil {
		logger.Printf("manco: prune unused plugins: %v", err)
	} else if removed > 0 {
		logger.Printf("manco: pruned %d unused extension(s) on startup", removed)
	}
	scanner.SetNotifier(bot)
	settings, err := server.Settings(ctx)
	if err != nil {
		logger.Printf("manco: load settings: %v", err)
	} else {
		engine.SetSourceConcurrency(settings.SourceConcurrency)
		engine.SetSourceSettings(server.SourceSettings(ctx))
		engine.SetDownloadPolicy(settings.BatchSize, settings.BatchIntervalMinutes, settings.ConvertToSimplified)
		bot.Configure(telegram.Config{
			Enabled: settings.TelegramEnabled,
			Token:   settings.TelegramToken,
			ChatIDs: settings.TelegramChatIDs,
		})
	}
	bot.Start(ctx)
	defer bot.Stop()
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
	// 登录保活：按各内容源设置的周期重新登录，避免 Token 过期后无法浏览或下载。
	go func() {
		for _, message := range registry.KeepAliveLogins(ctx) {
			logger.Printf("manco: %s", message)
		}
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			for _, message := range registry.KeepAliveLogins(ctx) {
				logger.Printf("manco: %s", message)
			}
		}
	}()
	go server.RunSubscriptionMaintenance(ctx, 30*time.Minute)
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := discoverItems.Prune(now); err != nil {
					logger.Printf("manco: prune discover cache: %v", err)
				}
			}
		}
	}()

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
