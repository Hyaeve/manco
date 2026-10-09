// Package api exposes the Manco HTTP API and serves the embedded Vue frontend.
package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/hyaeve/manco/internal/config"
	"github.com/hyaeve/manco/internal/downloader"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/scheduler"
	"github.com/hyaeve/manco/internal/secret"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/sources"
	"github.com/hyaeve/manco/internal/store"
)

const (
	sessionCookie = "manco_session"
	sessionTTL    = 30 * 24 * time.Hour
)

type Server struct {
	cfg       config.Config
	store     *store.Store
	box       *secret.Box
	registry  *sources.Registry
	engine    *downloader.Engine
	scheduler *scheduler.Scheduler
	logger    *log.Logger
	assets    fs.FS
}

type Options struct {
	Config    config.Config
	Store     *store.Store
	Box       *secret.Box
	Registry  *sources.Registry
	Engine    *downloader.Engine
	Scheduler *scheduler.Scheduler
	Logger    *log.Logger
	Assets    fs.FS
}

func New(options Options) *Server {
	logger := options.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		cfg:       options.Config,
		store:     options.Store,
		box:       options.Box,
		registry:  options.Registry,
		engine:    options.Engine,
		scheduler: options.Scheduler,
		logger:    logger,
		assets:    options.Assets,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))

	mux.HandleFunc("GET /api/sources", s.requireAuth(s.handleSources))
	mux.HandleFunc("PUT /api/sources/{id}/account", s.requireAuth(s.handleSaveAccount))
	mux.HandleFunc("DELETE /api/sources/{id}/account", s.requireAuth(s.handleDeleteAccount))
	mux.HandleFunc("GET /api/sources/{id}/search", s.requireAuth(s.handleSearch))
	mux.HandleFunc("GET /api/sources/{id}/browse", s.requireAuth(s.handleBrowse))
	mux.HandleFunc("GET /api/sources/{id}/comics/{comicId}", s.requireAuth(s.handleComic))
	mux.HandleFunc("GET /api/sources/{id}/comics/{comicId}/chapters", s.requireAuth(s.handleChapters))

	mux.HandleFunc("GET /api/subscriptions", s.requireAuth(s.handleListSubscriptions))
	mux.HandleFunc("POST /api/subscriptions", s.requireAuth(s.handleCreateSubscription))
	mux.HandleFunc("PATCH /api/subscriptions/{id}", s.requireAuth(s.handleUpdateSubscription))
	mux.HandleFunc("DELETE /api/subscriptions/{id}", s.requireAuth(s.handleDeleteSubscription))
	mux.HandleFunc("POST /api/subscriptions/{id}/check", s.requireAuth(s.handleCheckSubscription))

	mux.HandleFunc("GET /api/downloads", s.requireAuth(s.handleListDownloads))
	mux.HandleFunc("POST /api/downloads", s.requireAuth(s.handleCreateDownload))
	mux.HandleFunc("POST /api/downloads/{id}/retry", s.requireAuth(s.handleRetryDownload))
	mux.HandleFunc("DELETE /api/downloads/{id}", s.requireAuth(s.handleDeleteDownload))

	mux.HandleFunc("GET /api/library", s.requireAuth(s.handleLibrary))
	mux.HandleFunc("GET /api/settings", s.requireAuth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", s.requireAuth(s.handlePutSettings))
	mux.HandleFunc("GET /api/stats", s.requireAuth(s.handleStats))
	mux.HandleFunc("GET /api/proxy/image", s.requireAuth(s.handleImageProxy))

	mux.HandleFunc("/", s.handleSPA)
	return withRecovery(mux, s.logger)
}

// EnsureDefaultUser creates the bootstrap administrator when the users table is
// still empty.
func (s *Server) EnsureDefaultUser(ctx context.Context) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.store.EnsureUser(ctx, s.cfg.AdminUser, string(hash))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := s.store.UserByUsername(r.Context(), strings.TrimSpace(payload.Username))
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(payload.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "用户名或密码不正确")
		return
	}
	token, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法创建会话")
		return
	}
	expires := time.Now().Add(sessionTTL)
	if err := s.store.CreateSession(r.Context(), hashToken(token), user.ID, expires); err != nil {
		writeError(w, http.StatusInternalServerError, "无法保存会话")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure,
		Expires:  expires,
	})
	writeJSON(w, http.StatusOK, map[string]any{"user": map[string]any{"id": user.ID, "username": user.Username}})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(r.Context(), hashToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"user": map[string]any{"id": user.ID, "username": user.Username}})
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	type accountView struct {
		SourceID  string    `json:"sourceId"`
		Username  string    `json:"username,omitempty"`
		HomeURL   string    `json:"homeUrl,omitempty"`
		HasToken  bool      `json:"hasToken"`
		HasCookie bool      `json:"hasCookie"`
		UpdatedAt time.Time `json:"updatedAt"`
	}
	accounts := map[string]accountView{}
	stored, err := s.store.ListSourceAccounts(r.Context())
	if err == nil {
		for _, account := range stored {
			accounts[account.SourceID] = accountView{
				SourceID:  account.SourceID,
				Username:  account.Username,
				HomeURL:   account.HomeURL,
				HasToken:  account.TokenCipher != "",
				HasCookie: account.CookieCipher != "",
				UpdatedAt: account.UpdatedAt,
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    s.registry.List(),
		"accounts": accounts,
		"repoUrl":  s.sourceRepo(r.Context()),
	})
}

