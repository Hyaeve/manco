package api

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/hyaeve/manco/internal/downloader"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/store"
	"github.com/hyaeve/manco/internal/telegram"
)

const (
	bangumiMatchThreshold = 0.72
	bangumiMatchTimeout   = 45 * time.Second
)

type subscriptionMatchCandidate struct {
	comic    model.Comic
	score    float64
	order    int
	sourceID string
}

func (s *Server) finishBangumiSubscriptionMatch(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), bangumiMatchTimeout+30*time.Second)
	defer cancel()
	subscription, err := s.store.Subscription(ctx, id)
	if err != nil {
		s.logger.Printf("api: load pending Bangumi subscription %d: %v", id, err)
		return
	}
	matched, ok, err := s.MatchSubscription(ctx, subscription)
	if err != nil {
		s.logger.Printf("api: match Bangumi subscription %q: %v", subscription.Title, err)
		return
	}
	if !ok {
		return
	}
	if s.scheduler != nil {
		if _, err := s.scheduler.CheckOneDetailed(ctx, matched.ID); err != nil {
			s.logger.Printf("api: initialize matched Bangumi subscription %q: %v", matched.Title, err)
		}
	}
}

func (s *Server) MatchSubscription(ctx context.Context, subscription model.Subscription) (model.Subscription, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(subscription.SourceID), "bangumi") {
		return subscription, true, nil
	}
	targetKind := subscriptionMatchKind(subscription)
	hidden := s.hiddenSources(ctx)
	infos := s.registry.List()
	candidates := make([]subscriptionMatchCandidate, 0, len(infos))
	var resultsMu sync.Mutex
	sem := make(chan struct{}, 3)
	var wait sync.WaitGroup
	matchCtx, cancel := context.WithTimeout(ctx, bangumiMatchTimeout)
	defer cancel()
	for order, info := range infos {
		if info.Hidden || hidden[info.ID] || !info.CanSearch || source.KindOf(info) != targetKind {
			continue
		}
		wait.Add(1)
		go func(order int, info model.SourceInfo) {
			defer wait.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-matchCtx.Done():
				return
			}
			item, err := s.registry.Get(info.ID)
			if err != nil {
				return
			}
			account, err := s.registry.Account(matchCtx, info.ID)
			if err != nil {
				return
			}
			found, err := item.Search(matchCtx, account, subscription.Title, 1)
			if err != nil {
				return
			}
			for _, comic := range found.Items {
				score := subscriptionTitleScore(subscription.Title, comic.Title)
				if subscriptionAuthorMatches(subscription.Author, comic.Author) {
					score = min(1, score+0.05)
				}
				if score >= bangumiMatchThreshold {
					resultsMu.Lock()
					candidates = append(candidates, subscriptionMatchCandidate{
						comic: comic, score: score, order: order, sourceID: info.ID,
					})
					resultsMu.Unlock()
				}
			}
		}(order, info)
	}
	wait.Wait()
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].order < candidates[j].order
		}
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) == 0 {
		message := "未找到足够相似的内容源作品"
		if matchCtx.Err() != nil {
			message = "内容源匹配超时"
		}
		_ = s.store.MarkSubscriptionMatchAttempt(ctx, subscription.ID, "unmatched", message)
		updated, err := s.store.Subscription(ctx, subscription.ID)
		if err != nil {
			return subscription, false, err
		}
		return updated, false, nil
	}

	best := candidates[0]
	existing, findErr := s.store.SubscriptionByComic(ctx, best.sourceID, best.comic.ID)
	if findErr == nil && existing.ID != subscription.ID {
		message := "匹配到的内容源作品已经在订阅计划中"
		_ = s.store.MarkSubscriptionMatchAttempt(ctx, subscription.ID, "unmatched", message)
		updated, err := s.store.Subscription(ctx, subscription.ID)
		if err != nil {
			return subscription, false, err
		}
		return updated, false, nil
	}
	if findErr != nil && !errors.Is(findErr, store.ErrNotFound) {
		return subscription, false, findErr
	}
	if err := s.store.MatchSubscription(
		ctx, subscription.ID, best.sourceID, best.comic.ID, best.comic.Author, best.comic.Status,
	); err != nil {
		_ = s.store.MarkSubscriptionMatchAttempt(ctx, subscription.ID, "error", err.Error())
		return subscription, false, err
	}
	updated, err := s.store.Subscription(ctx, subscription.ID)
	if err != nil {
		return subscription, false, err
	}
	if s.telegram != nil && s.telegram.Enabled() {
		matchedComic, sourceName := s.bangumiMatchedComic(ctx, best)
		coverFile := ""
		if s.discover != nil {
			coverFile = s.discover.CachedCoverPath(best.sourceID, best.comic.ID)
		}
		s.telegram.BangumiMatched(ctx, telegram.BangumiMatchedNotice{
			Title:         matchedComic.Title,
			OriginalTitle: subscription.Title,
			Source:        sourceName,
			Author:        source.FirstNonEmpty(matchedComic.Author, updated.Author),
			Description:   matchedComic.Description,
			Status:        matchedComic.Status,
			CoverURL:      source.FirstNonEmpty(matchedComic.Cover, updated.Cover),
			CoverFile:     coverFile,
			CoverBlur:     updated.CoverBlur,
		})
	}
	s.logger.Printf(
		"api: matched Bangumi subscription %q to %s/%s (score %.2f)",
		subscription.Title, best.sourceID, best.comic.ID, best.score,
	)
	return updated, true, nil
}

