package api

import (
	"context"
	"strings"
	"time"

	"github.com/hyaeve/manco/internal/cronutil"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/telegram"
)

var _ telegram.App = (*Server)(nil)

// BotSources lists the content sources a bot user can search, in the same order
// the resource extension page shows them.
func (s *Server) BotSources(ctx context.Context) ([]telegram.BotSource, error) {
	items := s.filteredSourceList(s.hiddenSources(ctx), nil)
	result := make([]telegram.BotSource, 0, len(items))
	for _, item := range items {
		if item.Hidden {
			continue
		}
		result = append(result, telegram.BotSource{
			ID:   item.ID,
			Name: item.Name,
			Kind: source.KindOf(item),
		})
	}
	return result, nil
}

// BotSearch runs one content source search and returns its top results.
func (s *Server) BotSearch(ctx context.Context, sourceID, query string, limit int) ([]model.Comic, error) {
	adapter, err := s.registry.Get(sourceID)
	if err != nil {
		return nil, err
	}
	account, err := s.registry.Account(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	result, err := adapter.Search(ctx, account, strings.TrimSpace(query), 1)
	if err != nil {
		return nil, err
	}
	info := adapter.Info()
	kind := source.KindOf(info)
	items := make([]model.Comic, 0, len(result.Items))
	for _, item := range result.Items {
		item = s.annotateDiscover(info.ID, kind, item)
		items = append(items, item)
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return items, nil
}

// BotSubscribe creates a subscription with the same defaults the web UI uses:
// source default download location, simplified conversion, historical chapters
// and weekly automatic updates.
func (s *Server) BotSubscribe(ctx context.Context, sourceID, comicID string) (model.Subscription, error) {
	adapter, err := s.registry.Get(sourceID)
	if err != nil {
		return model.Subscription{}, err
	}
	account, err := s.registry.Account(ctx, sourceID)
	if err != nil {
		return model.Subscription{}, err
	}
	comic, err := s.loadComicDetail(ctx, adapter, account, comicID)
	if err != nil {
		return model.Subscription{}, err
	}
	settings := s.sourceDownloadSettingsFor(ctx, sourceID)
	downloadDir := strings.TrimSpace(settings.DefaultDownloadDir)
	if downloadDir == "" {
		downloadDir = s.cfg.DownloadDir
	}
	title := source.FirstNonEmpty(comic.Comic.Title, comicID)
	baselineID, baselineTitle, baselineOrder := "", "", 0.0
	if len(comic.Chapters) > 0 {
		newest := comic.Chapters[len(comic.Chapters)-1]
		baselineID, baselineTitle, baselineOrder = newest.ID, newest.Title, newest.Order
	}
	subscription := model.Subscription{
		SourceID:            adapter.Info().ID,
		ComicID:             source.FirstNonEmpty(comic.Comic.ID, comicID),
		Title:               title,
		SeriesDir:           subscriptionSeriesDirName("", title, true),
		Cover:               comic.Comic.Cover,
		Author:              comic.Comic.Author,
		DownloadDir:         downloadDir,
		ConvertToSimplified: true,
		AutoDownload:        true,
		CronExpr:            cronutil.Default(time.Now()),
		Enabled:             true,
		LastChapterID:       baselineID,
		LastChapterTitle:    baselineTitle,
		LastChapterOrder:    baselineOrder,
		ComicStatus:         comic.Comic.Status,
	}
	created, err := s.store.UpsertSubscription(ctx, subscription)
	if err != nil {
		return model.Subscription{}, err
	}
	_ = s.configs.PersistSubscriptions(ctx, s.store)
	// Historical chapters download right away; the stored baseline keeps later
	// scheduled checks from re-queueing them.
	_, _, _ = s.scheduler.QueueAll(ctx, created)
	s.engine.Notify()
	return created, nil
}

// sourceDownloadSettingsFor returns the per-source policy merged with defaults.
func (s *Server) sourceDownloadSettingsFor(ctx context.Context, sourceID string) model.SourceDownloadSettings {
	accounts, err := s.store.ListSourceAccounts(ctx)
	if err == nil {
		for _, account := range accounts {
			if account.SourceID == sourceID {
				return s.sourceDownloadSettings(account.Extra)
			}
		}
	}
	return s.sourceDownloadSettings(nil)
}

// BotSubscriptions lists active subscriptions for the bot.
func (s *Server) BotSubscriptions(ctx context.Context) ([]telegram.BotSubscription, error) {
	items, err := s.store.ListSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]telegram.BotSubscription, 0, len(items))
	for _, item := range items {
		name := item.SourceID
		if adapter, err := s.registry.Get(item.SourceID); err == nil {
			name = adapter.Info().Name
		}
		result = append(result, telegram.BotSubscription{
			ID:      item.ID,
			Source:  name,
			Title:   item.Title,
			Enabled: item.Enabled,
		})
	}
	return result, nil
}