func (s *Server) handleSaveAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.registry.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Token    string `json:"token"`
		Cookie   string `json:"cookie"`
		HomeURL  string `json:"homeUrl"`
		Login    bool   `json:"login"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := s.registry.Account(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	account := existing
	if strings.TrimSpace(payload.Username) != "" {
		account.Username = strings.TrimSpace(payload.Username)
	}
	if payload.Password != "" {
		account.Password = payload.Password
	}
	if payload.Token != "" {
		account.Token = payload.Token
	}
	if payload.Cookie != "" {
		account.Cookie = payload.Cookie
	}
	if strings.TrimSpace(payload.HomeURL) != "" {
		account.HomeURL = strings.TrimRight(strings.TrimSpace(payload.HomeURL), "/")
	}
	if payload.Login {
		loginSource, ok := item.(source.LoginSource)
		if !ok {
			writeError(w, http.StatusBadRequest, "该源不支持账号密码登录")
			return
		}
		result, err := loginSource.Login(r.Context(), account)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		account.Token = result.Token
		if result.Username != "" {
			account.Username = result.Username
		}
	}
	secretCipher, err := s.box.Encrypt(account.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tokenCipher, err := s.box.Encrypt(account.Token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cookieCipher, err := s.box.Encrypt(account.Cookie)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.UpsertSourceAccount(r.Context(), model.SourceAccount{
		SourceID:     id,
		Username:     account.Username,
		SecretCipher: secretCipher,
		TokenCipher:  tokenCipher,
		CookieCipher: cookieCipher,
		HomeURL:      account.HomeURL,
		Extra:        json.RawMessage(`{}`),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "connected": account.Token != "" || account.Cookie != ""})
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.registry.RemoveAccount(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	item, account, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "缺少搜索关键词")
		return
	}
	page := intParam(r, "page", 1)
	result, err := item.Search(r.Context(), account, query, page)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	item, account, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	page := intParam(r, "page", 1)
	result, err := item.Browse(r.Context(), account, kind, page)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleComic(w http.ResponseWriter, r *http.Request) {
	item, account, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	comic, err := item.Detail(r.Context(), account, r.PathValue("comicId"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	chapters, err := item.Chapters(r.Context(), account, comic.ID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, model.ComicDetail{Comic: comic, Chapters: chapters})
}

func (s *Server) handleChapters(w http.ResponseWriter, r *http.Request) {
	item, account, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	chapters, err := item.Chapters(r.Context(), account, r.PathValue("comicId"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": chapters})
}

func (s *Server) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListSubscriptions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []model.Subscription{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		SourceID     string  `json:"sourceId"`
		ComicID      string  `json:"comicId"`
		Title        string  `json:"title"`
		Cover        string  `json:"cover"`
		Author       string  `json:"author"`
		AutoDownload *bool   `json:"autoDownload"`
		Enabled      *bool   `json:"enabled"`
		Baseline     string  `json:"baseline"`
		LastOrder    float64 `json:"lastChapterOrder"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(payload.SourceID) == "" || strings.TrimSpace(payload.ComicID) == "" {
		writeError(w, http.StatusBadRequest, "缺少漫画源或作品 ID")
		return
	}
	item, err := s.registry.Get(payload.SourceID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if strings.TrimSpace(payload.Title) == "" || strings.TrimSpace(payload.Cover) == "" {
		account, _ := s.registry.Account(r.Context(), payload.SourceID)
		if comic, err := item.Detail(r.Context(), account, payload.ComicID); err == nil {
			payload.Title = source.FirstNonEmpty(payload.Title, comic.Title)
			payload.Cover = source.FirstNonEmpty(payload.Cover, comic.Cover)
			payload.Author = source.FirstNonEmpty(payload.Author, comic.Author)
		}
	}
	subscription := model.Subscription{
		SourceID:         payload.SourceID,
		ComicID:          payload.ComicID,
		Title:            source.FirstNonEmpty(payload.Title, payload.ComicID),
		Cover:            payload.Cover,
		Author:           payload.Author,
		Enabled:          payload.Enabled == nil || *payload.Enabled,
		AutoDownload:     payload.AutoDownload == nil || *payload.AutoDownload,
		LastChapterID:    strings.TrimSpace(payload.Baseline),
		LastChapterOrder: payload.LastOrder,
		LastChapterTitle: "",
	}
	saved, err := s.store.UpsertSubscription(r.Context(), subscription)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的订阅 ID")
		return
	}
	existing, err := s.store.Subscription(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	payload := struct {
		Enabled      *bool `json:"enabled"`
		AutoDownload *bool `json:"autoDownload"`
	}{}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := existing.Enabled
	auto := existing.AutoDownload
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	if payload.AutoDownload != nil {
		auto = *payload.AutoDownload
	}
	if err := s.store.UpdateSubscription(r.Context(), id, enabled, auto); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := s.store.Subscription(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的订阅 ID")
		return
	}
	if err := s.store.DeleteSubscription(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCheckSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的订阅 ID")
		return
	}
	if _, err := s.store.Subscription(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	go s.scheduler.RunOnce(context.Background())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListDownloads(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListDownloadJobs(r.Context(), intParam(r, "limit", 200))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []model.DownloadJob{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateDownload(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		SourceID     string          `json:"sourceId"`
		ComicID      string          `json:"comicId"`
		ComicTitle   string          `json:"comicTitle"`
		ComicCover   string          `json:"comicCover"`
		AutoDownload bool            `json:"autoDownload"`
		Chapters     []model.Chapter `json:"chapters"`
		Chapter      *model.Chapter  `json:"chapter"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	chapters := payload.Chapters
	if payload.Chapter != nil {
		chapters = append(chapters, *payload.Chapter)
	}
	if strings.TrimSpace(payload.SourceID) == "" || strings.TrimSpace(payload.ComicID) == "" || len(chapters) == 0 {
		writeError(w, http.StatusBadRequest, "缺少漫画源、作品或章节")
		return
	}
	title := strings.TrimSpace(payload.ComicTitle)
	if title == "" {
		title = payload.ComicID
	}
	created := make([]model.DownloadJob, 0, len(chapters))
	for _, chapter := range chapters {
		if strings.TrimSpace(chapter.ID) == "" {
			continue
		}
		saved, err := s.store.CreateDownloadJob(r.Context(), model.DownloadJob{
			SourceID:     payload.SourceID,
			ComicID:      payload.ComicID,
			ComicTitle:   title,
			ComicCover:   payload.ComicCover,
			ChapterID:    chapter.ID,
			ChapterTitle: source.FirstNonEmpty(chapter.Title, chapter.ID),
			ChapterOrder: chapter.Order,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		created = append(created, saved)
	}
	if payload.AutoDownload && len(created) > 0 {
		if _, err := s.store.UpsertSubscription(r.Context(), model.Subscription{
			SourceID:         payload.SourceID,
			ComicID:          payload.ComicID,
			Title:            title,
			Cover:            payload.ComicCover,
			Enabled:          true,
			AutoDownload:     true,
			LastChapterID:    created[len(created)-1].ChapterID,
			LastChapterTitle: created[len(created)-1].ChapterTitle,
			LastChapterOrder: created[len(created)-1].ChapterOrder,
		}); err != nil {
			s.logger.Printf("api: auto subscribe: %v", err)
		}
	}
	s.engine.Notify()
	writeJSON(w, http.StatusOK, map[string]any{"items": created})
}

func (s *Server) handleRetryDownload(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的下载任务 ID")
		return
	}
	if err := s.engine.Retry(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeleteDownload(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的下载任务 ID")
		return
	}
	removeFile := r.URL.Query().Get("removeFile") == "true"
	if removeFile {
		if job, err := s.store.DownloadJob(r.Context(), id); err == nil && job.FilePath != "" {
			if err := removeDownloadedFile(s.cfg.DownloadDir, job.FilePath); err != nil {
				s.logger.Printf("api: remove %s: %v", job.FilePath, err)
			}
		}
	}
	if err := s.store.DeleteDownloadJob(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.ListDownloadJobs(r.Context(), 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type chapterItem struct {
		ID       int64   `json:"id"`
		Title    string  `json:"title"`
		Order    float64 `json:"order"`
		FilePath string  `json:"filePath"`
		Status   string  `json:"status"`
		Pages    int     `json:"pages"`
	}
	type comicItem struct {
		SourceID string        `json:"sourceId"`
		ComicID  string        `json:"comicId"`
		Title    string        `json:"title"`
		Cover    string        `json:"cover"`
		Chapters []chapterItem `json:"chapters"`
	}
	index := map[string]int{}
	comics := make([]comicItem, 0, 32)
	for _, job := range jobs {
		if job.Status != "completed" {
			continue
		}
		key := job.SourceID + "\x00" + job.ComicID
		position, ok := index[key]
		if !ok {
			index[key] = len(comics)
			position = len(comics)
			comics = append(comics, comicItem{
				SourceID: job.SourceID,
				ComicID:  job.ComicID,
				Title:    job.ComicTitle,
				Cover:    job.ComicCover,
			})
		}
		comics[position].Chapters = append(comics[position].Chapters, chapterItem{
			ID:       job.ID,
			Title:    job.ChapterTitle,
			Order:    job.ChapterOrder,
			FilePath: job.FilePath,
			Status:   job.Status,
			Pages:    job.TotalPages,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": comics, "downloadDir": s.cfg.DownloadDir})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"repoUrl":               s.sourceRepo(r.Context()),
		"downloadDir":           s.cfg.DownloadDir,
		"dataDir":               s.cfg.DataDir,
		"scanInterval":          s.cfg.ScanInterval.String(),
		"maxChapterConcurrency": s.cfg.MaxChapterConcurrency,
		"maxPageConcurrency":    s.cfg.MaxPageConcurrency,
	})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		RepoURL string `json:"repoUrl"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(payload.RepoURL) != "" {
		if err := s.store.SetSetting(r.Context(), "source_repo", strings.TrimSpace(payload.RepoURL)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "repoUrl": s.sourceRepo(r.Context())})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleImageProxy(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("url"))
	if raw == "" {
		writeError(w, http.StatusBadRequest, "缺少 url 参数")
		return
	}
	target, err := url.Parse(raw)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" {
		writeError(w, http.StatusBadRequest, "无效的图片地址")
		return
	}
	if !s.registry.AllowedImageHost(target.Hostname()) {
		writeError(w, http.StatusForbidden, "该域名不在允许的图片源白名单内")
		return
	}
	if err := ensurePublicHost(r.Context(), target.Hostname()); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	request.Header.Set("User-Agent", source.DefaultUserAgent)
	request.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	referer := strings.TrimSpace(r.URL.Query().Get("referer"))
	if referer != "" {
		request.Header.Set("Referer", referer)
	}
	sourceID := strings.TrimSpace(r.URL.Query().Get("sourceId"))
	if sourceID != "" {
		if account, err := s.registry.Account(r.Context(), sourceID); err == nil && account.Cookie != "" {
			request.Header.Set("Cookie", account.Cookie)
		}
	}
	response, err := s.registry.Client().Do(request)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("上游返回 HTTP %d", response.StatusCode))
		return
	}
	contentType := response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = io.Copy(w, io.LimitReader(response.Body, 32<<20))
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if s.assets == nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "接口不存在")
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	if file, err := s.assets.Open(name); err == nil {
		_ = file.Close()
		http.FileServer(http.FS(s.assets)).ServeHTTP(w, r)
		return
	}
	index, err := fs.ReadFile(s.assets, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}

func (s *Server) resolveSource(w http.ResponseWriter, r *http.Request) (source.Source, source.Account, bool) {
	id := r.PathValue("id")
	item, err := s.registry.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return nil, source.Account{}, false
	}
	account, err := s.registry.Account(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, source.Account{}, false
	}
	return item, account, true
}