func (s *Server) bangumiMatchedComic(ctx context.Context, candidate subscriptionMatchCandidate) (model.Comic, string) {
	comic := candidate.comic
	if strings.TrimSpace(comic.SourceID) == "" {
		comic.SourceID = candidate.sourceID
	}
	adapter, err := s.registry.Get(candidate.sourceID)
	if err != nil {
		return comic, candidate.sourceID
	}
	sourceName := source.FirstNonEmpty(adapter.Info().Name, candidate.sourceID)
	account, err := s.registry.Account(ctx, candidate.sourceID)
	if err != nil {
		return comic, sourceName
	}
	detailCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	detail, err := adapter.Detail(detailCtx, account, candidate.comic.ID)
	if err != nil {
		return comic, sourceName
	}
	comic.Title = source.FirstNonEmpty(detail.Title, comic.Title)
	comic.Cover = source.FirstNonEmpty(detail.Cover, comic.Cover)
	comic.Author = source.FirstNonEmpty(detail.Author, comic.Author)
	comic.Description = source.FirstNonEmpty(detail.Description, comic.Description)
	comic.Status = source.FirstNonEmpty(detail.Status, comic.Status)
	comic.SourceURL = source.FirstNonEmpty(detail.SourceURL, comic.SourceURL)
	return comic, sourceName
}

func subscriptionMatchKind(subscription model.Subscription) string {
	if strings.EqualFold(strings.TrimSpace(subscription.MatchKind), source.KindBook) {
		return source.KindBook
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(subscription.OriginSourceID)), "bangumi-novel") {
		return source.KindBook
	}
	return source.KindComic
}

func subscriptionTitleScore(original, candidate string) float64 {
	left := normalizeMatchText(original)
	right := normalizeMatchText(candidate)
	if left == "" || right == "" {
		return 0
	}
	if left == right {
		return 1
	}
	shorter, longer := left, right
	if len([]rune(shorter)) > len([]rune(longer)) {
		shorter, longer = longer, shorter
	}
	if strings.Contains(longer, shorter) {
		ratio := float64(len([]rune(shorter))) / float64(len([]rune(longer)))
		return 0.86 + 0.12*ratio
	}
	leftPairs := matchBigrams(left)
	rightPairs := matchBigrams(right)
	if len(leftPairs) == 0 || len(rightPairs) == 0 {
		return 0
	}
	overlap := 0
	for pair := range leftPairs {
		if rightPairs[pair] {
			overlap++
		}
	}
	return 2 * float64(overlap) / float64(len(leftPairs)+len(rightPairs))
}

func subscriptionAuthorMatches(original, candidate string) bool {
	left := normalizeMatchText(original)
	right := normalizeMatchText(candidate)
	if left == "" || right == "" {
		return false
	}
	return strings.Contains(left, right) || strings.Contains(right, left)
}

func normalizeMatchText(value string) string {
	value = downloader.SimplifiedText(strings.ToLower(strings.TrimSpace(value)))
	var builder strings.Builder
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func matchBigrams(value string) map[string]bool {
	runes := []rune(value)
	result := map[string]bool{}
	if len(runes) < 2 {
		if len(runes) == 1 {
			result[string(runes)] = true
		}
		return result
	}
	for index := 0; index < len(runes)-1; index++ {
		result[string(runes[index:index+2])] = true
	}
	return result
}
