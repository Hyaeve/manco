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
	settings := s.allSourceDownloadSettings(ctx)
	result := make([]telegram.BotSource, 0, len(items))
	for _, item := range items {
		if item.Hidden {
			continue
		}
		result = append(result, telegram.BotSource{
			ID:        item.ID,
			Name:      item.Name,
			Kind:      source.KindOf(item),
			CoverBlur: settings[item.ID].CoverBlur,
		})
	}
	return result, nil
}

func (s *Server) BotRememberGlobalSearch(ctx context.Context, query string) error {
	_, err := s.recordGlobalSearch(ctx, query)
	return err
}

// NotifyDownloadRetry groups retry metadata into one Telegram notice per work.
// The downloader invokes this after automatic, scheduled, or manual retries are
// actually re-queued.
func (s *Server) NotifyDownloadRetry(ctx context.Context, jobs []model.DownloadJob) {
	if s.telegram == nil || len(jobs) == 0 {
		return
	}
	first := jobs[0]
	sourceName := first.SourceID
	if adapter, err := s.registry.Get(first.SourceID); err == nil {
		sourceName = adapter.Info().Name
	}
	notice := telegram.DownloadRetryNotice{
		Title:    first.ComicTitle,
		Source:   sourceName,
		Author:   first.ComicAuthor,
		CoverURL: first.ComicCover,
	}
	if subscription, err := s.store.SubscriptionByComic(ctx, first.SourceID, first.ComicID); err == nil {
		item := s.subscriptionWithCovers(ctx, subscription)
		notice.Title = source.FirstNonEmpty(item.Title, first.ComicTitle)
		notice.Author = source.FirstNonEmpty(item.Author, first.ComicAuthor)
		notice.CoverURL = source.FirstNonEmpty(item.Cover, first.ComicCover)
		notice.CoverFile = s.localCoverFileForSubscription(item)
		notice.CoverBlur = item.CoverBlur
	}
	for _, job := range jobs {
		notice.Chapters = append(notice.Chapters, job.ChapterTitle)
	}
	s.telegram.DownloadRetryScheduled(ctx, notice)
}

// BotSearch runs one page of a content source search.
func (s *Server) BotSearch(ctx context.Context, sourceID, query string, page, limit int) (telegram.BotSearchPage, error) {
	adapter, err := s.registry.Get(sourceID)
	if err != nil {
		return telegram.BotSearchPage{}, err
	}
	account, err := s.registry.Account(ctx, sourceID)
	if err != nil {
		return telegram.BotSearchPage{}, err
	}
	if page < 1 {
		page = 1
	}
	result, err := adapter.Search(ctx, account, strings.TrimSpace(query), page)
	if err != nil {
		return telegram.BotSearchPage{}, err
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
	hasMore := result.HasMore || (result.Pages > 0 && page < result.Pages) || (limit > 0 && len(result.Items) > limit)
	return telegram.BotSearchPage{Items: items, HasMore: hasMore}, nil
}

func (s *Server) BotBrowse(ctx context.Context, sourceID string, page int) (telegram.BotBrowsePage, error) {
	if page < 1 {
		page = 1
	}
	adapter, err := s.registry.Get(sourceID)
	if err != nil {
		return telegram.BotBrowsePage{}, err
	}
	account, err := s.registry.Account(ctx, sourceID)
	if err != nil {
		return telegram.BotBrowsePage{}, err
	}
	options := browseOptionsFromFilterValues(s.sourceBrowseFilters(ctx)[sourceID])
	result, err := adapter.Browse(ctx, account, options, page)
	if err != nil {
		return telegram.BotBrowsePage{}, err
	}
	info := adapter.Info()
	kind := source.KindOf(info)
	items := make([]model.Comic, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, s.annotateDiscover(info.ID, kind, item))
	}
	return telegram.BotBrowsePage{
		Items:   items,
		Page:    page,
		HasMore: result.HasMore,
	}, nil
}