func (s *Server) sourceRepo(ctx context.Context) string {
	if value, err := s.store.Setting(ctx, "source_repo"); err == nil && strings.TrimSpace(value) != "" {
		return value
	}
	return s.cfg.SourceRepo
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		user, err := s.store.UserBySession(r.Context(), hashToken(cookie.Value), time.Now())
		if err != nil {
			writeError(w, http.StatusUnauthorized, "会话已过期，请重新登录")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), userContextKey{}, user)))
	}
}

type userContextKey struct{}

func userFromContext(ctx context.Context) (model.User, bool) {
	user, ok := ctx.Value(userContextKey{}).(model.User)
	return user, ok
}

func withRecovery(next http.Handler, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Printf("api: panic: %v", recovered)
				writeError(w, http.StatusInternalServerError, "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(r *http.Request, out any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("请求体不能为空")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func intParam(r *http.Request, name string, fallback int) int {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func randomToken(length int) (string, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func removeDownloadedFile(root, file string) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	if target != absoluteRoot && !strings.HasPrefix(target, absoluteRoot+string(filepath.Separator)) {
		return errors.New("拒绝删除下载目录之外的文件")
	}
	return os.Remove(target)
}

func ensurePublicHost(ctx context.Context, host string) error {
	addresses, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return fmt.Errorf("无法解析图片域名: %w", err)
	}
	for _, address := range addresses {
		ip := net.ParseIP(address)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return errors.New("图片地址指向内网，已拒绝代理")
		}
	}
	return nil
}