// BotCheck runs one update check and also retries previously failed chapters.
func (s *Server) BotCheck(ctx context.Context, subscriptionID int64) (telegram.BotCheckResult, error) {
	subscription, err := s.store.Subscription(ctx, subscriptionID)
	if err != nil {
		return telegram.BotCheckResult{}, err
	}
	before, _ := s.store.CompletedDownloadJobsByComic(ctx, subscription.SourceID, subscription.ComicID)
	if err := s.scheduler.CheckOne(ctx, subscriptionID); err != nil {
		return telegram.BotCheckResult{}, err
	}
	s.retryFailedForSubscription(ctx, subscription)
	updated, _ := s.store.Subscription(ctx, subscriptionID)
	result := telegram.BotCheckResult{Title: subscription.Title}
	if updated.LastCheckedAt != nil {
		result.New = len(before)
	}
	result.Queued = 0
	result.Completed = strings.EqualFold(updated.ComicStatus, "已完结")
	return result, nil
}

// BotArchive archives one subscription.
func (s *Server) BotArchive(ctx context.Context, subscriptionID int64) error {
	if err := s.store.ArchiveSubscription(ctx, subscriptionID, "manual"); err != nil {
		return err
	}
	return nil
}

// BotFill queues missing chapters and reports how many were queued or skipped.
func (s *Server) BotFill(ctx context.Context, subscriptionID int64) (int, int, error) {
	subscription, err := s.store.Subscription(ctx, subscriptionID)
	if err != nil {
		return 0, 0, err
	}
	s.requeueMissingFiles(ctx, subscription)
	return s.scheduler.QueueAll(ctx, subscription)
}

// BotSetSubscriptionEnabled 切换订阅的启用/禁用状态。
func (s *Server) BotSetSubscriptionEnabled(ctx context.Context, subscriptionID int64, enabled bool) error {
	subscription, err := s.store.Subscription(ctx, subscriptionID)
	if err != nil {
		return err
	}
	if err := s.store.UpdateSubscription(ctx, subscriptionID, enabled, subscription.AutoDownload, subscription.CronExpr); err != nil {
		return err
	}
	_ = s.configs.PersistSubscriptions(ctx, s.store)
	return nil
}

// BotComicDetail 拉取作品详情，供 TG 搜索结果里显示作者、简介和最新章节——
// 列表接口通常不带这些字段，必须再查一次详情（和网页端同一条链路）。
func (s *Server) BotComicDetail(ctx context.Context, sourceID, comicID string) (model.Comic, error) {
	item, err := s.registry.Get(sourceID)
	if err != nil {
		return model.Comic{}, err
	}
	account, err := s.registry.Account(ctx, sourceID)
	if err != nil {
		return model.Comic{}, err
	}
	detail, err := s.loadComicDetail(ctx, item, account, comicID)
	if err != nil {
		return model.Comic{}, err
	}
	comic := detail.Comic
	if strings.TrimSpace(comic.LatestChapterTitle) == "" && len(detail.Chapters) > 0 {
		comic.LatestChapterTitle = detail.Chapters[len(detail.Chapters)-1].Title
	}
	return comic, nil
}

// BotScrape re-fetches the source metadata and overwrites the local
// ComicInfo.xml / series.json / cover files of an already downloaded work.
func (s *Server) BotScrape(ctx context.Context, subscriptionID int64) (bool, error) {
	subscription, err := s.store.Subscription(ctx, subscriptionID)
	if err != nil {
		return false, err
	}
	return s.engine.ScrapeSeriesMetadata(ctx, subscription)
}

// BotFullDownload performs the same full refresh as the web UI: clear records
// and files, then re-queue every chapter.
func (s *Server) BotFullDownload(ctx context.Context, subscriptionID int64) (int, error) {
	subscription, err := s.store.Subscription(ctx, subscriptionID)
	if err != nil {
		return 0, err
	}
	queued, _, _, err := s.fullRefreshSubscription(ctx, subscription)
	return queued, err
}

// BotDelete deletes a subscription.
func (s *Server) BotDelete(ctx context.Context, subscriptionID int64) error {
	if err := s.store.DeleteSubscription(ctx, subscriptionID); err != nil {
		return err
	}
	_ = s.configs.PersistSubscriptions(ctx, s.store)
	return nil
}