// BotSubscriptionVisual returns the current subscription cover and blur policy
// used by download lifecycle notifications.
func (s *Server) BotSubscriptionVisual(ctx context.Context, sourceID, comicID string) (telegram.BotSubscriptionVisual, error) {
	subscription, err := s.store.SubscriptionByComic(ctx, sourceID, comicID)
	if err != nil {
		return telegram.BotSubscriptionVisual{}, err
	}
	item := s.subscriptionWithCovers(ctx, subscription)
	sourceName := item.SourceID
	if adapter, err := s.registry.Get(item.SourceID); err == nil {
		sourceName = adapter.Info().Name
	}
	author, _ := s.subscriptionLocalMetadata(item)
	return telegram.BotSubscriptionVisual{
		Title:     item.Title,
		Author:    source.FirstNonEmpty(item.Author, author),
		Source:    sourceName,
		CoverURL:  item.Cover,
		CoverFile: s.localCoverFileForSubscription(item),
		CoverBlur: item.CoverBlur,
		InPlan:    item.ArchivedAt == nil && !item.Temporary,
	}, nil
}

// BotSubscribe creates a subscription with the same defaults the web UI uses:
// source default download location, simplified conversion, historical chapters
// and weekly automatic updates.
// BotSubscribe 建订阅并入队历史章节，第三个返回值是这次入队的章节数。
func (s *Server) BotSubscribe(ctx context.Context, sourceID, comicID string) (model.Subscription, int, error) {
	adapter, err := s.registry.Get(sourceID)
	if err != nil {
		return model.Subscription{}, 0, err
	}
	account, err := s.registry.Account(ctx, sourceID)
	if err != nil {
		return model.Subscription{}, 0, err
	}
	comic, err := s.loadComicDetail(ctx, adapter, account, comicID)
	if err != nil {
		return model.Subscription{}, 0, err
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
		CoverBlur:           settings.CoverBlur,
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
		return model.Subscription{}, 0, err
	}
	_ = s.configs.PersistSubscriptions(ctx, s.store)
	// Historical chapters download right away; the stored baseline keeps later
	// scheduled checks from re-queueing them.
	s.registerInitialDownloadNotice(ctx, created)
	queued, _, queueErr := s.scheduler.QueueAllManual(ctx, created)
	if queueErr == nil && queued == 0 {
		s.unregisterInitialDownloadNotice(ctx, created.SourceID, created.ComicID)
	} else if queueErr != nil {
		if pending, _ := s.store.InitialDownloadBatchPending(ctx, created.SourceID, created.ComicID); !pending {
			s.unregisterInitialDownloadNotice(ctx, created.SourceID, created.ComicID)
		}
	}
	s.engine.Notify()
	return created, queued, nil
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
		result = append(result, s.botSubscriptionBase(item))
	}
	return result, nil
}

// BotSubscriptionDetail enriches one subscription only when an interactive
// delete card is opened, keeping list and check commands lightweight.
func (s *Server) BotSubscriptionDetail(ctx context.Context, subscriptionID int64) (telegram.BotSubscription, error) {
	item, err := s.store.Subscription(ctx, subscriptionID)
	if err != nil {
		return telegram.BotSubscription{}, err
	}
	result := s.botSubscriptionBase(item)
	visual := s.subscriptionWithCovers(ctx, item)
	author, description := s.subscriptionLocalMetadata(visual)
	kind := ""
	if adapter, err := s.registry.Get(visual.SourceID); err == nil {
		kind = source.KindOf(adapter.Info())
	}
	if strings.TrimSpace(description) == "" && kind != "" && s.discover != nil {
		if cached, ok := s.discover.Get(visual.SourceID, kind, visual.ComicID); ok {
			description = cached.Comic.Description
		}
	}
	result.Title = visual.Title
	result.Author = source.FirstNonEmpty(visual.Author, author)
	result.Description = description
	result.CoverURL = visual.Cover
	result.CoverFile = s.localCoverFileForSubscription(visual)
	result.CoverBlur = visual.CoverBlur
	return result, nil
}

func (s *Server) botSubscriptionBase(item model.Subscription) telegram.BotSubscription {
	name := item.SourceID
	if adapter, err := s.registry.Get(item.SourceID); err == nil {
		name = adapter.Info().Name
	}
	return telegram.BotSubscription{
		ID:          item.ID,
		Source:      name,
		Title:       item.Title,
		Enabled:     item.Enabled,
		CronExpr:    item.CronExpr,
		DownloadDir: item.DownloadDir,
		ParentDir:   downloadParentDirName(item.DownloadDir),
	}
}

func (s *Server) BotDownloading(ctx context.Context) (telegram.BotDownloadingSnapshot, error) {
	running, err := s.store.ListRunningJobs(ctx)
	if err != nil {
		return telegram.BotDownloadingSnapshot{}, err
	}
	next, hasNext, err := s.store.NextQueuedJob(ctx)
	if err != nil {
		return telegram.BotDownloadingSnapshot{}, err
	}
	snapshot := telegram.BotDownloadingSnapshot{
		Running: make([]telegram.BotDownloadItem, 0, len(running)),
	}
	for _, job := range running {
		snapshot.Running = append(snapshot.Running, telegram.BotDownloadItem{
			ComicTitle:   job.ComicTitle,
			ChapterTitle: job.ChapterTitle,
		})
	}
	now := time.Now()
	var cooldownUntil time.Time
	if hasNext {
		snapshot.NextQueued = &telegram.BotDownloadItem{
			ComicTitle:   next.ComicTitle,
			ChapterTitle: next.ChapterTitle,
		}
		if s.engine != nil {
			cooldownUntil = s.engine.CooldownUntil(next.SourceID)
		}
	} else if s.engine != nil {
		cooldownUntil = s.engine.GlobalCooldown()
	}
	if wait := cooldownUntil.Sub(now); wait > 0 {
		snapshot.CooldownMinutes = int((wait + time.Minute - 1) / time.Minute)
	}
	return snapshot, nil
}

func downloadParentDirName(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimRight(value, "/")
	if value == "" {
		return ""
	}
	if index := strings.LastIndex(value, "/"); index >= 0 {
		return strings.TrimSpace(value[index+1:])
	}
	return value
}

// BotCheck runs one update check and also retries previously failed chapters.
func (s *Server) BotCheck(ctx context.Context, subscriptionID int64) (telegram.BotCheckResult, error) {
	subscription, err := s.store.Subscription(ctx, subscriptionID)
	if err != nil {
		return telegram.BotCheckResult{}, err
	}
	checked, err := s.scheduler.CheckOneManualDetailed(ctx, subscriptionID)
	if err != nil {
		return telegram.BotCheckResult{}, err
	}
	retried := s.retryFailedForSubscription(ctx, subscription)
	updated, _ := s.store.Subscription(ctx, subscriptionID)
	return telegram.BotCheckResult{
		Title:     source.FirstNonEmpty(updated.Title, subscription.Title),
		New:       checked.NewChapters,
		Queued:    checked.Queued,
		Retried:   retried,
		Completed: updated.CompletedAt != nil || strings.EqualFold(updated.ComicStatus, "已完结"),
	}, nil
}

// BotArchive archives one subscription.
func (s *Server) BotArchive(ctx context.Context, subscriptionID int64) error {
	if err := s.store.ArchiveSubscription(ctx, subscriptionID, "manual"); err != nil {
		return err
	}
	return nil
}

// BotFill queues chapters missing from completion records and reports how many
// were queued or skipped.
func (s *Server) BotFill(ctx context.Context, subscriptionID int64) (int, int, error) {
	subscription, err := s.store.Subscription(ctx, subscriptionID)
	if err != nil {
		return 0, 0, err
	}
	return s.scheduler.QueueAllManual(ctx, subscription)
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
	if comic.ChapterCount <= 0 && len(detail.Chapters) > 0 {
		comic.ChapterCount = len(detail.Chapters)
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
