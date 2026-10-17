// Package api exposes the Manco HTTP API and serves the embedded Vue frontend.
package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	htmlpkg "html"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	nethtml "golang.org/x/net/html"

	"github.com/hyaeve/manco/internal/config"
	"github.com/hyaeve/manco/internal/configstore"
	"github.com/hyaeve/manco/internal/cronutil"
	"github.com/hyaeve/manco/internal/discovercache"
	"github.com/hyaeve/manco/internal/diskcache"
	"github.com/hyaeve/manco/internal/downloader"
	"github.com/hyaeve/manco/internal/iconcache"
	"github.com/hyaeve/manco/internal/localops"
	"github.com/hyaeve/manco/internal/logbuf"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/pluginruntime"
	"github.com/hyaeve/manco/internal/repository"
	"github.com/hyaeve/manco/internal/scheduler"
	"github.com/hyaeve/manco/internal/secret"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/source/generic"
	"github.com/hyaeve/manco/internal/sources"
	"github.com/hyaeve/manco/internal/store"
	"github.com/hyaeve/manco/internal/subscriptioncover"
	"github.com/hyaeve/manco/internal/telegram"
)

const (
	sessionCookie = "manco_session"
	sessionTTL    = 30 * 24 * time.Hour
	appVersion    = "v0.6.8"
	// 并发上限：章节是全局同时下载数，图片是单章内并发。原来的 1-8 / 1-16
	// 是早期「每源并发 × 来源数」时代的经验值，现在全局上限独立了，放宽即可。
	maxChapterConcurrencyLimit = 32
	maxPageConcurrencyLimit    = 32
	// 探索发现列表回填：每页最多补 12 条、并发 3。
	discoverEnrichLimit       = 12
	discoverEnrichConcurrency = 3
)

var managedSourceIDs = []string{"picacg", "jmcomic", "baozimh", "biquge", "zlibrary"}

var sourceIconSources = map[string]string{
	"picacg":   "https://bica.mom/favicon.jpeg",
	"jmcomic":  "https://jmcomicapp.net/favicon.ico",
	"baozimh":  "https://www.webmota.com/favicon.ico",
	"zlibrary": "https://1lib.sk/favicon.ico",
	"biquge":   "https://www.biquge345.com/favicon.ico",
}

var sourceIconFallbacks = map[string]string{
	"picacg":   "/source-icons/picacg.png",
	"jmcomic":  "/source-icons/jmcomic.png",
	"baozimh":  "/source-icons/baozimh.png",
	"zlibrary": "/source-icons/zlibrary.png",
	"biquge":   "/source-icons/biquge345.ico",
}

type Server struct {
	cfg                config.Config
	store              *store.Store
	configs            *configstore.Files
	cache              *diskcache.Cache
	discover           *discovercache.Cache
	subscriptionCovers *subscriptioncover.Cache
	box                *secret.Box
	registry           *sources.Registry
	runtime            *pluginruntime.Manager
	engine             *downloader.Engine
	icons              *iconcache.Cache
	scheduler          *scheduler.Scheduler
	logger             *log.Logger
	logs               *logbuf.Buffer
	assets             fs.FS
	telegram           *telegram.Service

	discoverQueueOnce sync.Once
	discoverQueue     chan discoverCacheTask

	// 作品目录 → 本地 cover 的短期缓存：订阅页每 5 秒轮询一次，逐个
	// filepath.Glob 在 NAS 上会被放大成「订阅数 × 轮询次数」次目录扫描。
	coverCacheMu sync.Mutex
	coverCache   map[string]coverCacheEntry

	initialDownloadMu sync.Mutex

	// 活动通知的短缓存：前端每 15 秒轮询一次，同一份结果没必要重复聚合。
	activityMu  sync.Mutex
	activityRaw []byte
	activityAt  time.Time

	// 本地仓库扫描结果短缓存：NAS 目录扫描很慢，刷新页面不应该重扫一遍。
	libraryMu    sync.Mutex
	libraryItems []localItemEntry
	libraryRoots []string
	libraryKey   string
	libraryAt    time.Time
}

type coverCacheEntry struct {
	value        string
	expires      time.Time
	folderMod    int64
	folderExists bool
}

const localCoverCacheTTL = 30 * time.Second

var pluginCoverFetchSlots = make(chan struct{}, 2)

type discoverCacheTask struct {
	sourceID   string
	sourceName string
	kind       string
	items      []model.Comic
}

// Settings mirrors the persisted values in the settings table.
type Settings struct {
	RepoURL               string
	ScanInterval          time.Duration
	MaxChapterConcurrency int
	MaxPageConcurrency    int
	Proxy                 string
	ProxyUsername         string
	ProxyPassword         string
	CookieSecure          bool
	SessionTTLDays        int
	SourceConcurrency     map[string]int
	BatchSize             int
	BatchIntervalMinutes  int
	ConvertToSimplified   bool
	StaleDays             map[string]int
	LogMaxEntries         int
	LogRetentionDays      int
	TelegramEnabled       bool
	TelegramToken         string
	TelegramChatIDs       []string
}

type Options struct {
	Config             config.Config
	Store              *store.Store
	Configs            *configstore.Files
	Cache              *diskcache.Cache
	Discover           *discovercache.Cache
	SubscriptionCovers *subscriptioncover.Cache
	Box                *secret.Box
	Registry           *sources.Registry
	Runtime            *pluginruntime.Manager
	Engine             *downloader.Engine
	Icons              *iconcache.Cache
	Scheduler          *scheduler.Scheduler
	Logger             *log.Logger
	Logs               *logbuf.Buffer
	Assets             fs.FS
	Telegram           *telegram.Service
}

func New(options Options) *Server {
	logger := options.Logger
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		cfg:                options.Config,
		store:              options.Store,
		configs:            options.Configs,
		cache:              options.Cache,
		discover:           options.Discover,
		subscriptionCovers: options.SubscriptionCovers,
		box:                options.Box,
		registry:           options.Registry,
		runtime:            options.Runtime,
		engine:             options.Engine,
		icons:              options.Icons,
		scheduler:          options.Scheduler,
		logger:             logger,
		logs:               options.Logs,
		assets:             options.Assets,
		telegram:           options.Telegram,
	}
}

// Settings loads the persisted runtime settings with defaults applied.
func (s *Server) Settings(ctx context.Context) (Settings, error) {
	return s.loadSettings(ctx), nil
}

func (s *Server) loadSettings(ctx context.Context) Settings {
	current := Settings{
		RepoURL:               s.cfg.SourceRepo,
		ScanInterval:          s.cfg.ScanInterval,
		MaxChapterConcurrency: s.cfg.MaxChapterConcurrency,
		MaxPageConcurrency:    s.cfg.MaxPageConcurrency,
		// 全局批量暂停：所有内容源加起来每下载 30 章，整体暂停 5 分钟。
		BatchSize:            30,
		BatchIntervalMinutes: 5,
		CookieSecure:         s.cfg.CookieSecure,
		SessionTTLDays:       30,
		SourceConcurrency: map[string]int{
			"picacg":   s.cfg.MaxChapterConcurrency,
			"jmcomic":  s.cfg.MaxChapterConcurrency,
			"baozimh":  s.cfg.MaxChapterConcurrency,
			"zlibrary": s.cfg.MaxChapterConcurrency,
			"biquge":   s.cfg.MaxChapterConcurrency,
		},
		StaleDays: map[string]int{
			"picacg":   0,
			"jmcomic":  0,
			"baozimh":  0,
			"zlibrary": 0,
			"biquge":   0,
			"*":        30,
		},
	}
	if value, err := s.store.Setting(ctx, "source_repo"); err == nil && strings.TrimSpace(value) != "" {
		current.RepoURL = strings.TrimSpace(value)
	}
	if value, err := s.store.Setting(ctx, "scan_interval"); err == nil && value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			current.ScanInterval = parsed
		}
	}
	if value, err := s.store.Setting(ctx, "max_chapter_concurrency"); err == nil && value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 1 {
			current.MaxChapterConcurrency = parsed
		}
	}
	if value, err := s.store.Setting(ctx, "max_page_concurrency"); err == nil && value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 1 {
			current.MaxPageConcurrency = parsed
		}
	}
	if value, err := s.store.Setting(ctx, "telegram_enabled"); err == nil && value != "" {
		current.TelegramEnabled = value == "true" || value == "1"
	}
	if value, err := s.store.Setting(ctx, "telegram_token"); err == nil {
		current.TelegramToken = strings.TrimSpace(value)
	}
	if value, err := s.store.Setting(ctx, "telegram_chats"); err == nil && strings.TrimSpace(value) != "" {
		var ids []string
		if json.Unmarshal([]byte(value), &ids) == nil {
			current.TelegramChatIDs = ids
		}
	}
	if value, err := s.store.Setting(ctx, "proxy"); err == nil {
		current.Proxy = strings.TrimSpace(value)
	}
	if value, err := s.store.Setting(ctx, "proxy_username"); err == nil {
		current.ProxyUsername = strings.TrimSpace(value)
	}
	if value, err := s.store.Setting(ctx, "proxy_password"); err == nil {
		current.ProxyPassword = value
	}
	if value, err := s.store.Setting(ctx, "cookie_secure"); err == nil && value != "" {
		current.CookieSecure = value == "true"
	}
	if value, err := s.store.Setting(ctx, "session_ttl_days"); err == nil && value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 1 && parsed <= 3650 {
			current.SessionTTLDays = parsed
		}
	}
	if value, err := s.store.Setting(ctx, "source_concurrency"); err == nil && strings.TrimSpace(value) != "" {
		parsed := map[string]int{}
		if json.Unmarshal([]byte(value), &parsed) == nil {
			for sourceID, limit := range parsed {
				if limit >= 1 && limit <= maxChapterConcurrencyLimit {
					current.SourceConcurrency[sourceID] = limit
				}
			}
		}
	}
	if value, err := s.store.Setting(ctx, "batch_size"); err == nil && value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 {
			current.BatchSize = parsed
		}
	}
	if value, err := s.store.Setting(ctx, "batch_interval_minutes"); err == nil && value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 {
			current.BatchIntervalMinutes = parsed
		}
	}
	current.LogMaxEntries = 10000
	if value, err := s.store.Setting(ctx, "log_max_entries"); err == nil && value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			current.LogMaxEntries = parsed
		}
	}
	current.LogRetentionDays = 30
	if value, err := s.store.Setting(ctx, "log_retention_days"); err == nil && value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 {
			current.LogRetentionDays = parsed
		}
	}
	if value, err := s.store.Setting(ctx, "convert_to_simplified"); err == nil && value != "" {
		current.ConvertToSimplified = value == "true"
	}
	if value, err := s.store.Setting(ctx, "stale_days"); err == nil && strings.TrimSpace(value) != "" {
		parsed := map[string]int{}
		if json.Unmarshal([]byte(value), &parsed) == nil {
			for sourceID, days := range parsed {
				if days >= 0 {
					current.StaleDays[sourceID] = days
				}
			}
		}
	}
	return current
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/auth/setup", s.handleSetup)
	mux.HandleFunc("POST /api/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))

	mux.HandleFunc("GET /api/sources", s.requireAuth(s.handleSources))
	mux.HandleFunc("GET /api/icons", s.requireAuth(s.handleListIcons))
	mux.HandleFunc("POST /api/icons", s.requireAuth(s.handleUploadIcon))
	mux.HandleFunc("POST /api/icons/download", s.requireAuth(s.handleDownloadIcon))
	mux.HandleFunc("GET /api/icons/{id}", s.requireAuth(s.handleSourceIcon))
	mux.HandleFunc("POST /api/sources/{id}/icon", s.requireAuth(s.handleUpdateSourceIcon))
	mux.HandleFunc("GET /api/source-repo", s.requireAuth(s.handleSourceRepo))
	mux.HandleFunc("GET /api/repositories", s.requireAuth(s.handleListRepositories))
	mux.HandleFunc("POST /api/repositories", s.requireAuth(s.handleCreateRepository))
	mux.HandleFunc("PUT /api/repositories/{id}", s.requireAuth(s.handleUpdateRepository))
	mux.HandleFunc("POST /api/repositories/{id}/sync", s.requireAuth(s.handleSyncRepository))
	mux.HandleFunc("DELETE /api/repositories/{id}", s.requireAuth(s.handleDeleteRepository))
	mux.HandleFunc("POST /api/repositories/{id}/extensions/{extensionId}", s.requireAuth(s.handleImportRepositoryExtension))
	mux.HandleFunc("POST /api/plugins/prune", s.requireAuth(s.handlePrunePlugins))
	mux.HandleFunc("POST /api/plugins/install-apk", s.requireAuth(s.handleInstallAPK))
	mux.HandleFunc("POST /api/custom-sources", s.requireAuth(s.handleCreateCustomSource))
	mux.HandleFunc("GET /api/custom-sources/{id}", s.requireAuth(s.handleGetCustomSource))
	mux.HandleFunc("PUT /api/custom-sources/{id}", s.requireAuth(s.handleUpdateCustomSource))
	mux.HandleFunc("PUT /api/custom-sources/{id}/name", s.requireAuth(s.handleRenameCustomSource))
	mux.HandleFunc("DELETE /api/custom-sources/{id}", s.requireAuth(s.handleDeleteCustomSource))
	mux.HandleFunc("PATCH /api/sources/{id}", s.requireAuth(s.handleUpdateSource))
	mux.HandleFunc("PUT /api/sources/order", s.requireAuth(s.handleSourceOrder))
	mux.HandleFunc("PUT /api/sources/{id}/account", s.requireAuth(s.handleSaveAccount))
	mux.HandleFunc("GET /api/sources/{id}/plugin-auth", s.requireAuth(s.handlePluginAuthCapabilities))
	mux.HandleFunc("POST /api/sources/{id}/plugin-auth/login", s.requireAuth(s.handlePluginCredentialLogin))
	mux.HandleFunc("GET /api/sources/{id}/plugin-preferences", s.requireAuth(s.handlePluginPreferences))
	mux.HandleFunc("PUT /api/sources/{id}/plugin-preferences", s.requireAuth(s.handlePluginPreferenceUpdate))
	mux.HandleFunc("GET /api/sources/{id}/browser-session", s.requireAuth(s.handleSourceBrowserSession))
	mux.HandleFunc("POST /api/sources/{id}/cookies", s.requireAuth(s.handleImportSourceCookies))
	mux.HandleFunc("DELETE /api/sources/{id}/account", s.requireAuth(s.handleDeleteAccount))
	mux.HandleFunc("GET /api/sources/{id}/search", s.requireAuth(s.handleSearch))
	mux.HandleFunc("GET /api/search", s.requireAuth(s.handleGlobalSearch))
	mux.HandleFunc("GET /api/sources/{id}/browse", s.requireAuth(s.handleBrowse))
	mux.HandleFunc("GET /api/sources/{id}/filters", s.requireAuth(s.handleSourceFilters))
	mux.HandleFunc("GET /api/sources/{id}/comics/{comicId}", s.requireAuth(s.handleComic))
	mux.HandleFunc("GET /api/discover/cover", s.requireAuth(s.handleDiscoverCover))
	mux.HandleFunc("GET /api/subscription-cover", s.requireAuth(s.handleSubscriptionCover))
	mux.HandleFunc("GET /api/sources/{id}/comics/{comicId}/chapters", s.requireAuth(s.handleChapters))
	mux.HandleFunc("POST /api/sources/{id}/comics/{comicId}/preview", s.requireAuth(s.handleChapterPreview))
	mux.HandleFunc("GET /api/sources/{id}/comics/{comicId}/preview/image", s.requireAuth(s.handleChapterPreviewImage))
	mux.HandleFunc("GET /api/sources/{id}/comics/{comicId}/downloads", s.requireAuth(s.handleComicDownloads))

	mux.HandleFunc("GET /api/subscriptions", s.requireAuth(s.handleListSubscriptions))
	mux.HandleFunc("GET /api/subscriptions/archived", s.requireAuth(s.handleListArchivedSubscriptions))
	mux.HandleFunc("GET /api/subscriptions/match", s.requireAuth(s.handleMatchSubscription))
	mux.HandleFunc("POST /api/subscriptions", s.requireAuth(s.handleCreateSubscription))
	mux.HandleFunc("PATCH /api/subscriptions/{id}", s.requireAuth(s.handleUpdateSubscription))
	mux.HandleFunc("DELETE /api/subscriptions/{id}", s.requireAuth(s.handleDeleteSubscription))
	mux.HandleFunc("POST /api/subscriptions/{id}/check", s.requireAuth(s.handleCheckSubscription))
	mux.HandleFunc("POST /api/subscriptions/{id}/download", s.requireAuth(s.handleDownloadSubscription))
	mux.HandleFunc("POST /api/subscriptions/{id}/download-all", s.requireAuth(s.handleDownloadAllSubscription))
	mux.HandleFunc("POST /api/subscriptions/{id}/fill", s.requireAuth(s.handleFillSubscription))
	mux.HandleFunc("POST /api/subscriptions/{id}/scrape", s.requireAuth(s.handleScrapeSubscription))
	mux.HandleFunc("POST /api/subscriptions/{id}/chapter-marker/backfill", s.requireAuth(s.handleBackfillChapterMarkers))
	mux.HandleFunc("POST /api/subscriptions/{id}/archive", s.requireAuth(s.handleArchiveSubscription))
	mux.HandleFunc("POST /api/subscriptions/{id}/restore", s.requireAuth(s.handleRestoreSubscription))

	mux.HandleFunc("GET /api/downloads", s.requireAuth(s.handleListDownloads))
	mux.HandleFunc("POST /api/downloads", s.requireAuth(s.handleCreateDownload))
	mux.HandleFunc("POST /api/downloads/{id}/retry", s.requireAuth(s.handleRetryDownload))
	mux.HandleFunc("DELETE /api/downloads/{id}", s.requireAuth(s.handleDeleteDownload))

	mux.HandleFunc("GET /api/library", s.requireAuth(s.handleLibrary))
	mux.HandleFunc("GET /api/local", s.requireAuth(s.handleLocalLibrary))
	mux.HandleFunc("GET /api/local/file", s.requireAuth(s.handleLocalFile))
	mux.HandleFunc("GET /api/local/cbz", s.requireAuth(s.handleLocalCBZ))
	mux.HandleFunc("GET /api/local/cbz/file", s.requireAuth(s.handleLocalCBZFile))
	mux.HandleFunc("GET /api/local/epub", s.requireAuth(s.handleLocalEPUB))
	mux.HandleFunc("GET /api/local/epub/file", s.requireAuth(s.handleLocalEPUBFile))
	mux.HandleFunc("POST /api/local/merge", s.requireAuth(s.handleLocalMerge))
	mux.HandleFunc("POST /api/local/simplify", s.requireAuth(s.handleLocalSimplify))
	mux.HandleFunc("POST /api/local/delete", s.requireAuth(s.handleLocalDelete))
	mux.HandleFunc("GET /api/settings", s.requireAuth(s.handleGetSettings))
	mux.HandleFunc("GET /api/version", s.requireAuth(s.handleVersion))
	mux.HandleFunc("GET /api/update-check", s.requireAuth(s.handleUpdateCheck))
	mux.HandleFunc("PUT /api/settings", s.requireAuth(s.handlePutSettings))
	mux.HandleFunc("GET /api/stats", s.requireAuth(s.handleStats))
	mux.HandleFunc("GET /api/dashboard/upcoming", s.requireAuth(s.handleDashboardUpcoming))
	mux.HandleFunc("GET /api/logs", s.requireAuth(s.handleLogs))
	mux.HandleFunc("GET /api/activity", s.requireAuth(s.handleActivity))
	mux.HandleFunc("GET /api/download-directories", s.requireAuth(s.handleDownloadDirectories))
	mux.HandleFunc("POST /api/download-directories", s.requireAuth(s.handleCreateDownloadDirectory))
	mux.HandleFunc("POST /api/tools/series-dir-name", s.requireAuth(s.handleSeriesDirectoryName))
	mux.HandleFunc("GET /api/proxy/image", s.requireAuth(s.handleImageProxy))

	mux.HandleFunc("/", s.handleSPA)
	return withRecovery(mux, s.logger)
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法读取用户状态")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"setupRequired": count == 0})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	username := strings.TrimSpace(payload.Username)
	if username == "" || len(payload.Password) < 8 {
		writeError(w, http.StatusBadRequest, "用户名不能为空，密码至少需要 8 个字符")
		return
	}
	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法读取用户状态")
		return
	}
	if count > 0 {
		writeError(w, http.StatusConflict, "系统已完成初始化，请使用已有账号登录")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "无法保存密码")
		return
	}
	if err := s.store.CreateUser(r.Context(), username, string(hash)); err != nil {
		if errors.Is(err, store.ErrUsernameTaken) {
			writeError(w, http.StatusConflict, "用户名已存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "无法创建用户")
		return
	}
	_ = s.configs.PersistAccounts(r.Context(), s.store)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "user": map[string]any{"username": username}})
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
	sessionTTL := s.sessionTTL(r.Context())
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
		MaxAge:   int(sessionTTL / time.Second),
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
	hidden := s.hiddenSources(r.Context())
	type accountView struct {
		Settings    model.SourceDownloadSettings `json:"settings"`
		SourceID    string                       `json:"sourceId"`
		Username    string                       `json:"username,omitempty"`
		Password    string                       `json:"password,omitempty"`
		UserAgent   string                       `json:"userAgent,omitempty"`
		HomeURL     string                       `json:"homeUrl,omitempty"`
		UseProxy    bool                         `json:"useProxy"`
		ReloginDays int                          `json:"reloginDays"`
		HasToken    bool                         `json:"hasToken"`
		HasCookie   bool                         `json:"hasCookie"`
		UpdatedAt   time.Time                    `json:"updatedAt"`
	}
	accounts := map[string]accountView{}
	stored, err := s.store.ListSourceAccounts(r.Context())
	if err == nil {
		for _, account := range stored {
			plainPassword, _ := s.box.Decrypt(account.SecretCipher)
			plain, _ := source.AccountFromModel(s.box, account)
			reloginDays := 0
			if item, err := s.registry.Get(account.SourceID); err == nil {
				if _, ok := item.(source.LoginSource); ok {
					fallback := 0
					if account.SourceID == "picacg" {
						fallback = source.DefaultReloginDays
					}
					reloginDays = source.AccountReloginDays(plain, fallback)
				}
			}
			accounts[account.SourceID] = accountView{
				Settings:    s.sourceDownloadSettings(account.Extra),
				SourceID:    account.SourceID,
				Username:    account.Username,
				Password:    plainPassword,
				UserAgent:   plain.UserAgent,
				HomeURL:     account.HomeURL,
				UseProxy:    source.AccountUsesProxy(plain),
				ReloginDays: reloginDays,
				HasToken:    account.TokenCipher != "",
				HasCookie:   account.CookieCipher != "",
				UpdatedAt:   account.UpdatedAt,
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    s.filteredSourceList(hidden, s.disabledCategories(r.Context())),
		"accounts": accounts,
		"repoUrl":  s.sourceRepo(r.Context()),
	})
}

func (s *Server) handleSourceIcon(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	if id == "" || s.icons == nil {
		http.NotFound(w, r)
		return
	}
	if file, entry, err := s.icons.Open(id); err == nil {
		defer file.Close()
		http.ServeContent(w, r, entry.File, entry.UpdatedAt, file)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := s.fetchSourceIcon(ctx, id); err != nil {
		if fallback := sourceIconFallbacks[id]; fallback != "" {
			http.Redirect(w, r, fallback, http.StatusTemporaryRedirect)
			return
		}
		http.NotFound(w, r)
		return
	}
	file, entry, err := s.icons.Open(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	http.ServeContent(w, r, entry.File, entry.UpdatedAt, file)
}

// fetchSourceIcon resolves the default favicon URL for a source and caches it.
func (s *Server) fetchSourceIcon(ctx context.Context, id string) (iconcache.Entry, error) {
	if s.icons == nil {
		return iconcache.Entry{}, errors.New("图标缓存未启用")
	}
	sourceURL := strings.TrimSpace(sourceIconSources[id])
	if sourceURL == "" {
		if item, err := s.registry.Get(id); err == nil {
			if homepage := strings.TrimRight(strings.TrimSpace(item.Info().Homepage), "/"); homepage != "" {
				sourceURL = homepage + "/favicon.ico"
			}
		}
	}
	if sourceURL == "" {
		return iconcache.Entry{}, errors.New("该内容源没有可用的默认图标地址")
	}
	return s.icons.Fetch(ctx, id, sourceURL)
}

// localizeCustomSourceIcon downloads the candidate/source favicon to /data/icon
// and persists a local URL when the download succeeds. A failed download keeps
// the original URL so the frontend can still fall back to its type icon.
func (s *Server) localizeCustomSourceIcon(ctx context.Context, item model.CustomSource) model.CustomSource {
	if s.icons == nil {
		return item
	}
	id := strings.ToLower(strings.TrimSpace(item.ID))
	if id == "" {
		return item
	}
	if s.icons.Has(id) {
		item.Icon = s.icons.URL(id)
		return item
	}
	if data, ok := decodeIconDataURI(item.Icon); ok {
		if _, err := s.icons.Save(id, data, ""); err == nil {
			item.Icon = s.icons.URL(id)
			return item
		}
	}
	sourceURL := remoteIconURL(item.Icon)
	if sourceURL == "" {
		sourceURL = strings.TrimSpace(sourceIconSources[id])
	}
	if sourceURL == "" {
		homepage := strings.TrimRight(strings.TrimSpace(item.Homepage), "/")
		if homepage != "" {
			sourceURL = homepage + "/favicon.ico"
		}
	}
	if sourceURL == "" {
		return s.saveFallbackSourceIcon(item)
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if _, err := s.icons.Fetch(fetchCtx, id, sourceURL); err != nil {
		return s.saveFallbackSourceIcon(item)
	}
	item.Icon = s.icons.URL(id)
	return item
}

func decodeIconDataURI(value string) ([]byte, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(strings.ToLower(value), "data:") {
		return nil, false
	}
	comma := strings.Index(value, ",")
	if comma < 0 {
		return nil, false
	}
	meta := value[5:comma]
	payload := value[comma+1:]
	var data []byte
	var err error
	if strings.Contains(strings.ToLower(meta), ";base64") {
		payload = strings.Join(strings.Fields(payload), "")
		data, err = base64.StdEncoding.DecodeString(payload)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(payload)
		}
	} else {
		var decoded string
		decoded, err = url.PathUnescape(payload)
		data = []byte(decoded)
	}
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

func (s *Server) saveFallbackSourceIcon(item model.CustomSource) model.CustomSource {
	if s.icons == nil || strings.TrimSpace(item.ID) == "" {
		return item
	}
	accent := "#e07a2f"
	glyph := `<path d="M18 24h28M18 32h28M18 40h18"/><path d="M14 16h36v32H14z"/>`
	switch source.KindOf(model.SourceInfo{Kind: item.Kind}) {
	case source.KindBook:
		accent = "#4f7cff"
		glyph = `<path d="M16 16h24a8 8 0 0 1 8 8v24H24a8 8 0 0 0-8 8z"/><path d="M24 16v32"/>`
	case source.KindOther:
		accent = "#3fa66a"
		glyph = `<rect x="14" y="16" width="36" height="32" rx="6"/><path d="m27 25 12 7-12 7z"/>`
	}
	payload := []byte(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="16" fill="#fff7ed"/><g fill="none" stroke="%s" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">%s</g></svg>`,
		accent,
		glyph,
	))
	if _, err := s.icons.Save(item.ID, payload, ".svg"); err == nil {
		item.Icon = s.icons.URL(item.ID)
	}
	return item
}

func remoteIconURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return parsed.String()
}

// handleListIcons lists the locally cached icons under /data/icon.
func (s *Server) handleListIcons(w http.ResponseWriter, r *http.Request) {
	if s.icons == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	backfillCtx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	s.ensureCustomSourceIcons(backfillCtx)
	cancel()
	entries := s.icons.List()
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		items = append(items, map[string]any{
			"id":        entry.ID,
			"name":      entry.ID,
			"url":       s.icons.URL(entry.ID),
			"updatedAt": entry.UpdatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ensureCustomSourceIcons backfills icons for sources added before local icon
// indexing was enforced. Every custom source receives either its downloaded
// icon or a type-specific local SVG fallback.
func (s *Server) ensureCustomSourceIcons(ctx context.Context) {
	if s.icons == nil || s.store == nil {
		return
	}
	items, err := s.store.ListCustomSources(ctx)
	if err != nil {
		return
	}
	missing := make([]model.CustomSource, 0, len(items))
	for _, item := range items {
		if !s.icons.Has(item.ID) {
			missing = append(missing, item)
		}
	}
	if len(missing) == 0 {
		return
	}
	type result struct {
		index int
		item  model.CustomSource
	}
	results := make(chan result, len(missing))
	work := make(chan int)
	workers := 4
	if len(missing) < workers {
		workers = len(missing)
	}
	var wait sync.WaitGroup
	wait.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wait.Done()
			for index := range work {
				item := missing[index]
				results <- result{index: index, item: s.localizeCustomSourceIcon(ctx, item)}
			}
		}()
	}
	for index := range missing {
		work <- index
	}
	close(work)
	wait.Wait()
	close(results)
	for outcome := range results {
		item := outcome.item
		original := missing[outcome.index]
		if strings.TrimSpace(item.Icon) == strings.TrimSpace(original.Icon) {
			continue
		}
		if _, err := s.store.UpsertCustomSource(ctx, item); err != nil {
			s.logger.Printf("api: persist localized source icon %s: %v", item.ID, err)
		}
	}
}

// handleUploadIcon 接收用户上传的图标图片并保存到本地图标库。
func (s *Server) handleUploadIcon(w http.ResponseWriter, r *http.Request) {
	if s.icons == nil {
		writeError(w, http.StatusServiceUnavailable, "图标库不可用")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := r.ParseMultipartForm(6 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "无效的上传请求")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "请选择要上传的图片")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (5<<20)+1))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取上传图片失败")
		return
	}
	if len(data) == 0 || len(data) > 5<<20 {
		writeError(w, http.StatusBadRequest, "图片为空或超过 5MB")
		return
	}
	id := sanitizeIconID(strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename)))
	if id == "" {
		id = fmt.Sprintf("upload-%d", time.Now().Unix())
	}
	base := id
	for attempt := 1; ; attempt++ {
		if _, _, openErr := s.icons.Open(id); openErr != nil {
			break
		}
		id = fmt.Sprintf("%s-%d", base, attempt)
	}
	entry, err := s.icons.Save(id, data, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"item": map[string]any{
			"id":   entry.ID,
			"name": entry.ID,
			"url":  s.icons.URL(entry.ID),
		},
	})
}

// sanitizeIconID 只保留可安全用于文件名与索引的字符。
func sanitizeIconID(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// iconIDFromURL 用图标地址的域名推断图标库里的名称，例如 https://bica.mom/favicon.jpeg -> bica-mom。
func iconIDFromURL(parsed *url.URL) string {
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	host = strings.TrimPrefix(host, "www.")
	id := sanitizeIconID(strings.ReplaceAll(host, ".", "-"))
	if id == "" {
		return sanitizeIconID(parsed.Path)
	}
	return id
}

// handleDownloadIcon 从用户输入的网址下载图标，写入本地图标库 /data/icon。
func (s *Server) handleDownloadIcon(w http.ResponseWriter, r *http.Request) {
	if s.icons == nil {
		writeError(w, http.StatusServiceUnavailable, "图标库不可用")
		return
	}
	var payload struct {
		URL string `json:"url"`
		ID  string `json:"id"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rawURL := strings.TrimSpace(payload.URL)
	if rawURL == "" {
		writeError(w, http.StatusBadRequest, "请输入图标地址")
		return
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeError(w, http.StatusBadRequest, "图标地址无效")
		return
	}
	id := sanitizeIconID(payload.ID)
	if id == "" {
		id = iconIDFromURL(parsed)
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "无法从地址推断图标名称")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	// 允许用同一地址重新下载覆盖已有图标，Fetch 默认会命中缓存直接返回。
	_ = s.icons.Delete(id)
	entry, err := s.icons.Fetch(ctx, id, rawURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "图标下载失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"item": map[string]any{
			"id":   entry.ID,
			"name": entry.ID,
			"url":  s.icons.URL(entry.ID),
		},
	})
}

// handleUpdateSourceIcon fetches, selects or resets the icon of one source.
func (s *Server) handleUpdateSourceIcon(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	if id == "" || s.icons == nil {
		writeError(w, http.StatusBadRequest, "无效的内容源 ID")
		return
	}
	if _, err := s.registry.Get(id); err != nil {
		writeError(w, http.StatusNotFound, "内容源不存在")
		return
	}
	var payload struct {
		Action string `json:"action"`
		IconID string `json:"iconId"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "无效的请求内容")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	switch strings.ToLower(strings.TrimSpace(payload.Action)) {
	case "reset":
		if err := s.icons.Delete(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_, _ = s.fetchSourceIcon(ctx, id)
	case "fetch":
		if err := s.icons.Delete(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := s.fetchSourceIcon(ctx, id); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "select":
		iconID := strings.TrimSpace(payload.IconID)
		file, _, err := s.icons.Open(iconID)
		if err != nil {
			writeError(w, http.StatusNotFound, "本地图标库中没有该图标")
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 5<<20))
		_ = file.Close()
		if readErr != nil {
			writeError(w, http.StatusInternalServerError, readErr.Error())
			return
		}
		if _, err := s.icons.Save(id, data, ""); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "未知的图标操作")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "icon": s.icons.URL(id)})
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": appVersion})
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var latest, releaseURL string
	// GitHub 的公开 Atom 订阅不消耗 api.github.com 的匿名额度，
	// 避免用户共享网络出口被 API 限流后检查更新直接失败。
	atomState, atomErr := s.fetchGitHubAtom(ctx, "https://github.com/Hyaeve/manco/releases.atom", &latest, &releaseURL)
	if atomState != http.StatusOK || strings.TrimSpace(latest) == "" {
		atomState, atomErr = s.fetchGitHubAtom(ctx, "https://github.com/Hyaeve/manco/tags.atom", &latest, &releaseURL)
	}
	if atomState != http.StatusOK || strings.TrimSpace(latest) == "" {
		releaseState, releaseErr := s.fetchGitHubUpdate(ctx, "https://api.github.com/repos/Hyaeve/manco/releases/latest", true, &latest, &releaseURL)
		if releaseState != http.StatusOK {
			tagState, tagErr := s.fetchGitHubUpdate(ctx, "https://api.github.com/repos/Hyaeve/manco/tags?per_page=1", false, &latest, &releaseURL)
			if tagState != http.StatusOK {
				message := "GitHub 更新服务不可用"
				if atomErr != nil {
					message = atomErr.Error()
				} else if releaseErr != nil {
					message = releaseErr.Error()
				} else if tagErr != nil {
					message = tagErr.Error()
				}
				writeError(w, http.StatusBadGateway, message)
				return
			}
		}
	}
	latest = strings.TrimSpace(latest)
	if latest == "" {
		writeError(w, http.StatusBadGateway, "GitHub 未返回可用版本标签")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"current":   appVersion,
		"latest":    latest,
		"url":       releaseURL,
		"hasUpdate": compareVersions(latest, appVersion) > 0,
	})
}

func (s *Server) fetchGitHubUpdate(ctx context.Context, endpoint string, release bool, latest, releaseURL *string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "Manco/"+appVersion)
	response, err := s.registry.Client().Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return response.StatusCode, fmt.Errorf("GitHub 更新服务返回 HTTP %d", response.StatusCode)
	}
	if release {
		var payload struct {
			TagName string `json:"tag_name"`
			HTMLURL string `json:"html_url"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
			return 0, err
		}
		*latest = payload.TagName
		*releaseURL = payload.HTMLURL
		return http.StatusOK, nil
	}
	var payload []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return 0, err
	}
	if len(payload) == 0 || strings.TrimSpace(payload[0].Name) == "" {
		return http.StatusOK, nil
	}
	latestValue := strings.TrimSpace(payload[0].Name)
	*latest = latestValue
	*releaseURL = "https://github.com/Hyaeve/manco/releases/tag/" + url.PathEscape(latestValue)
	return http.StatusOK, nil
}

func compareVersions(left, right string) int {
	parse := func(value string) []int {
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		value = strings.SplitN(value, "-", 2)[0]
		parts := strings.Split(value, ".")
		result := make([]int, len(parts))
		for index, part := range parts {
			result[index], _ = strconv.Atoi(part)
		}
		return result
	}
	a, b := parse(left), parse(right)
	length := len(a)
	if len(b) > length {
		length = len(b)
	}
	for index := 0; index < length; index++ {
		var leftValue, rightValue int
		if index < len(a) {
			leftValue = a[index]
		}
		if index < len(b) {
			rightValue = b[index]
		}
		if leftValue > rightValue {
			return 1
		}
		if leftValue < rightValue {
			return -1
		}
	}
	return 0
}
func (s *Server) sourceDownloadSettings(raw json.RawMessage) model.SourceDownloadSettings {
	settings := model.SourceDownloadSettings{
		ChapterConcurrency:   1,
		PageConcurrency:      2,
		BatchSize:            5,
		BatchIntervalMinutes: 5,
	}
	extra := map[string]any{}
	if len(raw) > 0 && json.Unmarshal(raw, &extra) == nil {
		if value, ok := extra["downloadSettings"]; ok {
			encoded, _ := json.Marshal(value)
			_ = json.Unmarshal(encoded, &settings)
		}
	}
	if settings.ChapterConcurrency < 1 {
		settings.ChapterConcurrency = 1
	}
	if settings.PageConcurrency < 1 {
		settings.PageConcurrency = 1
	}
	return settings
}

func (s *Server) allSourceDownloadSettings(ctx context.Context) map[string]model.SourceDownloadSettings {
	result := map[string]model.SourceDownloadSettings{}
	// 每个已注册内容源都先落一份默认下载策略（章 1 线程 / 图 2 线程 /
	// 每 5 章暂停 5 分钟），这样尚未保存过账号的来源也能拿到一致默认值。
	for _, info := range s.registry.List() {
		result[info.ID] = s.sourceDownloadSettings(nil)
	}
	accounts, err := s.store.ListSourceAccounts(ctx)
	if err != nil {
		return result
	}
	for _, account := range accounts {
		result[account.SourceID] = s.sourceDownloadSettings(account.Extra)
	}
	return result
}

// SourceSettings returns the current per-source policy for the download engine.
func (s *Server) SourceSettings(ctx context.Context) map[string]model.SourceDownloadSettings {
	return s.allSourceDownloadSettings(ctx)
}
func (s *Server) filteredSourceList(hidden map[string]bool, disabled map[string][]string) []model.SourceInfo {
	items := s.registry.List()
	orderSetting := s.sourceOrder(context.Background())
	customRepositories := map[string]string{}
	if customSources, err := s.store.ListCustomSources(context.Background()); err == nil {
		repositoriesByName := map[string]string{}
		if repositories, repoErr := s.store.ListExtensionRepositories(context.Background()); repoErr == nil {
			for _, repository := range repositories {
				repositoriesByName[repository.URL] = repository.Name
			}
		}
		for _, custom := range customSources {
			if repositoryName := repositoriesByName[custom.RepoURL]; repositoryName != "" {
				customRepositories[custom.ID] = repositoryName
			}
		}
	}
	for index := range items {
		// 内置来源始终使用本地 /data/icon 缓存；其他来源仅在用户
		// 自定义过图标时（缓存里已有文件）才覆盖清单自带图标。
		_, builtin := sourceIconSources[items[index].ID]
		if s.icons != nil && (builtin || s.icons.Has(items[index].ID)) {
			items[index].Icon = s.icons.URL(items[index].ID)
		}
		if repositoryName := customRepositories[items[index].ID]; repositoryName != "" {
			items[index].RepositoryName = repositoryName
		}
		items[index].Hidden = hidden[items[index].ID]
		blocked := map[string]bool{}
		for _, value := range disabled[items[index].ID] {
			blocked[value] = true
		}
		for groupIndex := range items[index].Filters {
			for optionIndex := range items[index].Filters[groupIndex].Options {
				option := &items[index].Filters[groupIndex].Options[optionIndex]
				option.Disabled = blocked[option.Value]
			}
		}
	}
	return sortSourceItems(items, orderSetting)
}

// sortSourceItems applies the persisted source order without letting plugin
// sources move ahead of the built-in ones. Built-in sources keep their
// registry order, configured plugin sources follow their saved order, and
// plugin sources missing from the setting stay after them in registry order.
func sortSourceItems(items []model.SourceInfo, orderSetting map[string][]string) []model.SourceInfo {
	registryPosition := make(map[string]int, len(items))
	for index, item := range items {
		registryPosition[item.ID] = index
	}
	orderPositions := make(map[string]map[string]int, len(orderSetting))
	for kind, ids := range orderSetting {
		positions := make(map[string]int, len(ids))
		for index, id := range ids {
			positions[id] = index
		}
		orderPositions[kind] = positions
	}
	// 比较函数必须是严格弱序，否则 sort.SliceStable 会近似于不做任何排序。
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.Builtin != right.Builtin {
			return left.Builtin
		}
		if left.Builtin {
			return registryPosition[left.ID] < registryPosition[right.ID]
		}
		leftKind := source.KindOf(left)
		rightKind := source.KindOf(right)
		if leftKind != rightKind {
			return leftKind < rightKind
		}
		positions := orderPositions[leftKind]
		leftOrder, leftOK := positions[left.ID]
		rightOrder, rightOK := positions[right.ID]
		switch {
		case leftOK && rightOK:
			return leftOrder < rightOrder
		case leftOK != rightOK:
			return leftOK
		default:
			return registryPosition[left.ID] < registryPosition[right.ID]
		}
	})
	return items
}

func (s *Server) hiddenSources(ctx context.Context) map[string]bool {
	hidden := map[string]bool{}
	value, err := s.store.Setting(ctx, "hidden_sources")
	if err != nil || strings.TrimSpace(value) == "" {
		return hidden
	}
	_ = json.Unmarshal([]byte(value), &hidden)
	return hidden
}

func (s *Server) disabledCategories(ctx context.Context) map[string][]string {
	disabled := map[string][]string{}
	value, err := s.store.Setting(ctx, "disabled_categories")
	if err != nil || strings.TrimSpace(value) == "" {
		return disabled
	}
	_ = json.Unmarshal([]byte(value), &disabled)
	return disabled
}

func (s *Server) sourceBrowseFilters(ctx context.Context) map[string]map[string]string {
	filters := map[string]map[string]string{}
	value, err := s.store.Setting(ctx, "source_browse_filters")
	if err != nil || strings.TrimSpace(value) == "" {
		return filters
	}
	_ = json.Unmarshal([]byte(value), &filters)
	return filters
}

func normalizeBrowseFilterValues(values map[string]string) map[string]string {
	normalized := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			normalized[key] = value
		}
	}
	return normalized
}

func browseOptionsFromFilterValues(values map[string]string) model.BrowseOptions {
	options := model.BrowseOptions{}
	for key, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch key {
		case "category", "kind":
			options.Category = value
		case "sort", "order":
			options.Sort = value
		case "state":
			options.State = value
		case "region":
			options.Region = value
		default:
			if options.Extra == nil {
				options.Extra = map[string]string{}
			}
			options.Extra[key] = value
		}
	}
	return options
}

// sourceFilterGroups returns the static filters advertised by a source, or
// asks a runtime-backed source to discover them lazily.
func sourceFilterGroups(ctx context.Context, item source.Source) ([]model.FilterGroup, error) {
	if item == nil {
		return nil, nil
	}
	info := item.Info()
	if len(info.Filters) > 0 {
		return info.Filters, nil
	}
	filterSource, ok := item.(source.FilterSource)
	if !ok {
		return info.Filters, nil
	}
	return filterSource.Filters(ctx)
}

func normalizeDisabledCategories(groups []model.FilterGroup, values []string) []string {
	valid := map[string]bool{}
	for _, group := range groups {
		for _, option := range group.Options {
			if option.Value != "" {
				valid[option.Value] = true
			}
		}
	}
	normalized := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || !valid[value] || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	return normalized
}

func applyDisabledCategories(filters []model.FilterGroup, disabled []string) []model.FilterGroup {
	blocked := map[string]bool{}
	for _, value := range disabled {
		blocked[value] = true
	}
	for groupIndex := range filters {
		for optionIndex := range filters[groupIndex].Options {
			option := &filters[groupIndex].Options[optionIndex]
			option.Disabled = blocked[option.Value]
		}
	}
	return filters
}

// handleSourceOrder persists the drag-and-drop order of plugin sources. The
// order is keyed by source kind and only ever contains plugin sources; built-in
// sources keep their registry order and always stay in front.
//
// 之前前端是借「PATCH 某个内容源」来夹带 order 的，只要那个目标源查不到就整
// 个请求 404，表现就是拖完没有任何效果。现在改成独立入口，前端显式传 kind。
func (s *Server) handleSourceOrder(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Kind  string   `json:"kind"`
		Order []string `json:"order"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind := source.KindOf(model.SourceInfo{Kind: payload.Kind})
	normalized := make([]string, 0, len(payload.Order))
	seen := map[string]bool{}
	for _, value := range payload.Order {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	order := s.sourceOrder(r.Context())
	order[kind] = normalized
	raw, err := json.Marshal(order)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.SetSetting(r.Context(), "source_order", string(raw)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.configs.PersistSettings(r.Context(), s.store)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "order": normalized})
}

func (s *Server) sourceOrder(ctx context.Context) map[string][]string {
	result := map[string][]string{}
	value, err := s.store.Setting(ctx, "source_order")
	if err == nil && strings.TrimSpace(value) != "" {
		_ = json.Unmarshal([]byte(value), &result)
	}
	return result
}

func (s *Server) handleSourceRepo(w http.ResponseWriter, r *http.Request) {
	address := s.sourceRepo(r.Context())
	text, err := source.FetchText(r.Context(), s.registry.Client(), address, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取 Kototoro 拓展仓库失败: "+err.Error())
		return
	}
	var payload any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		writeError(w, http.StatusBadGateway, "拓展仓库返回的不是有效 JSON")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": address, "items": payload})
}

func resolvePluginSourceName(sourceID string) string {
	switch strings.ToLower(strings.TrimSpace(sourceID)) {
	case "baozimh", "baozimh.com":
		return "BAOZIMH"
	case "jmcomic", "18comic":
		return "JMCOMIC"
	case "picacg", "bika":
		return "PICACG"
	case "biquge":
		return "BIQUGE"
	default:
		return ""
	}
}

var pluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// isPluginRuntime reports whether a persisted source config points at one of
// the JVM plugin runtimes hosted by the embedded sidecar.
func isPluginRuntime(runtime string) bool {
	runtime = strings.TrimSpace(runtime)
	return runtime == pluginruntime.RuntimeKototoro || runtime == pluginruntime.RuntimeMihon
}

func pluginRuntimeLabel(runtime string) string {
	if runtime == pluginruntime.RuntimeMihon {
		return "Mihon 插件"
	}
	return "Kototoro 解析器"
}

func (s *Server) pluginSourceName(ctx context.Context, sourceID string) (string, error) {
	sourceID = strings.ToLower(strings.TrimSpace(sourceID))
	if sourceID == "" {
		return "", errors.New("内容源为空")
	}
	item, err := s.store.CustomSource(ctx, sourceID)
	if err != nil {
		if fallback := resolvePluginSourceName(sourceID); fallback != "" {
			return fallback, nil
		}
		return "", errors.New("该内容源不是插件来源")
	}
	var config pluginruntime.Config
	if err := json.Unmarshal(item.Config, &config); err != nil || !isPluginRuntime(config.Runtime) {
		return "", errors.New("该内容源不是插件来源")
	}
	if strings.TrimSpace(config.SourceName) == "" {
		return "", errors.New("插件来源缺少 sourceName")
	}
	return config.SourceName, nil
}

func (s *Server) handleSourceBrowserSession(w http.ResponseWriter, r *http.Request) {
	if s.runtime == nil || s.registry == nil || s.registry.Runtime() == nil {
		writeError(w, http.StatusBadRequest, "Kototoro 解析器运行时未初始化")
		return
	}
	sourceName, err := s.pluginSourceName(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	address, userAgent, err := s.runtime.BrowserSession(ctx, sourceName)
	if err != nil {
		writeError(w, http.StatusBadGateway, "获取浏览器验证地址失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sourceId":   r.PathValue("id"),
		"sourceName": sourceName,
		"url":        address,
		"userAgent":  userAgent,
	})
}

func (s *Server) handleImportSourceCookies(w http.ResponseWriter, r *http.Request) {
	if s.runtime == nil || s.registry == nil || s.registry.Runtime() == nil {
		writeError(w, http.StatusBadRequest, "Kototoro 解析器运行时未初始化")
		return
	}
	var payload struct {
		SourceName string   `json:"sourceName"`
		URL        string   `json:"url"`
		Cookies    []string `json:"cookies"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sourceName := strings.TrimSpace(payload.SourceName)
	if sourceName == "" {
		sourceName, _ = s.pluginSourceName(r.Context(), r.PathValue("id"))
	}
	if sourceName == "" {
		writeError(w, http.StatusBadRequest, "该内容源不是 Kototoro 插件来源，无法导入浏览器 Cookie")
		return
	}
	if len(payload.Cookies) == 0 {
		writeError(w, http.StatusBadRequest, "没有可导入的 Cookie")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	count, err := s.runtime.ImportCookies(ctx, sourceName, strings.TrimSpace(payload.URL), payload.Cookies)
	if err != nil {
		writeError(w, http.StatusBadGateway, "导入浏览器 Cookie 失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "imported": count})
}

func (s *Server) handlePluginAuthCapabilities(w http.ResponseWriter, r *http.Request) {
	if s.runtime == nil || s.registry == nil || s.registry.Runtime() == nil {
		writeError(w, http.StatusBadRequest, "Kototoro 解析器运行时未初始化")
		return
	}
	sourceName, err := s.pluginSourceName(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	capabilities, err := s.runtime.AuthCapabilities(ctx, sourceName)
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取插件认证能力失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sourceId":     r.PathValue("id"),
		"sourceName":   sourceName,
		"capabilities": capabilities,
	})
}

func (s *Server) handlePluginCredentialLogin(w http.ResponseWriter, r *http.Request) {
	if s.runtime == nil || s.registry == nil || s.registry.Runtime() == nil {
		writeError(w, http.StatusBadRequest, "Kototoro 解析器运行时未初始化")
		return
	}
	var payload struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sourceName, err := s.pluginSourceName(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	result, err := s.runtime.LoginCredentials(ctx, sourceName, payload.Username, payload.Password)
	if err != nil {
		writeError(w, http.StatusBadGateway, "插件账号登录失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"sourceId":   r.PathValue("id"),
		"sourceName": sourceName,
		"result":     result,
	})
}

func (s *Server) handlePluginPreferences(w http.ResponseWriter, r *http.Request) {
	if s.runtime == nil || s.registry == nil || s.registry.Runtime() == nil {
		writeError(w, http.StatusBadRequest, "Kototoro 解析器运行时未初始化")
		return
	}
	sourceName, err := s.pluginSourceName(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	preferences, err := s.runtime.Preferences(ctx, sourceName)
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取插件设置失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sourceId":    r.PathValue("id"),
		"sourceName":  sourceName,
		"preferences": preferences,
	})
}

func (s *Server) handlePluginPreferenceUpdate(w http.ResponseWriter, r *http.Request) {
	if s.runtime == nil || s.registry == nil || s.registry.Runtime() == nil {
		writeError(w, http.StatusBadRequest, "Kototoro 解析器运行时未初始化")
		return
	}
	var payload struct {
		Revision string         `json:"revision"`
		NodeID   string         `json:"nodeId"`
		Value    map[string]any `json:"value"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sourceName, err := s.pluginSourceName(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	preferences, err := s.runtime.UpdatePreference(ctx, sourceName, payload.Revision, payload.NodeID, payload.Value)
	if err != nil {
		writeError(w, http.StatusBadGateway, "更新插件设置失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"sourceId":    r.PathValue("id"),
		"sourceName":  sourceName,
		"preferences": preferences,
	})
}

// pluginRuntimeFor selects the JVM runtime that can execute one repository
// extension. Kototoro Parsers ship Kotlin/JVM JARs; Mihon-family extensions
// ship Android APKs that the same sidecar converts to JVM bytecode.
func pluginRuntimeFor(repo model.ExtensionRepository, selected model.RepositoryExtension) string {
	pluginType := strings.ToLower(strings.TrimSpace(firstNonEmptyText(selected.PluginType, repo.Kind)))
	switch pluginType {
	case "mihon", "aniyomi", "tsundoku", "kanade", "ireader", "cloudstream":
		return pluginruntime.RuntimeMihon
	}
	if strings.HasSuffix(strings.ToLower(strings.Split(selected.InstallURL, "?")[0]), ".apk") {
		return pluginruntime.RuntimeMihon
	}
	return pluginruntime.RuntimeKototoro
}

// importRepositoryPlugin installs one JVM extension and returns the candidate
// sources it declares. Installing a repository extension must not silently add
// every source: the user chooses candidates explicitly from the
// resource-library add-source dialog.
func (s *Server) importRepositoryPlugin(w http.ResponseWriter, r *http.Request, repo model.ExtensionRepository, selected model.RepositoryExtension) {
	if s.runtime == nil {
		writeError(w, http.StatusBadRequest, "插件运行时未初始化")
		return
	}
	if strings.TrimSpace(selected.InstallURL) == "" {
		writeError(w, http.StatusBadRequest, "该来源缺少插件安装地址")
		return
	}
	// 枚举要先把插件装上再重启 sidecar，很慢；命中缓存就直接返回上次的结果。
	// 仓库重新同步会带出新版本号，版本不一致时缓存自然失效。
	if r.URL.Query().Get("refresh") != "1" {
		payload, version, err := s.store.ExtensionCandidates(r.Context(), repo.ID, selected.ID)
		if err == nil && version == strings.TrimSpace(selected.Version) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(payload))
			return
		}
	}
	runtimeType := pluginRuntimeFor(repo, selected)
	prefix := "kototoro-"
	if runtimeType == pluginruntime.RuntimeMihon {
		prefix = "mihon-"
	}
	pluginID := prefix + repository.Slug(firstNonEmptyText(selected.PackageName, selected.Name, selected.ID))
	if !pluginIDPattern.MatchString(pluginID) {
		pluginID = prefix + repository.Slug(selected.ID)
	}
	// 只装这一个扩展，并且只返回它新引入的来源：sidecar 的 metadata 会报告
	// 整个运行时里所有插件的内容源，不做差集就会把别的插件来源算到它头上。
	sources, err := s.runtime.InstallIsolated(r.Context(), pluginID, selected.InstallURL, runtimeType)
	if err != nil {
		writeError(w, http.StatusBadGateway, "安装插件失败："+err.Error())
		return
	}
	if len(sources) == 0 {
		// 差集为空通常是因为这个扩展之前已经装过（来源已经躺在运行时里），
		// 这时退回「运行时全部来源里还没被添加过的那些」，不要误报失败。
		sources = s.pendingPluginSources(r.Context())
	}
	if len(sources) == 0 {
		writeError(w, http.StatusBadGateway, "插件未声明任何可用内容源")
		return
	}
	candidates := make([]map[string]any, 0, len(sources))
	for _, parsed := range sources {
		if strings.TrimSpace(parsed.Name) == "" {
			continue
		}
		icon := strings.TrimSpace(selected.Icon)
		homepage := firstNonEmptyText(parsed.Homepage, selected.Homepage)
		if icon == "" && homepage != "" {
			icon = strings.TrimRight(homepage, "/") + "/favicon.ico"
		}
		sourceID := "ext-" + repository.Slug(pluginID+"-"+parsed.Name)
		config, _ := json.Marshal(pluginruntime.Config{
			Runtime:     runtimeType,
			PluginID:    pluginID,
			SourceName:  parsed.Name,
			ContentType: parsed.ContentType,
			Locale:      parsed.Locale,
			Homepage:    parsed.Homepage,
			InstallURL:  selected.InstallURL,
		})
		candidates = append(candidates, map[string]any{
			"id":          sourceID,
			"name":        firstNonEmptyText(parsed.Title, parsed.Name),
			"sourceName":  parsed.Name,
			"kind":        pluginruntime.KindForContentType(parsed.ContentType),
			"contentType": parsed.ContentType,
			"locale":      parsed.Locale,
			"description": firstNonEmptyText(selected.Description, pluginRuntimeLabel(runtimeType)+"来源"),
			"homepage":    homepage,
			"icon":        icon,
			"repoUrl":     repo.URL,
			"config":      json.RawMessage(config),
			"runtime":     runtimeType,
			"pluginId":    pluginID,
		})
	}
	payload, err := json.Marshal(map[string]any{
		"plugin":     pluginID,
		"candidates": candidates,
		"sources":    candidates,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.SaveExtensionCandidates(r.Context(), repo.ID, selected.ID, strings.TrimSpace(selected.Version), string(payload)); err != nil {
		s.logger.Printf("repository: cache extension candidates %s/%s: %v", repo.ID, selected.ID, err)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func repositoryView(item model.ExtensionRepository) model.ExtensionRepository {
	item.Error = item.LastError
	if item.LastError != "" {
		item.Status = "error"
	} else if item.LastSyncAt != nil || len(item.Catalog) > 0 {
		item.Status = "ok"
	} else {
		item.Status = "pending"
	}
	if len(item.Catalog) > 0 {
		_ = json.Unmarshal(item.Catalog, &item.Extensions)
		// 清单原文可能有几 MB（Keiyoushi 这类几百个扩展的仓库），前端只用
		// 解析后的 extensions，没必要把原始 JSON 再下发一份。
		item.Catalog = nil
	}
	return item
}

func (s *Server) handleListRepositories(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListExtensionRepositories(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for index := range items {
		items[index] = repositoryView(items[index])
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "kinds": repository.SupportedKinds()})
}

func (s *Server) handleCreateRepository(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		URL         string `json:"url"`
		Description string `json:"description"`
		Icon        string `json:"icon"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload.URL = strings.TrimSpace(payload.URL)
	if payload.URL == "" {
		writeError(w, http.StatusBadRequest, "仓库地址不能为空")
		return
	}
	customName := strings.TrimSpace(payload.Name)
	name := source.FirstNonEmpty(customName, "拓展仓库")
	idSeed := payload.URL
	if customName != "" {
		idSeed = customName + payload.URL
	}
	item := model.ExtensionRepository{
		ID:          newConfigID("repo", idSeed),
		Name:        name,
		CustomName:  customName,
		Kind:        repository.NormalizeKind(payload.Kind),
		URL:         payload.URL,
		Description: strings.TrimSpace(payload.Description),
		Icon:        strings.TrimSpace(payload.Icon),
		Enabled:     true,
		Catalog:     json.RawMessage(`[]`),
	}
	saved, err := s.store.UpsertExtensionRepository(r.Context(), item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	synced, err := s.syncRepository(r.Context(), saved)
	if err != nil {
		saved.LastError = err.Error()
		_, _ = s.store.UpsertExtensionRepository(r.Context(), saved)
	} else {
		saved = synced
	}
	_ = s.configs.PersistRepositories(r.Context(), s.store)
	writeJSON(w, http.StatusCreated, repositoryView(saved))
}

func (s *Server) handleUpdateRepository(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	existing, err := s.store.ExtensionRepository(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "拓展仓库不存在")
		return
	}
	var payload struct {
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		URL         string `json:"url"`
		Description string `json:"description"`
		Icon        string `json:"icon"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload.URL = strings.TrimSpace(payload.URL)
	if payload.URL == "" {
		writeError(w, http.StatusBadRequest, "仓库地址不能为空")
		return
	}
	existing.CustomName = strings.TrimSpace(payload.Name)
	existing.Name = source.FirstNonEmpty(existing.CustomName, "拓展仓库")
	existing.Kind = repository.NormalizeKind(payload.Kind)
	existing.URL = payload.URL
	existing.Description = strings.TrimSpace(payload.Description)
	existing.Icon = strings.TrimSpace(payload.Icon)
	saved, err := s.store.UpsertExtensionRepository(r.Context(), existing)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	synced, syncErr := s.syncRepository(r.Context(), saved)
	if syncErr != nil {
		saved.LastError = syncErr.Error()
		_, _ = s.store.UpsertExtensionRepository(r.Context(), saved)
	} else {
		saved = synced
	}
	_ = s.configs.PersistRepositories(r.Context(), s.store)
	writeJSON(w, http.StatusOK, repositoryView(saved))
}

func (s *Server) handleSyncRepository(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.ExtensionRepository(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "拓展仓库不存在")
		return
	}
	synced, err := s.syncRepository(r.Context(), item)
	if err != nil {
		item.LastError = err.Error()
		_, _ = s.store.UpsertExtensionRepository(r.Context(), item)
		_ = s.configs.PersistRepositories(r.Context(), s.store)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.configs.PersistRepositories(r.Context(), s.store)
	var items []model.RepositoryExtension
	_ = json.Unmarshal(synced.Catalog, &items)
	writeJSON(w, http.StatusOK, map[string]any{"repository": repositoryView(synced), "items": items})
}

func (s *Server) syncRepository(ctx context.Context, item model.ExtensionRepository) (model.ExtensionRepository, error) {
	metadata, err := repository.SyncWithMetadata(ctx, s.registry.Client(), item)
	if err != nil {
		return model.ExtensionRepository{}, err
	}
	items := metadata.Extensions
	if metadata.Name != "" && strings.TrimSpace(item.CustomName) == "" {
		item.Name = metadata.Name
	} else if strings.TrimSpace(item.CustomName) != "" {
		item.Name = strings.TrimSpace(item.CustomName)
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return model.ExtensionRepository{}, err
	}
	now := time.Now()
	item.Catalog = raw
	item.LastSyncAt = &now
	item.LastError = ""
	return s.store.UpsertExtensionRepository(ctx, item)
}

func (s *Server) handleDeleteRepository(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if err := s.store.DeleteExtensionRepository(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 仓库没了，它下面扩展的内容源缓存也没意义，顺手清掉。
	_ = s.store.DeleteExtensionCandidatesForRepository(r.Context(), id)
	_ = s.configs.PersistRepositories(r.Context(), s.store)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleImportRepositoryExtension(w http.ResponseWriter, r *http.Request) {
	repo, err := s.store.ExtensionRepository(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "拓展仓库不存在")
		return
	}
	var catalog []model.RepositoryExtension
	if err := json.Unmarshal(repo.Catalog, &catalog); err != nil {
		writeError(w, http.StatusBadRequest, "仓库清单尚未同步")
		return
	}
	var selected *model.RepositoryExtension
	for index := range catalog {
		if catalog[index].ID == r.PathValue("extensionId") {
			selected = &catalog[index]
			break
		}
	}
	if selected == nil {
		writeError(w, http.StatusNotFound, "仓库扩展不存在")
		return
	}
	if selected.Runtime == "plugin" && strings.TrimSpace(selected.InstallURL) != "" {
		s.importRepositoryPlugin(w, r, repo, *selected)
		return
	}
	if !selected.Installable || len(selected.Config) == 0 || strings.TrimSpace(selected.Homepage) == "" {
		writeError(w, http.StatusBadRequest, "该扩展没有可直接导入的配置")
		return
	}
	baseID := strings.TrimSpace(selected.PackageName)
	if baseID == "" {
		baseID = selected.Name
	}
	id := "ext-" + repository.Slug(baseID)
	item := model.CustomSource{
		ID:          id,
		Name:        selected.Name,
		Kind:        selected.Kind,
		Description: selected.Description,
		Homepage:    selected.Homepage,
		Icon:        selected.Icon,
		RepoURL:     repo.URL,
		Config:      selected.Config,
		Enabled:     true,
	}
	if _, err := generic.New(s.registry.Client(), item); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 添加内容源时就把网页图标落到本地 /data/icon，之后统一索引本地文件。
	item = s.localizeCustomSourceIcon(r.Context(), item)
	saved, err := s.store.UpsertCustomSource(r.Context(), item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.registry.ReloadCustomSources(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.configs.PersistSources(r.Context(), s.store)
	writeJSON(w, http.StatusOK, map[string]any{"source": saved})
}

// handlePrunePlugins unregisters extensions that no added source references.
// Reading a repository's candidate list requires installing its extensions, so
// without this the sidecar would keep loading every enumerated extension on
// every restart, which is what made the add-source dialog slow and heavy.
// handleInstallAPK 接收用户上传的 .apk 插件源，装进 Mihon 运行时并返回它声明
// 的内容源候选（与拓展仓库里的「加载内容源」同一套输出）。
func (s *Server) handleInstallAPK(w http.ResponseWriter, r *http.Request) {
	if s.runtime == nil {
		writeError(w, http.StatusBadRequest, "插件运行时未初始化")
		return
	}
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "读取上传文件失败："+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "缺少 apk 文件")
		return
	}
	defer file.Close()
	temp, err := os.CreateTemp("", "manco-upload-*.apk")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(temp, io.LimitReader(file, 256<<20)); err != nil {
		_ = temp.Close()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = temp.Close()
	base := strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))
	pluginID := "apk-" + repository.Slug(base)
	sources, err := s.runtime.InstallFile(r.Context(), pluginID, tempPath, pluginruntime.RuntimeMihon)
	if err != nil {
		writeError(w, http.StatusBadGateway, "安装 APK 失败："+err.Error())
		return
	}
	candidates := make([]map[string]any, 0, len(sources))
	for _, parsed := range sources {
		if strings.TrimSpace(parsed.Name) == "" {
			continue
		}
		config, _ := json.Marshal(pluginruntime.Config{
			Runtime:     pluginruntime.RuntimeMihon,
			PluginID:    pluginID,
			SourceName:  parsed.Name,
			ContentType: parsed.ContentType,
			Locale:      parsed.Locale,
			Homepage:    parsed.Homepage,
		})
		candidates = append(candidates, map[string]any{
			"id":          "ext-" + repository.Slug(pluginID+"-"+parsed.Name),
			"name":        firstNonEmptyText(parsed.Title, parsed.Name),
			"sourceName":  parsed.Name,
			"kind":        pluginruntime.KindForContentType(parsed.ContentType),
			"contentType": parsed.ContentType,
			"locale":      parsed.Locale,
			"homepage":    parsed.Homepage,
			"config":      json.RawMessage(config),
			"runtime":     pluginruntime.RuntimeMihon,
			"pluginId":    pluginID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugin": pluginID, "candidates": candidates, "sources": candidates})
}

// pendingPluginSources 返回插件运行时里目前声明、但还没被添加成内容源的那批
// 来源。探测式枚举的差集为空时用它兜底（扩展之前已经装过的情况）。
func (s *Server) pendingPluginSources(ctx context.Context) []pluginruntime.Source {
	if s.runtime == nil {
		return nil
	}
	all, err := s.runtime.Sources(ctx)
	if err != nil {
		s.logger.Printf("api: list runtime sources: %v", err)
		return nil
	}
	added := map[string]bool{}
	if items, err := s.store.ListCustomSources(ctx); err == nil {
		for _, item := range items {
			var config pluginruntime.Config
			if json.Unmarshal(item.Config, &config) == nil && strings.TrimSpace(config.SourceName) != "" {
				added[strings.ToLower(strings.TrimSpace(config.SourceName))] = true
			}
		}
	}
	result := make([]pluginruntime.Source, 0, len(all))
	for _, item := range all {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if name == "" || added[name] {
			continue
		}
		result = append(result, item)
	}
	return result
}

func (s *Server) handlePrunePlugins(w http.ResponseWriter, r *http.Request) {
	removed, err := s.PruneUnusedPlugins(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "清理插件失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

// PruneUnusedPlugins drops every installed extension that no added source
// references. It is also called once at startup so a runtime left behind by the
// old "enumerate every repository on open" behaviour is cleaned automatically —
// hundreds of registered plugins otherwise make the sidecar load them all on
// every restart and slow the whole app down.
func (s *Server) PruneUnusedPlugins(ctx context.Context) (int, error) {
	if s.runtime == nil {
		return 0, nil
	}
	items, err := s.store.ListCustomSources(ctx)
	if err != nil {
		return 0, err
	}
	keep := map[string]struct{}{}
	for _, item := range items {
		var config pluginruntime.Config
		if err := json.Unmarshal(item.Config, &config); err != nil {
			continue
		}
		if id := strings.TrimSpace(config.PluginID); id != "" {
			keep[id] = struct{}{}
		}
	}
	removed, err := s.runtime.PrunePlugins(keep)
	if err != nil {
		return 0, err
	}
	if removed > 0 {
		s.logger.Printf("plugins: pruned %d unused extension(s)", removed)
	}
	return removed, nil
}

// RestorePluginSources re-installs plugin extensions that are referenced by an
// added source but missing from the runtime — for example after a manual
// cleanup or a plugin file that failed to download. Sources added through the
// add-source dialog carry their install URL, so they can heal themselves
// instead of silently turning into broken entries after a restart.
func (s *Server) RestorePluginSources(ctx context.Context) (int, error) {
	if s.runtime == nil {
		return 0, nil
	}
	items, err := s.store.ListCustomSources(ctx)
	if err != nil {
		return 0, err
	}
	restored := 0
	seen := map[string]struct{}{}
	for _, item := range items {
		var config pluginruntime.Config
		if err := json.Unmarshal(item.Config, &config); err != nil {
			continue
		}
		if !isPluginRuntime(config.Runtime) {
			continue
		}
		pluginID := strings.TrimSpace(config.PluginID)
		installURL := strings.TrimSpace(config.InstallURL)
		if pluginID == "" || installURL == "" {
			continue
		}
		if _, ok := seen[pluginID]; ok {
			continue
		}
		seen[pluginID] = struct{}{}
		if s.runtime.HasPlugin(pluginID) {
			continue
		}
		if err := s.runtime.EnsureWithRuntime(ctx, pluginID, installURL, config.Runtime); err != nil {
			s.logger.Printf("plugins: restore %s: %v", pluginID, err)
			continue
		}
		s.logger.Printf("plugins: restored %s for %s", pluginID, item.Name)
		restored++
	}
	return restored, nil
}

func newConfigID(prefix, value string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	return prefix + "-" + hex.EncodeToString(sum[:6])
}

func (s *Server) handleCreateCustomSource(w http.ResponseWriter, r *http.Request) {
	s.saveCustomSource(w, r, "")
}

func (s *Server) handleUpdateCustomSource(w http.ResponseWriter, r *http.Request) {
	s.saveCustomSource(w, r, strings.ToLower(strings.TrimSpace(r.PathValue("id"))))
}

// handleRenameCustomSource 只改非内置来源的显示名称，不影响来源 ID、配置与
// 运行时路由；插件源加进来时通常带很长的站点名，允许用户改成自己的叫法。
func (s *Server) handleRenameCustomSource(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	item, err := s.store.CustomSource(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "自定义源不存在")
		return
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "名称不能为空")
		return
	}
	item.Name = name
	saved, err := s.store.UpsertCustomSource(r.Context(), item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.registry.ReloadCustomSources(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.configs.PersistSources(r.Context(), s.store)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "item": saved})
}

func (s *Server) handleGetCustomSource(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	item, err := s.store.CustomSource(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "自定义源不存在")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) saveCustomSource(w http.ResponseWriter, r *http.Request, pathID string) {
	var payload struct {
		ID          string          `json:"id"`
		Name        string          `json:"name"`
		Kind        string          `json:"kind"`
		Description string          `json:"description"`
		Homepage    string          `json:"homepage"`
		Icon        string          `json:"icon"`
		RepoURL     string          `json:"repoUrl"`
		Config      json.RawMessage `json:"config"`
		Enabled     *bool           `json:"enabled"`
		Hidden      *bool           `json:"hidden"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := strings.ToLower(strings.TrimSpace(source.FirstNonEmpty(pathID, payload.ID)))
	if s.registry.IsBuiltin(id) {
		writeError(w, http.StatusConflict, "默认资源源不可修改")
		return
	}
	if id == "" || strings.TrimSpace(payload.Name) == "" {
		writeError(w, http.StatusBadRequest, "源 ID 和名称不能为空")
		return
	}
	if len(payload.Config) == 0 {
		payload.Config = json.RawMessage(`{}`)
	}
	kind := source.KindOf(model.SourceInfo{Kind: payload.Kind})
	enabled := true
	hidden := false
	if existing, err := s.store.CustomSource(r.Context(), id); err == nil {
		enabled = existing.Enabled
		hidden = existing.Hidden
	}
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	if payload.Hidden != nil {
		hidden = *payload.Hidden
	}
	item := model.CustomSource{
		ID:          id,
		Name:        strings.TrimSpace(payload.Name),
		Kind:        kind,
		Description: strings.TrimSpace(payload.Description),
		Homepage:    strings.TrimRight(strings.TrimSpace(payload.Homepage), "/"),
		Icon:        strings.TrimSpace(payload.Icon),
		RepoURL:     strings.TrimSpace(payload.RepoURL),
		Config:      payload.Config,
		Enabled:     enabled,
		Hidden:      hidden,
	}
	var runtimeConfig pluginruntime.Config
	isPlugin := json.Unmarshal(item.Config, &runtimeConfig) == nil && isPluginRuntime(runtimeConfig.Runtime)
	if isPlugin {
		if strings.TrimSpace(runtimeConfig.PluginID) == "" || strings.TrimSpace(runtimeConfig.SourceName) == "" {
			writeError(w, http.StatusBadRequest, "插件来源缺少 pluginId 或 sourceName")
			return
		}
		// 枚举用的插件在批量加载结束后会被清理，添加来源时按需补装回来。
		if installURL := strings.TrimSpace(runtimeConfig.InstallURL); installURL != "" && s.runtime != nil {
			if err := s.runtime.EnsureWithRuntime(r.Context(), runtimeConfig.PluginID, installURL, runtimeConfig.Runtime); err != nil {
				writeError(w, http.StatusBadGateway, "安装插件失败："+err.Error())
				return
			}
		}
	} else {
		if strings.TrimSpace(payload.Homepage) == "" {
			writeError(w, http.StatusBadRequest, "普通来源的首页地址不能为空")
			return
		}
		if _, err := generic.New(s.registry.Client(), item); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	item = s.localizeCustomSourceIcon(r.Context(), item)
	saved, err := s.store.UpsertCustomSource(r.Context(), item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.registry.ReloadCustomSources(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	adapter, err := s.registry.Get(saved.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.configs.PersistSources(r.Context(), s.store)
	writeJSON(w, http.StatusOK, adapter.Info())
}

func (s *Server) handleDeleteCustomSource(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	if s.registry.IsBuiltin(id) {
		writeError(w, http.StatusConflict, "默认资源源不可删除")
		return
	}
	if _, err := s.store.CustomSource(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "自定义源不存在")
		return
	}
	if err := s.store.DeleteCustomSource(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.registry.ReloadCustomSources(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.configs.PersistSources(r.Context(), s.store)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUpdateSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.registry.Get(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var payload struct {
		Hidden             *bool              `json:"hidden"`
		DisabledCategories *[]string          `json:"disabledCategories"`
		Order              *[]string          `json:"order"`
		BrowseFilters      *map[string]string `json:"browseFilters"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if payload.Hidden == nil && payload.DisabledCategories == nil && payload.Order == nil && payload.BrowseFilters == nil {
		writeError(w, http.StatusBadRequest, "没有可更新的资源设置")
		return
	}
	hidden := s.hiddenSources(r.Context())
	if payload.Hidden != nil {
		if *payload.Hidden {
			hidden[id] = true
		} else {
			delete(hidden, id)
		}
		payloadJSON, err := json.Marshal(hidden)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.store.SetSetting(r.Context(), "hidden_sources", string(payloadJSON)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if payload.DisabledCategories != nil {
		adapter, err := s.registry.Get(id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		groups, err := sourceFilterGroups(ctx, adapter)
		if err != nil {
			writeError(w, http.StatusBadGateway, "加载来源筛选失败："+err.Error())
			return
		}
		normalized := normalizeDisabledCategories(groups, *payload.DisabledCategories)
		disabled := s.disabledCategories(r.Context())
		if len(normalized) == 0 {
			delete(disabled, id)
		} else {
			disabled[id] = normalized
		}
		raw, err := json.Marshal(disabled)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.store.SetSetting(r.Context(), "disabled_categories", string(raw)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if payload.Order != nil {
		adapter, err := s.registry.Get(id)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		kind := source.KindOf(adapter.Info())
		order := s.sourceOrder(r.Context())
		normalized := make([]string, 0, len(*payload.Order))
		seen := map[string]bool{}
		for _, value := range *payload.Order {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			normalized = append(normalized, value)
		}
		order[kind] = normalized
		raw, err := json.Marshal(order)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.store.SetSetting(r.Context(), "source_order", string(raw)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if payload.BrowseFilters != nil {
		filters := s.sourceBrowseFilters(r.Context())
		normalized := normalizeBrowseFilterValues(*payload.BrowseFilters)
		if len(normalized) == 0 {
			delete(filters, id)
		} else {
			filters[id] = normalized
		}
		raw, err := json.Marshal(filters)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.store.SetSetting(r.Context(), "source_browse_filters", string(raw)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"hidden":             hidden[id],
		"disabledCategories": s.disabledCategories(r.Context())[id],
		"browseFilters":      s.sourceBrowseFilters(r.Context())[id],
	})
	_ = s.configs.PersistSettings(r.Context(), s.store)
}

func (s *Server) handleSaveAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.registry.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var payload struct {
		Settings    *model.SourceDownloadSettings `json:"settings"`
		Username    string                        `json:"username"`
		Password    string                        `json:"password"`
		Token       string                        `json:"token"`
		Cookie      string                        `json:"cookie"`
		UserAgent   string                        `json:"userAgent"`
		HomeURL     *string                       `json:"homeUrl"`
		UseProxy    *bool                         `json:"useProxy"`
		ReloginDays *int                          `json:"reloginDays"`
		Login       bool                          `json:"login"`
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
	// User-Agent is always assigned so clearing the field falls back to the
	// built-in default instead of silently keeping the previous value.
	account.UserAgent = strings.TrimSpace(payload.UserAgent)
	if payload.HomeURL != nil {
		account.HomeURL = strings.TrimRight(strings.TrimSpace(*payload.HomeURL), "/")
	}
	if payload.UseProxy != nil {
		source.SetAccountUseProxy(&account, *payload.UseProxy)
	}
	if payload.ReloginDays != nil {
		days := *payload.ReloginDays
		if days < 0 {
			days = 0
		}
		source.SetAccountReloginDays(&account, days)
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
		for key, value := range result.Extra {
			if account.Extra == nil {
				account.Extra = map[string]any{}
			}
			account.Extra[key] = value
		}
		source.MarkAccountLoggedIn(&account, time.Now())
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
	userAgentCipher, err := s.box.Encrypt(account.UserAgent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if payload.Settings != nil {
		if account.Extra == nil {
			account.Extra = map[string]any{}
		}
		account.Extra["downloadSettings"] = *payload.Settings
	}
	extraJSON, err := json.Marshal(account.Extra)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.UpsertSourceAccount(r.Context(), model.SourceAccount{
		SourceID:        id,
		Username:        account.Username,
		SecretCipher:    secretCipher,
		TokenCipher:     tokenCipher,
		CookieCipher:    cookieCipher,
		UserAgentCipher: userAgentCipher,
		HomeURL:         account.HomeURL,
		Extra:           extraJSON,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.configs.PersistSources(r.Context(), s.store)
	s.engine.SetSourceSettings(s.allSourceDownloadSettings(r.Context()))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "connected": account.Token != "" || account.Cookie != ""})
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if err := s.registry.RemoveAccount(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.configs.PersistSources(r.Context(), s.store)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	item, account, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	convert := s.loadSettings(r.Context()).ConvertToSimplified
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "缺少搜索关键词")
		return
	}
	page := intParam(r, "page", 1)
	cacheKey := "search:" + r.URL.RequestURI()
	if raw, storedAt, ok := s.cache.Get(cacheKey); ok {
		var cached model.SearchResult
		if json.Unmarshal(raw, &cached) == nil {
			info := item.Info()
			kind := source.KindOf(info)
			for index := range cached.Items {
				cached.Items[index] = s.annotateDiscover(info.ID, kind, cached.Items[index])
			}
			writeJSON(w, http.StatusOK, convertDiscoverResult(cached, convert))
			return
		}
		writeCachedJSON(w, raw, storedAt)
		return
	}
	result, err := item.Search(r.Context(), account, query, page)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	info := item.Info()
	kind := source.KindOf(info)
	for index := range result.Items {
		result.Items[index] = s.annotateDiscover(info.ID, kind, result.Items[index])
	}
	s.cacheDiscoverItems(info, kind, result.Items)
	_ = s.cache.Set(cacheKey, result)
	writeJSON(w, http.StatusOK, convertDiscoverResult(result, convert))
	// 作者、简介和连载状态属于次要字段，不能让它们阻塞搜索结果首屏。
	// 后台补齐详情并回写搜索/发现缓存，下一次查询即可直接拿到完整信息。
	enrichedItems := append([]model.Comic(nil), result.Items...)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.enrichDiscoverItems(ctx, item, account, enrichedItems)
		for index := range enrichedItems {
			enrichedItems[index] = s.annotateDiscover(info.ID, kind, enrichedItems[index])
		}
		s.cacheDiscoverItems(info, kind, enrichedItems)
		enrichedResult := result
		enrichedResult.Items = enrichedItems
		_ = s.cache.Set(cacheKey, enrichedResult)
	}()
}

func (s *Server) handleGlobalSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "缺少搜索关键词")
		return
	}
	kindFilter := strings.TrimSpace(r.URL.Query().Get("kind"))
	page := intParam(r, "page", 1)
	convert := s.loadSettings(r.Context()).ConvertToSimplified
	sources := s.filteredSourceList(s.hiddenSources(r.Context()), nil)
	type searchItem struct {
		model.Comic
		SourceName string `json:"sourceName"`
		SourceIcon string `json:"sourceIcon,omitempty"`
	}
	result := make([]searchItem, 0)
	errorsFound := make([]string, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	for _, info := range sources {
		if info.Hidden || !info.CanSearch {
			continue
		}
		if kindFilter != "" && source.KindOf(info) != kindFilter {
			continue
		}
		adapter, err := s.registry.Get(info.ID)
		if err != nil {
			continue
		}
		account, err := s.registry.Account(ctx, info.ID)
		if err != nil {
			continue
		}
		wg.Add(1)
		go func(info model.SourceInfo, adapter source.Source, account source.Account) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			payload, err := adapter.Search(ctx, account, query, page)
			if err != nil {
				mu.Lock()
				if len(errorsFound) < 8 {
					errorsFound = append(errorsFound, info.Name+": "+err.Error())
				}
				mu.Unlock()
				return
			}
			s.cacheDiscoverItems(info, source.KindOf(info), payload.Items)
			mu.Lock()
			for _, item := range payload.Items {
				item.SourceID = info.ID
				item = s.annotateDiscover(info.ID, source.KindOf(info), item)
				result = append(result, searchItem{Comic: item, SourceName: info.Name, SourceIcon: info.Icon})
			}
			mu.Unlock()
		}(info, adapter, account)
	}
	wg.Wait()
	if len(result) == 0 && len(errorsFound) > 0 {
		writeError(w, http.StatusBadGateway, strings.Join(errorsFound, "；"))
		return
	}
	if convert {
		for index := range result {
			result[index].Comic = convertComicText(result[index].Comic)
			result[index].SourceName = downloader.SimplifiedText(result[index].SourceName)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result, "errors": errorsFound, "page": page})
}

func (s *Server) handleSourceFilters(w http.ResponseWriter, r *http.Request) {
	item, _, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	filters, err := sourceFilterGroups(ctx, item)
	if err != nil {
		writeError(w, http.StatusBadGateway, "加载来源筛选失败："+err.Error())
		return
	}
	filters = applyDisabledCategories(filters, s.disabledCategories(r.Context())[r.PathValue("id")])
	writeJSON(w, http.StatusOK, map[string]any{"items": filters})
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	item, account, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	convert := s.loadSettings(r.Context()).ConvertToSimplified
	options := model.BrowseOptions{
		Category: strings.TrimSpace(r.URL.Query().Get("category")),
		Sort:     strings.TrimSpace(r.URL.Query().Get("sort")),
		State:    strings.TrimSpace(r.URL.Query().Get("state")),
		Region:   strings.TrimSpace(r.URL.Query().Get("region")),
	}
	for key, values := range r.URL.Query() {
		if len(values) == 0 {
			continue
		}
		value := strings.TrimSpace(values[0])
		if value == "" || isBrowseControlParam(key) {
			continue
		}
		if options.Extra == nil {
			options.Extra = map[string]string{}
		}
		options.Extra[key] = value
	}
	if options.Sort == "" {
		options.Sort = strings.TrimSpace(r.URL.Query().Get("order"))
	}
	if options.Category == "" {
		options.Category = strings.TrimSpace(r.URL.Query().Get("kind"))
	}
	page := intParam(r, "page", 1)
	cacheKey := "browse:" + r.URL.RequestURI()
	if raw, storedAt, ok := s.cache.Get(cacheKey); ok {
		var cached model.SearchResult
		if json.Unmarshal(raw, &cached) == nil {
			info := item.Info()
			kind := source.KindOf(info)
			for index := range cached.Items {
				cached.Items[index] = s.annotateDiscover(info.ID, kind, cached.Items[index])
			}
			writeJSON(w, http.StatusOK, convertDiscoverResult(cached, convert))
			return
		}
		writeCachedJSON(w, raw, storedAt)
		return
	}
	result, err := item.Browse(r.Context(), account, options, page)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	info := item.Info()
	kind := source.KindOf(info)
	// 列表常常缺作者/简介/连载状态，这里按页补一次详情。
	s.enrichDiscoverItems(r.Context(), item, account, result.Items)
	s.cacheDiscoverItems(info, kind, result.Items)
	for index := range result.Items {
		result.Items[index] = s.annotateDiscover(info.ID, kind, result.Items[index])
	}
	_ = s.cache.Set(cacheKey, result)
	writeJSON(w, http.StatusOK, convertDiscoverResult(result, convert))
}

// enrichDiscoverItems 给浏览/搜索结果补 author/description/status：很多内容源的
// 列表接口不带这些字段，只有详情接口才有。只补当前页、最多 12 条、并发 3，
// 避免首屏被回填拖慢；补好的结果会随 result 一起写进发现缓存。
func (s *Server) enrichDiscoverItems(ctx context.Context, item source.Source, account source.Account, items []model.Comic) {
	missing := make([]int, 0, discoverEnrichLimit)
	for index := range items {
		if strings.TrimSpace(items[index].Cover) != "" &&
			strings.TrimSpace(items[index].Author) != "" &&
			strings.TrimSpace(items[index].Description) != "" &&
			strings.TrimSpace(items[index].Status) != "" {
			continue
		}
		missing = append(missing, index)
		if len(missing) >= discoverEnrichLimit {
			break
		}
	}
	if len(missing) == 0 {
		return
	}
	sem := make(chan struct{}, discoverEnrichConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, index := range missing {
		wg.Add(1)
		sem <- struct{}{}
		go func(index int) {
			defer wg.Done()
			defer func() { <-sem }()
			detail, err := s.loadComicDetail(ctx, item, account, items[index].ID)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if strings.TrimSpace(items[index].Cover) == "" {
				items[index].Cover = detail.Comic.Cover
			}
			if strings.TrimSpace(items[index].Author) == "" {
				items[index].Author = detail.Comic.Author
			}
			if strings.TrimSpace(items[index].Description) == "" {
				items[index].Description = detail.Comic.Description
			}
			if strings.TrimSpace(items[index].Status) == "" {
				items[index].Status = detail.Comic.Status
			}
		}(index)
	}
	wg.Wait()
}

func isBrowseControlParam(key string) bool {
	switch strings.TrimSpace(key) {
	case "page", "category", "sort", "state", "region", "order", "kind", "q":
		return true
	default:
		return false
	}
}

func (s *Server) handleComic(w http.ResponseWriter, r *http.Request) {
	item, account, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	sourceID := item.Info().ID
	kind := source.KindOf(item.Info())
	comicID := r.PathValue("comicId")
	if s.discover != nil {
		if cached, ok := s.discover.Get(sourceID, kind, comicID); ok {
			refreshNeeded := len(cached.Chapters) == 0 || strings.TrimSpace(cached.Comic.Description) == ""
			if refreshNeeded {
				ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
				fresh, freshErr := s.loadComicDetail(ctx, item, account, cached.Comic.ID)
				cancel()
				if freshErr == nil {
					fresh.Comic.SourceID = source.FirstNonEmpty(fresh.Comic.SourceID, sourceID)
					cached = fresh
					go func() {
						ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
						defer cancel()
						if err := s.putDiscoverDetail(ctx, sourceID, item.Info().Name, kind, cached); err != nil {
							s.logger.Printf("api: cache discover detail %s/%s: %v", sourceID, cached.Comic.ID, err)
						}
					}()
				} else {
					s.logger.Printf("api: load discover detail %s/%s: %v", sourceID, cached.Comic.ID, freshErr)
					if len(cached.Chapters) == 0 {
						writeError(w, http.StatusBadGateway, "加载章节失败："+freshErr.Error())
						return
					}
				}
			}
			applyLatestChapterUpdatedAt(&cached)
			writeJSON(w, http.StatusOK, convertComicDetail(cached, s.loadSettings(r.Context()).ConvertToSimplified))
			if !refreshNeeded {
				go s.refreshDiscoverDetail(sourceID, kind, comicID)
			}
			return
		}
	}
	detail, err := s.loadComicDetail(r.Context(), item, account, comicID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if s.discover != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := s.putDiscoverDetail(ctx, sourceID, item.Info().Name, kind, detail); err != nil {
				s.logger.Printf("api: cache discover %s/%s: %v", sourceID, detail.Comic.ID, err)
			}
		}()
	}
	writeJSON(w, http.StatusOK, convertComicDetail(detail, s.loadSettings(r.Context()).ConvertToSimplified))
}

func (s *Server) loadComicDetail(ctx context.Context, item source.Source, account source.Account, comicID string) (model.ComicDetail, error) {
	var detail model.ComicDetail
	if combined, ok := item.(source.DetailWithChaptersSource); ok {
		loaded, err := combined.DetailWithChapters(ctx, account, comicID)
		if err != nil {
			return model.ComicDetail{}, err
		}
		detail = loaded
	} else {
		comic, err := item.Detail(ctx, account, comicID)
		if err != nil {
			return model.ComicDetail{}, err
		}
		chapters, err := item.Chapters(ctx, account, comic.ID)
		if err != nil {
			return model.ComicDetail{}, err
		}
		detail = model.ComicDetail{Comic: comic, Chapters: chapters}
	}
	if len(detail.Chapters) > 0 {
		downloader.SortChapters(detail.Chapters)
		if strings.TrimSpace(detail.Comic.LatestChapterTitle) == "" {
			last := detail.Chapters[len(detail.Chapters)-1]
			detail.Comic.LatestChapterTitle = source.FirstNonEmpty(last.Title, last.ID)
		}
		applyLatestChapterUpdatedAt(&detail)
	}
	return detail, nil
}

func applyLatestChapterUpdatedAt(detail *model.ComicDetail) {
	if detail == nil {
		return
	}
	if updatedAt := source.NormalizeTimestamp(detail.Comic.UpdatedAt); updatedAt != "" {
		detail.Comic.UpdatedAt = updatedAt
		return
	}
	detail.Comic.UpdatedAt = ""
	for index := len(detail.Chapters) - 1; index >= 0; index-- {
		if updatedAt := source.NormalizeTimestamp(detail.Chapters[index].UpdatedAt); updatedAt != "" {
			detail.Comic.UpdatedAt = updatedAt
			return
		}
	}
}

func (s *Server) handleChapters(w http.ResponseWriter, r *http.Request) {
	item, account, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	convert := s.loadSettings(r.Context()).ConvertToSimplified
	detail, err := s.loadComicDetail(r.Context(), item, account, r.PathValue("comicId"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	chapters := detail.Chapters
	if convert {
		for index := range chapters {
			chapters[index].Title = downloader.SimplifiedText(chapters[index].Title)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": chapters})
}

type cachedChapterPreview struct {
	SourceID  string        `json:"sourceId"`
	ComicID   string        `json:"comicId"`
	Chapter   model.Chapter `json:"chapter"`
	Pages     []model.Page  `json:"pages"`
	CreatedAt time.Time     `json:"createdAt"`
}

func (s *Server) handleChapterPreview(w http.ResponseWriter, r *http.Request) {
	item, account, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	var payload struct {
		Chapter model.Chapter `json:"chapter"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(payload.Chapter.ID) == "" {
		writeError(w, http.StatusBadRequest, "缺少章节")
		return
	}
	comicID := r.PathValue("comicId")
	title := source.FirstNonEmpty(payload.Chapter.Title, payload.Chapter.ID)
	if contentSource, ok := item.(source.ContentSource); ok && source.KindOf(item.Info()) == source.KindBook {
		text, err := contentSource.ChapterContent(r.Context(), account, comicID, payload.Chapter)
		if err != nil {
			writeError(w, http.StatusBadGateway, "加载章节正文失败："+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"kind":    "text",
			"title":   title,
			"content": text,
		})
		return
	}
	pages, err := item.Pages(r.Context(), account, comicID, payload.Chapter)
	if err != nil {
		writeError(w, http.StatusBadGateway, "加载章节页面失败："+err.Error())
		return
	}
	if len(pages) == 0 {
		writeError(w, http.StatusBadGateway, "该章节没有可预览的页面")
		return
	}
	random := make([]byte, 18)
	if _, err := rand.Read(random); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	token := hex.EncodeToString(random)
	preview := cachedChapterPreview{
		SourceID:  item.Info().ID,
		ComicID:   comicID,
		Chapter:   payload.Chapter,
		Pages:     pages,
		CreatedAt: time.Now(),
	}
	if err := s.cache.Set("chapter-preview:"+token, preview); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	urls := make([]string, 0, len(pages))
	for index := range pages {
		urls = append(urls, fmt.Sprintf(
			"/api/sources/%s/comics/%s/preview/image?key=%s&index=%d",
			url.PathEscape(item.Info().ID),
			url.PathEscape(comicID),
			token,
			index,
		))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":  "images",
		"title": title,
		"pages": urls,
	})
}

func (s *Server) handleChapterPreviewImage(w http.ResponseWriter, r *http.Request) {
	item, account, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("key"))
	index := intParam(r, "index", -1)
	if token == "" || index < 0 {
		writeError(w, http.StatusBadRequest, "无效的预览页面")
		return
	}
	raw, _, ok := s.cache.Get("chapter-preview:" + token)
	if !ok {
		writeError(w, http.StatusGone, "预览已过期，请重新打开章节")
		return
	}
	var preview cachedChapterPreview
	if json.Unmarshal(raw, &preview) != nil ||
		preview.SourceID != item.Info().ID ||
		preview.ComicID != r.PathValue("comicId") ||
		index >= len(preview.Pages) {
		writeError(w, http.StatusGone, "预览已过期，请重新打开章节")
		return
	}
	client := s.registry.ClientForAccount(account)
	if client == nil {
		client = source.NewHTTPClient()
	}
	data, contentType, err := downloader.PreviewPageImage(
		r.Context(),
		client,
		account,
		item,
		preview.ComicID,
		preview.Chapter,
		preview.Pages[index],
	)
	if err != nil {
		writeError(w, http.StatusBadGateway, "加载预览页面失败："+err.Error())
		return
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = http.DetectContentType(data)
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// handleComicDownloads 列出某作品已经下载完成的章节 ID，供归档集查看“哪些章节下过”。
func (s *Server) handleComicDownloads(w http.ResponseWriter, r *http.Request) {
	item, _, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	sourceID := item.Info().ID
	comicID := r.PathValue("comicId")
	jobs, err := s.store.CompletedDownloadJobsByComic(r.Context(), sourceID, comicID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	chapterIDs := make([]string, 0, len(jobs))
	seen := make(map[string]struct{}, len(jobs))
	for _, job := range jobs {
		id := strings.TrimSpace(job.ChapterID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		chapterIDs = append(chapterIDs, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"chapterIds": chapterIDs, "total": len(chapterIDs)})
}

func convertDiscoverResult(result model.SearchResult, convert bool) model.SearchResult {
	if !convert {
		return result
	}
	for index := range result.Items {
		result.Items[index] = convertComicText(result.Items[index])
	}
	return result
}

func convertComicDetail(detail model.ComicDetail, convert bool) model.ComicDetail {
	if !convert {
		return detail
	}
	detail.Comic = convertComicText(detail.Comic)
	for index := range detail.Chapters {
		detail.Chapters[index].Title = downloader.SimplifiedText(detail.Chapters[index].Title)
	}
	return detail
}

func convertComicText(comic model.Comic) model.Comic {
	comic.Title = downloader.SimplifiedText(comic.Title)
	comic.Author = downloader.SimplifiedText(comic.Author)
	comic.Description = downloader.SimplifiedText(comic.Description)
	comic.Status = downloader.SimplifiedText(comic.Status)
	comic.LatestChapterTitle = downloader.SimplifiedText(comic.LatestChapterTitle)
	for index := range comic.Tags {
		comic.Tags[index] = downloader.SimplifiedText(comic.Tags[index])
	}
	return comic
}

func (s *Server) cacheDiscoverItems(info model.SourceInfo, kind string, items []model.Comic) {
	if s.discover == nil || len(items) == 0 {
		return
	}
	task := discoverCacheTask{
		sourceID:   info.ID,
		sourceName: info.Name,
		kind:       kind,
		items:      append([]model.Comic(nil), items...),
	}
	s.discoverQueueOnce.Do(func() {
		s.discoverQueue = make(chan discoverCacheTask, 8)
		go s.runDiscoverCacheQueue()
	})
	select {
	case s.discoverQueue <- task:
	default:
		// A slow source should not build an unbounded background queue.
	}
}

func (s *Server) runDiscoverCacheQueue() {
	for task := range s.discoverQueue {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		for _, comic := range task.items {
			if strings.TrimSpace(comic.ID) == "" {
				continue
			}
			if err := s.putDiscoverCard(ctx, task.sourceID, task.sourceName, task.kind, comic); err != nil {
				s.logger.Printf("api: cache discover browse %s/%s: %v", task.sourceID, comic.ID, err)
			}
			if ctx.Err() != nil {
				break
			}
		}
		cancel()
	}
}

// putDiscoverDetail stores discover metadata and lets image-capable sources
// materialize covers through their own runtime before falling back to HTTP.
func (s *Server) putDiscoverDetail(ctx context.Context, sourceID, sourceName, kind string, detail model.ComicDetail) error {
	if s.discover == nil {
		return nil
	}
	var materialize discovercache.CoverMaterializer
	if s.registry != nil {
		if adapter, err := s.registry.Get(sourceID); err == nil {
			if imageSource, ok := adapter.(source.ImageSource); ok {
				account, accountErr := s.registry.Account(ctx, sourceID)
				if accountErr == nil {
					materialize = func(ctx context.Context, comic model.Comic) ([]byte, string, error) {
						select {
						case pluginCoverFetchSlots <- struct{}{}:
							defer func() { <-pluginCoverFetchSlots }()
						case <-ctx.Done():
							return nil, "", ctx.Err()
						}
						return imageSource.CoverImage(ctx, account, comic)
					}
				}
			}
		}
	}
	return s.discover.PutNamedWithCover(ctx, s.registry.Client(), sourceID, sourceName, kind, detail, materialize)
}

func (s *Server) putDiscoverCard(ctx context.Context, sourceID, sourceName, kind string, comic model.Comic) error {
	if s.discover == nil {
		return nil
	}
	// Browse/search cards stay lightweight: persist the card metadata for
	// long-lived reuse, but leave cover materialization and ComicInfo sidecars
	// to detail visits. The browser can still render the remote cover through
	// the existing image proxy in the meantime.
	return s.discover.PutNamedCard(ctx, s.registry.Client(), sourceID, sourceName, kind, model.ComicDetail{Comic: comic})
}

func (s *Server) annotateDiscover(sourceID, kind string, comic model.Comic) model.Comic {

	comic.SourceID = source.FirstNonEmpty(comic.SourceID, sourceID)
	if s.discover == nil {
		return comic
	}
	return s.discover.AnnotateKind(kind, comic)
}

func (s *Server) refreshDiscoverDetail(sourceID, kind, comicID string) {
	if s.discover == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	adapter, err := s.registry.Get(sourceID)
	if err != nil {
		return
	}
	account, err := s.registry.Account(ctx, sourceID)
	if err != nil {
		return
	}
	detail, err := s.loadComicDetail(ctx, adapter, account, comicID)
	if err != nil {
		return
	}
	if err := s.putDiscoverDetail(ctx, sourceID, adapter.Info().Name, kind, detail); err != nil {
		s.logger.Printf("api: refresh discover %s/%s: %v", sourceID, comicID, err)
	}
}

func (s *Server) handleDiscoverCover(w http.ResponseWriter, r *http.Request) {
	if s.discover == nil {
		http.NotFound(w, r)
		return
	}
	sourceID := strings.TrimSpace(r.URL.Query().Get("sourceId"))
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	comicID := strings.TrimSpace(r.URL.Query().Get("comicId"))
	if sourceID == "" || comicID == "" || !s.discover.ServeCover(w, r, sourceID, kind, comicID) {
		http.NotFound(w, r)
	}
}

func (s *Server) handleSubscriptionCover(w http.ResponseWriter, r *http.Request) {
	sourceID := strings.TrimSpace(r.URL.Query().Get("sourceId"))
	comicID := strings.TrimSpace(r.URL.Query().Get("comicId"))
	if sourceID == "" || comicID == "" || s.subscriptionCovers == nil {
		http.NotFound(w, r)
		return
	}
	file, err := s.subscriptionCovers.OpenCover(sourceID, comicID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if contentType := mime.TypeByExtension(filepath.Ext(info.Name())); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func (s *Server) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	// 下载结束后自动移除临时卡片（文件保留），顺带保证列表里的状态是干净的。
	s.pruneTemporarySubscriptions(r.Context())
	filter := s.subscriptionFilterFromRequest(r)
	limit := intParam(r, "limit", 0)
	offset := intParam(r, "offset", 0)
	var (
		items []model.Subscription
		total int
		err   error
	)
	if limit > 0 {
		items, total, err = s.store.ListSubscriptionsPage(r.Context(), filter, limit, offset)
	} else {
		items, err = s.store.ListSubscriptions(r.Context())
		total = len(items)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []model.Subscription{}
	}
	for index := range items {
		items[index] = s.subscriptionWithCovers(r.Context(), items[index])
	}
	s.flushSubscriptionCoverIndex()
	stats, err := s.store.DownloadJobStatsForSubscriptions(r.Context(), items)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	kindCounts := s.subscriptionKindCounts(r.Context())
	downloadDirs, err := s.store.SubscriptionDownloadDirs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	activePending, err := s.store.ActiveDownloadJobCount(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":         items,
		"total":         total,
		"jobStats":      stats,
		"kindCounts":    kindCounts,
		"downloadDirs":  downloadDirs,
		"activePending": activePending,
	})
}

// pruneTemporarySubscriptions 移除「临时下载卡片」：只有仍有进行中或失败任务
// 时保留卡片；完成、取消且无其他任务时移除，已经下载到本地的文件保持不动。
func (s *Server) pruneTemporarySubscriptions(ctx context.Context) {
	items, err := s.store.ListTemporarySubscriptions(ctx)
	if err != nil {
		return
	}
	for _, item := range items {
		jobs, err := s.store.DownloadJobsByComic(ctx, item.SourceID, item.ComicID)
		if err != nil {
			continue
		}
		retain := false
		for _, job := range jobs {
			switch job.Status {
			case "queued", "running", "waiting", "paused", "failed":
				retain = true
			}
			if retain {
				break
			}
		}
		if retain {
			continue
		}
		if err := s.store.DeleteSubscription(ctx, item.ID); err != nil {
			s.logger.Printf("api: prune temporary subscription %d: %v", item.ID, err)
		}
	}
}

func (s *Server) subscriptionFilterFromRequest(r *http.Request) store.SubscriptionFilter {
	query := r.URL.Query()
	filter := store.SubscriptionFilter{
		DownloadDir: strings.TrimSpace(query.Get("dir")),
		Sort:        strings.ToLower(strings.TrimSpace(query.Get("sort"))),
		Descending:  true,
	}
	if raw := strings.TrimSpace(query.Get("desc")); raw != "" {
		filter.Descending = raw != "0" && !strings.EqualFold(raw, "false")
	}
	kind := strings.ToLower(strings.TrimSpace(query.Get("kind")))
	if kind == "" || kind == "all" || s.registry == nil {
		return filter
	}
	var bookSources, otherSources []string
	for _, info := range s.registry.List() {
		switch source.KindOf(info) {
		case source.KindBook:
			bookSources = append(bookSources, info.ID)
		case source.KindOther:
			otherSources = append(otherSources, info.ID)
		}
	}
	switch kind {
	case source.KindBook:
		filter.SourceIDs = bookSources
		filter.MatchNone = len(bookSources) == 0
	case source.KindOther:
		filter.SourceIDs = otherSources
		filter.MatchNone = len(otherSources) == 0
	case source.KindComic:
		filter.ExcludeSourceIDs = append(filter.ExcludeSourceIDs, bookSources...)
		filter.ExcludeSourceIDs = append(filter.ExcludeSourceIDs, otherSources...)
	}
	return filter
}

func (s *Server) subscriptionKindCounts(ctx context.Context) map[string]int {
	counts := map[string]int{"all": 0, source.KindComic: 0, source.KindBook: 0, source.KindOther: 0}
	kindBySource := map[string]string{}
	if s.registry != nil {
		for _, info := range s.registry.List() {
			kindBySource[info.ID] = source.KindOf(info)
		}
	}
	sourceCounts, err := s.store.SubscriptionSourceCounts(ctx)
	if err != nil {
		return counts
	}
	for sourceID, count := range sourceCounts {
		kind := kindBySource[sourceID]
		if kind == "" {
			kind = source.KindComic
		}
		counts["all"] += count
		counts[kind] += count
	}
	return counts
}

// cancelUnfinishedJobs 取消某部作品正在下载/排队的任务并删掉这些任务记录；
// 已下载文件与已完成记录都不动。
func (s *Server) cancelUnfinishedJobs(ctx context.Context, sub model.Subscription) {
	jobs, err := s.store.UnfinishedDownloadJobsByComic(ctx, sub.SourceID, sub.ComicID)
	if err != nil {
		s.logger.Printf("api: list unfinished jobs for %s/%s: %v", sub.SourceID, sub.ComicID, err)
		return
	}
	for _, job := range jobs {
		s.engine.Cancel(job.ID)
		if err := s.store.DeleteDownloadJob(ctx, job.ID); err != nil {
			s.logger.Printf("api: delete unfinished job %d: %v", job.ID, err)
		}
	}
}

// handleListArchivedSubscriptions 返回归档集里的订阅卡片，供订阅清单的“归档集”窗口展示。
func (s *Server) handleListArchivedSubscriptions(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListArchivedSubscriptions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []model.Subscription{}
	}
	for index := range items {
		items[index] = s.subscriptionWithCovers(r.Context(), items[index])
	}
	s.flushSubscriptionCoverIndex()
	stats, err := s.store.DownloadJobStatsForSubscriptions(r.Context(), items)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jobStats := make(map[string]model.DownloadJobStats, len(stats))
	for key, value := range stats {
		jobStats[key] = value
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "jobStats": jobStats})
}

func (s *Server) handleMatchSubscription(w http.ResponseWriter, r *http.Request) {
	sourceID := strings.TrimSpace(r.URL.Query().Get("sourceId"))
	comicID := strings.TrimSpace(r.URL.Query().Get("comicId"))
	if sourceID == "" || comicID == "" {
		writeError(w, http.StatusBadRequest, "缺少 sourceId 或 comicId")
		return
	}
	latestDownloadDir, err := s.store.LatestSubscriptionDownloadDir(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	item, err := s.store.SubscriptionByComic(r.Context(), sourceID, comicID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"item": nil, "latestDownloadDir": latestDownloadDir})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": item, "latestDownloadDir": latestDownloadDir})
}

func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		SourceID            string  `json:"sourceId"`
		ComicID             string  `json:"comicId"`
		Title               string  `json:"title"`
		Cover               string  `json:"cover"`
		Author              string  `json:"author"`
		SeriesDir           string  `json:"seriesDir"`
		AutoDownload        *bool   `json:"autoDownload"`
		Enabled             *bool   `json:"enabled"`
		CronExpr            *string `json:"cronExpr"`
		DownloadDir         string  `json:"downloadDir"`
		ConvertToSimplified *bool   `json:"convertToSimplified"`
		ChapterMarker       *bool   `json:"chapterMarker"`
		CoverBlur           *bool   `json:"coverBlur"`
		Baseline            string  `json:"baseline"`
		AllChapters         bool    `json:"allChapters"`
		LastOrder           float64 `json:"lastChapterOrder"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(payload.SourceID) == "" || strings.TrimSpace(payload.ComicID) == "" {
		writeError(w, http.StatusBadRequest, "缺少漫画源或作品 ID")
		return
	}
	// 显式传空串表示关掉这个订阅的定时检查；字段缺省才补默认周期，兼容老客户端。
	cronExpr := cronutil.Default(time.Now())
	if payload.CronExpr != nil {
		cronExpr = strings.TrimSpace(*payload.CronExpr)
	}
	if cronExpr != "" {
		if err := cronutil.Validate(cronExpr); err != nil {
			writeError(w, http.StatusBadRequest, "Cron 表达式无效: "+err.Error())
			return
		}
	}
	downloadDir, err := s.cleanDownloadDir(payload.DownloadDir)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	convertToSimplified := s.loadSettings(r.Context()).ConvertToSimplified
	chapterMarker := payload.ChapterMarker != nil && *payload.ChapterMarker
	coverBlur := payload.CoverBlur != nil && *payload.CoverBlur
	if payload.ConvertToSimplified != nil {
		convertToSimplified = *payload.ConvertToSimplified
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
	title := source.FirstNonEmpty(payload.Title, payload.ComicID)
	seriesDir := subscriptionSeriesDirName(payload.SeriesDir, title, convertToSimplified)
	subscription := model.Subscription{
		SourceID:            payload.SourceID,
		ComicID:             payload.ComicID,
		Title:               title,
		SeriesDir:           seriesDir,
		Cover:               payload.Cover,
		Author:              payload.Author,
		Enabled:             payload.Enabled == nil || *payload.Enabled,
		AutoDownload:        payload.AutoDownload == nil || *payload.AutoDownload,
		CronExpr:            cronExpr,
		DownloadDir:         downloadDir,
		ConvertToSimplified: convertToSimplified,
		ChapterMarker:       chapterMarker,
		CoverBlur:           coverBlur,
		LastChapterID:       strings.TrimSpace(payload.Baseline),
		LastChapterOrder:    payload.LastOrder,
		LastChapterTitle:    "",
	}
	saved, err := s.store.UpsertSubscription(r.Context(), subscription)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.configs.PersistSubscriptions(r.Context(), s.store)
	response := map[string]any{"item": s.subscriptionWithCovers(r.Context(), saved)}
	s.flushSubscriptionCoverIndex()
	if payload.AllChapters {
		s.registerInitialDownloadNotice(r.Context(), saved)
		queued, skipped, queueErr := s.scheduler.QueueAll(r.Context(), saved)
		if queueErr != nil {
			s.logger.Printf("api: queue all subscription %d: %v", saved.ID, queueErr)
			response["queueError"] = queueErr.Error()
			if pending, _ := s.store.InitialDownloadBatchPending(r.Context(), saved.SourceID, saved.ComicID); !pending {
				s.unregisterInitialDownloadNotice(r.Context(), saved.SourceID, saved.ComicID)
			}
		} else {
			response["queued"] = queued
			response["skipped"] = skipped
			if queued == 0 {
				s.unregisterInitialDownloadNotice(r.Context(), saved.SourceID, saved.ComicID)
			}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

type initialDownloadNoticeState struct {
	SubscriptionID int64     `json:"subscriptionId"`
	SourceID       string    `json:"sourceId"`
	ComicID        string    `json:"comicId"`
	StartedAt      time.Time `json:"startedAt"`
}

func initialDownloadNoticeKey(sourceID, comicID string) string {
	return sourceID + "\x00" + comicID
}

func (s *Server) initialDownloadNotices(ctx context.Context) map[string]initialDownloadNoticeState {
	result := map[string]initialDownloadNoticeState{}
	raw, err := s.store.Setting(ctx, "initial_download_notices")
	if err == nil && strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &result)
	}
	return result
}

func (s *Server) saveInitialDownloadNotices(ctx context.Context, notices map[string]initialDownloadNoticeState) error {
	raw, err := json.Marshal(notices)
	if err != nil {
		return err
	}
	return s.store.SetSetting(ctx, "initial_download_notices", string(raw))
}

func (s *Server) registerInitialDownloadNotice(ctx context.Context, subscription model.Subscription) {
	s.initialDownloadMu.Lock()
	defer s.initialDownloadMu.Unlock()
	notices := s.initialDownloadNotices(ctx)
	notices[initialDownloadNoticeKey(subscription.SourceID, subscription.ComicID)] = initialDownloadNoticeState{
		SubscriptionID: subscription.ID,
		SourceID:       subscription.SourceID,
		ComicID:        subscription.ComicID,
		StartedAt:      time.Now(),
	}
	if err := s.saveInitialDownloadNotices(ctx, notices); err != nil {
		s.logger.Printf("api: register initial download notice %s/%s: %v", subscription.SourceID, subscription.ComicID, err)
	}
}

func (s *Server) unregisterInitialDownloadNotice(ctx context.Context, sourceID, comicID string) {
	s.initialDownloadMu.Lock()
	defer s.initialDownloadMu.Unlock()
	notices := s.initialDownloadNotices(ctx)
	delete(notices, initialDownloadNoticeKey(sourceID, comicID))
	if err := s.saveInitialDownloadNotices(ctx, notices); err != nil {
		s.logger.Printf("api: unregister initial download notice %s/%s: %v", sourceID, comicID, err)
	}
}

// RunInitialDownloadNotifications checks historical download batches on a
// low-frequency timer so a restart cannot lose a completion notification.
func (s *Server) RunInitialDownloadNotifications(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	s.processInitialDownloadNotices(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processInitialDownloadNotices(ctx)
		}
	}
}

func (s *Server) processInitialDownloadNotices(ctx context.Context) {
	if s.telegram == nil || !s.telegram.NotificationReady() {
		return
	}
	s.initialDownloadMu.Lock()
	defer s.initialDownloadMu.Unlock()
	notices := s.initialDownloadNotices(ctx)
	if len(notices) == 0 {
		return
	}
	changed := false
	now := time.Now()
	for key, state := range notices {
		subscription, err := s.store.Subscription(ctx, state.SubscriptionID)
		if errors.Is(err, store.ErrNotFound) {
			delete(notices, key)
			changed = true
			continue
		}
		if err != nil {
			continue
		}
		pending, err := s.store.InitialDownloadBatchPending(ctx, state.SourceID, state.ComicID)
		if err != nil || pending {
			continue
		}
		jobs, err := s.store.DownloadJobsByComic(ctx, state.SourceID, state.ComicID)
		if err != nil {
			continue
		}
		succeeded := 0
		failed := 0
		for _, job := range jobs {
			finished := job.UpdatedAt
			if job.FinishedAt != nil {
				finished = *job.FinishedAt
			}
			if finished.Before(state.StartedAt) {
				continue
			}
			switch job.Status {
			case "completed":
				succeeded++
			case "failed", "canceled":
				failed++
			}
		}
		item := s.subscriptionWithCovers(ctx, subscription)
		author, description := s.subscriptionLocalMetadata(item)
		if strings.TrimSpace(item.Author) == "" {
			item.Author = author
		}
		notice := telegram.SubscriptionDownloadCompletedNotice{
			Title:          item.Title,
			Author:         item.Author,
			Description:    description,
			CoverURL:       item.Cover,
			CoverFile:      s.localCoverFileForSubscription(item),
			Succeeded:      succeeded,
			Failed:         failed,
			NextCheckLabel: nextSubscriptionCheckLabel(item.CronExpr, now),
		}
		s.telegram.SubscriptionDownloadCompleted(ctx, notice)
		delete(notices, key)
		changed = true
	}
	if changed {
		if err := s.saveInitialDownloadNotices(ctx, notices); err != nil {
			s.logger.Printf("api: save initial download notices: %v", err)
		}
	}
}

func (s *Server) localCoverFileForSubscription(subscription model.Subscription) string {
	root, err := s.cleanDownloadDir(subscription.DownloadDir)
	if err != nil {
		return ""
	}
	series := strings.TrimSpace(subscription.SeriesDir)
	if series == "" {
		series = downloader.SeriesDirectoryName(subscription.Title, subscription.ConvertToSimplified)
	}
	covers, err := filepath.Glob(filepath.Join(root, downloader.SafeName(series), "cover.*"))
	if err != nil || len(covers) == 0 {
		return ""
	}
	sort.Strings(covers)
	return covers[0]
}

func nextSubscriptionCheckLabel(expression string, now time.Time) string {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return "未启用自动追更"
	}
	next, err := cronutil.Next(expression, now)
	if err != nil {
		return "Cron 表达式无效"
	}
	localNow := now.In(time.Local)
	localNext := next.In(time.Local)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, localNow.Location())
	target := time.Date(localNext.Year(), localNext.Month(), localNext.Day(), 0, 0, 0, 0, localNext.Location())
	days := int(target.Sub(today).Hours() / 24)
	clock := localNext.Format("15:04")
	switch days {
	case 0:
		return "今天 " + clock
	case 1:
		return "明天 " + clock
	case 2:
		return "后天 " + clock
	default:
		weekdays := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
		return "下周" + strings.TrimPrefix(weekdays[int(localNext.Weekday())], "周") + " " + clock
	}
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
		Enabled             *bool   `json:"enabled"`
		AutoDownload        *bool   `json:"autoDownload"`
		CronExpr            *string `json:"cronExpr"`
		DownloadDir         *string `json:"downloadDir"`
		SeriesDir           *string `json:"seriesDir"`
		ConvertToSimplified *bool   `json:"convertToSimplified"`
		ChapterMarker       *bool   `json:"chapterMarker"`
		CoverBlur           *bool   `json:"coverBlur"`
	}{}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := existing.Enabled
	auto := existing.AutoDownload
	cronExpr := existing.CronExpr
	downloadDir := existing.DownloadDir
	seriesDir := existing.SeriesDir
	convertToSimplified := existing.ConvertToSimplified
	chapterMarker := existing.ChapterMarker
	coverBlur := existing.CoverBlur
	oldEffectiveSeriesDir := subscriptionSeriesDirName(existing.SeriesDir, existing.Title, existing.ConvertToSimplified)
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	if payload.AutoDownload != nil {
		auto = *payload.AutoDownload
	}
	if payload.CronExpr != nil {
		// 空串表示关掉这个订阅的定时检查，不再回退到旧值。
		cronExpr = strings.TrimSpace(*payload.CronExpr)
		if cronExpr != "" {
			if err := cronutil.Validate(cronExpr); err != nil {
				writeError(w, http.StatusBadRequest, "Cron 表达式无效: "+err.Error())
				return
			}
		}
	}
	if payload.DownloadDir != nil {
		downloadDir, err = s.cleanDownloadDir(*payload.DownloadDir)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if payload.ConvertToSimplified != nil {
		convertToSimplified = *payload.ConvertToSimplified
	}
	if payload.SeriesDir != nil {
		seriesDir = subscriptionSeriesDirName(*payload.SeriesDir, existing.Title, convertToSimplified)
	} else if payload.ConvertToSimplified != nil && strings.TrimSpace(seriesDir) != "" {
		seriesDir = subscriptionSeriesDirName(seriesDir, existing.Title, convertToSimplified)
	}
	if payload.ChapterMarker != nil {
		chapterMarker = *payload.ChapterMarker
	}
	if payload.CoverBlur != nil {
		coverBlur = *payload.CoverBlur
	}
	if strings.TrimSpace(seriesDir) == "" {
		seriesDir = downloader.SeriesDirectoryName(existing.Title, convertToSimplified)
	}
	metadataTitleChanged := downloader.SafeName(oldEffectiveSeriesDir) != downloader.SafeName(seriesDir)
	if err := s.store.UpdateSubscriptionConfig(r.Context(), id, enabled, auto, cronExpr, downloadDir, seriesDir, convertToSimplified, chapterMarker, coverBlur); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 作品目录名或下载位置变化时，同步迁移本地已下载目录与任务路径。
	s.syncSubscriptionLocalDir(r.Context(), existing, downloadDir, seriesDir)
	updated, _ := s.store.Subscription(r.Context(), id)
	if metadataTitleChanged && s.engine != nil {
		if err := s.engine.SyncSeriesTitle(updated); err != nil {
			s.logger.Printf("api: sync series metadata title %s/%s: %v", updated.SourceID, updated.ComicID, err)
		}
	}
	updated = s.subscriptionWithCovers(r.Context(), updated)
	s.flushSubscriptionCoverIndex()
	_ = s.configs.PersistSubscriptions(r.Context(), s.store)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDownloadAllSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的订阅 ID")
		return
	}
	subscription, err := s.store.Subscription(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	queued, removedFiles, clearedJobs, err := s.fullRefreshSubscription(r.Context(), subscription)
	if err != nil {
		if errors.Is(err, scheduler.ErrNoChapters) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"queued":       queued,
		"skipped":      0,
		"removedFiles": removedFiles,
		"clearedJobs":  clearedJobs,
	})
}

// fullRefreshSubscription implements the shared full-refresh semantics: cancel
// running jobs, delete downloaded chapter files, clear success/failure records
// and re-queue every chapter from the source. It returns the number of queued
// chapters and the number of removed files.
func (s *Server) fullRefreshSubscription(ctx context.Context, subscription model.Subscription) (queued, removedFiles, clearedJobs int, err error) {
	jobs, err := s.store.DownloadJobsByComic(ctx, subscription.SourceID, subscription.ComicID)
	if err != nil {
		return 0, 0, 0, err
	}
	for _, job := range jobs {
		if job.Status == "running" {
			s.engine.Cancel(job.ID)
		}
	}
	for _, job := range jobs {
		removed, removeErr := s.removeDownloadedChapterFile(job)
		if removeErr != nil {
			s.logger.Printf("api: 全量下载清理文件 %s/%s: %v", subscription.SourceID, job.ChapterTitle, removeErr)
			continue
		}
		if removed {
			removedFiles++
		}
	}
	if _, err := s.store.DeleteDownloadJobsForComic(ctx, subscription.SourceID, subscription.ComicID); err != nil {
		return 0, removedFiles, len(jobs), err
	}
	queued, _, err = s.scheduler.QueueAll(ctx, subscription)
	if err != nil {
		return 0, removedFiles, len(jobs), err
	}
	return queued, removedFiles, len(jobs), nil
}

// removeDownloadedChapterFile deletes one downloaded chapter file so a full
// refresh cannot leave stale chapter names or stale image data behind.
func (s *Server) removeDownloadedChapterFile(job model.DownloadJob) (bool, error) {
	root, err := s.cleanDownloadDir(job.DownloadDir)
	if err != nil {
		return false, err
	}
	filePath := strings.TrimSpace(job.FilePath)
	if filePath == "" {
		return false, nil
	}
	target, err := filepath.Abs(filepath.Clean(filePath))
	if err != nil {
		return false, err
	}
	root, err = filepath.Abs(filepath.Clean(root))
	if err != nil {
		return false, err
	}
	within, err := filepath.Rel(root, target)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(os.PathSeparator)) {
		return false, errors.New("章节文件不在下载目录内")
	}
	if err := os.Remove(target); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Server) handleBackfillChapterMarkers(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的订阅 ID")
		return
	}
	subscription, err := s.store.Subscription(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	jobs, err := s.store.CompletedDownloadJobsByComic(r.Context(), subscription.SourceID, subscription.ComicID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	renamed, failed, err := backfillChapterMarkerFiles(r.Context(), jobs, s.store.SetDownloadJobFilePath, s.logger.Printf)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "renamed": renamed, "failed": failed, "total": len(jobs)})
}

func backfillChapterMarkerFiles(ctx context.Context, jobs []model.DownloadJob, setPath func(context.Context, int64, string) error, logf func(string, ...any)) (int, int, error) {
	renamed := 0
	failed := 0
	for _, job := range jobs {
		current := filepath.Clean(job.FilePath)
		if strings.TrimSpace(current) == "" || current == "." {
			continue
		}
		ext := filepath.Ext(current)
		if ext == "" {
			continue
		}
		target := filepath.Clean(filepath.Join(filepath.Dir(current), downloader.ChapterFileName(job.ChapterTitle, job.ChapterOrder, true, ext)))
		if target == current {
			continue
		}
		if _, statErr := os.Stat(target); statErr != nil {
			if err := os.Rename(current, target); err != nil {
				logf("api: rename chapter marker %s: %v", current, err)
				failed++
				continue
			}
		}
		if err := setPath(ctx, job.ID, target); err != nil {
			return renamed, failed, err
		}
		renamed++
	}
	return renamed, failed, nil
}

func (s *Server) handleArchiveSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的订阅 ID")
		return
	}
	if _, err := s.store.Subscription(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	if err := s.store.ArchiveSubscription(r.Context(), id, "manual"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.configs.PersistSubscriptions(r.Context(), s.store)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRestoreSubscription 把归档集里的订阅移回订阅清单，并重新启用。
func (s *Server) handleRestoreSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的订阅 ID")
		return
	}
	if _, err := s.store.Subscription(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	if err := s.store.RestoreSubscription(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	restored, _ := s.store.Subscription(r.Context(), id)
	_ = s.configs.PersistSubscriptions(r.Context(), s.store)
	writeJSON(w, http.StatusOK, restored)
}

func (s *Server) cleanDownloadDir(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = s.cfg.DownloadDir
	}
	if strings.Contains(value, "..") {
		return "", errors.New("下载位置不能包含 ..")
	}
	if !filepath.IsAbs(value) {
		resolved, err := filepath.Abs(value)
		if err != nil {
			return "", errors.New("无法解析下载位置")
		}
		value = resolved
	}
	cleaned := filepath.Clean(value)
	if cleaned == string(filepath.Separator) || cleaned == "." {
		return "", errors.New("下载位置不能是容器根目录")
	}
	return cleaned, nil
}

func (s *Server) handleDownloadDirectories(w http.ResponseWriter, r *http.Request) {
	defaultDir, err := s.cleanDownloadDir("")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	seen := map[string]bool{}
	items := make([]map[string]any, 0)
	add := func(path string, isDefault bool) {
		path = strings.TrimSpace(path)
		if path == "" || path == string(filepath.Separator) {
			return
		}
		cleaned := filepath.Clean(path)
		if seen[cleaned] {
			return
		}
		seen[cleaned] = true
		items = append(items, map[string]any{
			"path":      cleaned,
			"default":   isDefault || cleaned == defaultDir,
			"available": directoryExists(cleaned),
		})
	}
	if raw, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}
			mountpoint := unescapeMountPath(fields[4])
			if mountpoint == "" || mountpoint == "/" || mountpoint == defaultDir {
				continue
			}
			skip := false
			for _, prefix := range []string{"/proc", "/sys", "/dev", "/run", "/etc"} {
				if mountpoint == prefix || strings.HasPrefix(mountpoint, prefix+"/") {
					skip = true
					break
				}
			}
			if skip || !directoryExists(mountpoint) {
				continue
			}
			add(mountpoint, false)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i]["path"].(string) < items[j]["path"].(string)
	})
	defaultVisible := false
	for _, item := range items {
		if item["path"].(string) == defaultDir && item["available"].(bool) {
			item["default"] = true
			defaultVisible = true
		}
	}
	if !defaultVisible {
		defaultDir = ""
	}
	s.downloadDirectoryView(w, r, defaultDir, items)
}

func (s *Server) downloadDirectoryView(w http.ResponseWriter, r *http.Request, defaultDir string, roots []map[string]any) {
	requested := strings.TrimSpace(r.URL.Query().Get("path"))
	if requested == "" {
		writeJSON(w, http.StatusOK, map[string]any{"items": roots, "default": defaultDir, "current": "", "children": []map[string]any{}, "breadcrumbs": []map[string]any{}})
		return
	}
	current, err := s.cleanDownloadDir(requested)
	if err != nil || !directoryAllowed(current, roots) || !directoryExists(current) {
		writeError(w, http.StatusBadRequest, "目录不在允许访问的挂载目录中")
		return
	}
	entries, err := os.ReadDir(current)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取目录失败："+err.Error())
		return
	}
	children := make([]map[string]any, 0)
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		child := filepath.Join(current, entry.Name())
		children = append(children, map[string]any{"name": entry.Name(), "path": child, "writable": directoryWritable(child)})
	}
	sort.Slice(children, func(i, j int) bool { return children[i]["name"].(string) < children[j]["name"].(string) })
	breadcrumbs := make([]map[string]any, 0)
	for _, root := range roots {
		rootPath, _ := root["path"].(string)
		if rootPath != "" && directoryAllowed(current, []map[string]any{root}) {
			breadcrumbs = append(breadcrumbs, map[string]any{"name": filepath.Base(rootPath), "path": rootPath})
			relative, relErr := filepath.Rel(rootPath, current)
			if relErr == nil && relative != "." {
				parent := rootPath
				for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
					if part == "" || part == "." {
						continue
					}
					parent = filepath.Join(parent, part)
					breadcrumbs = append(breadcrumbs, map[string]any{"name": part, "path": parent})
				}
			}
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": roots, "default": defaultDir, "current": current,
		"parent": filepath.Dir(current), "children": children, "breadcrumbs": breadcrumbs,
	})
}

func directoryAllowed(path string, roots []map[string]any) bool {
	path = filepath.Clean(path)
	for _, root := range roots {
		rootPath, _ := root["path"].(string)
		if rootPath == "" {
			continue
		}
		relative, err := filepath.Rel(filepath.Clean(rootPath), path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func directoryWritable(path string) bool {
	file, err := os.CreateTemp(path, ".manco-write-*")
	if err != nil {
		return false
	}
	name := file.Name()
	_ = file.Close()
	_ = os.Remove(name)
	return true
}

func (s *Server) handleCreateDownloadDirectory(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		writeError(w, http.StatusBadRequest, "文件夹名称不能为空，且不能包含路径分隔符")
		return
	}
	if len([]rune(name)) > 120 {
		writeError(w, http.StatusBadRequest, "文件夹名称不能超过 120 个字符")
		return
	}
	parent, err := s.cleanDownloadDir(payload.Parent)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	path := filepath.Join(parent, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "创建文件夹失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"path": filepath.Clean(path), "name": name})
}

// handleSeriesDirectoryName 返回订阅作品目录实际使用的文件夹名（含繁转简与
// 非法字符替换）。订阅编辑窗口用它预览下载位置后面补全的目录，保证展示名与
// 实际落盘目录一致。
func (s *Server) handleSeriesDirectoryName(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Title               string `json:"title"`
		ConvertToSimplified bool   `json:"convertToSimplified"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": downloader.SeriesDirectoryName(payload.Title, payload.ConvertToSimplified),
	})
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func unescapeMountPath(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}

// cachedCoverURL 返回发现缓存里的封面地址（无缓存时返回空串）。
func (s *Server) cachedCoverURL(sourceID, comicID string) string {
	if s.discover == nil || strings.TrimSpace(sourceID) == "" || strings.TrimSpace(comicID) == "" {
		return ""
	}
	return s.discover.CachedCoverURL(sourceID, comicID)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	// 10 秒内的重复请求直接回缓存，避免多开页面时反复跑聚合查询。
	s.activityMu.Lock()
	if len(s.activityRaw) > 0 && time.Since(s.activityAt) < 10*time.Second {
		raw := s.activityRaw
		s.activityMu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
		return
	}
	s.activityMu.Unlock()
	groups, err := s.store.DownloadJobActivity(r.Context(), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type activityItem struct {
		ID         string    `json:"id"`
		ComicTitle string    `json:"comicTitle"`
		Succeeded  int       `json:"succeeded"`
		Failed     int       `json:"failed"`
		Total      int       `json:"total"`
		Level      string    `json:"level"`
		Time       time.Time `json:"time"`
		Ongoing    bool      `json:"ongoing,omitempty"`
		SourceID   string    `json:"sourceId,omitempty"`
		ComicID    string    `json:"comicId,omitempty"`
		Pending    int       `json:"pending,omitempty"`
		Cover      string    `json:"cover,omitempty"`
	}
	// 通知按作品聚合：一章一条太碎，改为每个作品一条汇总。聚合下推到
	// SQLite，开销只跟作品数相关，不再把整张任务表读进内存。
	result := make([]activityItem, 0, len(groups))
	for _, group := range groups {
		level := "info"
		switch {
		case group.Failed > 0:
			level = "error"
		case group.Succeeded > 0:
			level = "success"
		}
		result = append(result, activityItem{
			ID:         group.SourceID + "\x00" + group.ComicID,
			ComicTitle: group.Title,
			Succeeded:  group.Succeeded,
			Failed:     group.Failed,
			Pending:    group.Pending,
			Total:      group.Succeeded + group.Failed,
			Level:      level,
			Time:       group.LastAt,
			Ongoing:    group.Pending > 0,
			SourceID:   group.SourceID,
			ComicID:    group.ComicID,
			Cover:      s.cachedCoverURL(group.SourceID, group.ComicID),
		})
	}
	raw, err := json.Marshal(map[string]any{"items": result})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.activityMu.Lock()
	s.activityRaw = raw
	s.activityAt = time.Now()
	s.activityMu.Unlock()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的订阅 ID")
		return
	}
	subscription, err := s.store.Subscription(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	// 只清掉还在下载/排队的任务，已下载的文件和完成记录都保留。
	s.cancelUnfinishedJobs(r.Context(), subscription)
	if err := s.store.DeleteSubscription(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.configs.PersistSubscriptions(r.Context(), s.store)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCheckSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的订阅 ID")
		return
	}
	if err := s.scheduler.CheckOne(r.Context(), id); err != nil {
		writeError(w, http.StatusBadGateway, "检查更新失败："+err.Error())
		return
	}
	updated, err := s.store.Subscription(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	// A single check click should also recover previously failed chapters and
	// repair missing series metadata (handled inside scheduler.CheckOne).
	retried := s.retryFailedForSubscription(r.Context(), updated)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"item":      updated,
		"completed": updated.CompletedAt != nil,
		"retried":   retried,
	})
}

// retryFailedForSubscription re-queues every failed or canceled chapter for a
// subscribed work. It is used by the card check button so a single click also
// recovers previously failed downloads.
func (s *Server) retryFailedForSubscription(ctx context.Context, subscription model.Subscription) int {
	retried, err := s.engine.RetryFailedByComic(ctx, subscription.SourceID, subscription.ComicID)
	if err != nil {
		s.logger.Printf("api: retry failed jobs %s/%s: %v", subscription.SourceID, subscription.ComicID, err)
	}
	if retried > 0 {
		s.logger.Printf("api: retried %d failed download(s) for %s/%s", retried, subscription.SourceID, subscription.Title)
	}
	return retried
}

// handleFillSubscription compares the source chapter list with completed
// download records only. Local file presence is intentionally not consulted:
// the completion record is the authoritative fill state for a subscription.
func (s *Server) handleFillSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的订阅 ID")
		return
	}
	subscription, err := s.store.Subscription(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	queued, skipped, err := s.scheduler.QueueAll(r.Context(), subscription)
	if err != nil {
		if errors.Is(err, scheduler.ErrNoChapters) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queued": queued, "skipped": skipped, "missing": queued, "total": queued + skipped})
}

// handleScrapeSubscription re-fetches the upstream metadata and overwrites the
// already downloaded ComicInfo.xml / series.json / cover files. Unlike the
// metadata completion that runs during checks, it never skips work because the
// files look current.
func (s *Server) handleScrapeSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的订阅 ID")
		return
	}
	subscription, err := s.store.Subscription(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	updated, err := s.engine.ScrapeSeriesMetadata(r.Context(), subscription)
	if err != nil {
		writeError(w, http.StatusBadGateway, "刮削失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": updated})
}

func (s *Server) handleListDownloads(w http.ResponseWriter, r *http.Request) {
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	paged := status == "active" || status == "completed"
	var items []model.DownloadJob
	total := 0
	var err error
	if paged {
		items, total, err = s.store.DownloadJobPage(r.Context(), status, intParam(r, "limit", 200), intParam(r, "offset", 0))
	} else {
		items, err = s.store.ListDownloadJobs(r.Context(), intParam(r, "limit", 0))
		total = len(items)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []model.DownloadJob{}
	}
	// 批量暂停是按内容源全局生效的：冷却期间该来源下所有排队的章节
	// 都标记为 waiting，前端展示为“等待中”。
	cooldowns := s.engine.SourceCooldowns()
	// 全局批量暂停期间，所有内容源排队中的章节都显示为「等待中」。
	globalCooldown := s.engine.GlobalCooldown()
	annotated := map[string]model.Comic{}
	// 同一部作品的多个章节共用一套本地元数据，按作品缓存一次即可，否则
	// 每个章节都要读一次 ComicInfo.xml。
	localMetadata := map[string]localJobMetadata{}
	for index := range items {
		if items[index].Status == "queued" {
			if globalCooldown.After(time.Now()) {
				items[index].Status = "waiting"
			} else if until, ok := cooldowns[items[index].SourceID]; ok && until.After(time.Now()) {
				items[index].Status = "waiting"
			}
		}
		if strings.TrimSpace(items[index].FilePath) == "" {
			items[index].LocalCover = s.localCoverForJob(items[index])
		} else {
			items[index].LocalCover = s.localCoverForJob(items[index])
			if info, statErr := os.Stat(items[index].FilePath); statErr == nil && !info.IsDir() {
				items[index].Size = info.Size()
			}
		}
		key := items[index].SourceID + "\x00" + items[index].ComicID
		annotatedComic, ok := annotated[key]
		if !ok {
			annotatedComic = s.annotateDiscover(items[index].SourceID, "", model.Comic{
				SourceID: items[index].SourceID,
				ID:       items[index].ComicID,
				Title:    items[index].ComicTitle,
				Cover:    items[index].ComicCover,
				Author:   items[index].ComicAuthor,
			})
			annotated[key] = annotatedComic
		}
		if strings.TrimSpace(items[index].ComicDescription) == "" {
			items[index].ComicDescription = annotatedComic.Description
		}
		if len(items[index].ComicTags) == 0 {
			items[index].ComicTags = append([]string(nil), annotatedComic.Tags...)
		}
		if strings.TrimSpace(items[index].ComicAuthor) == "" {
			items[index].ComicAuthor = annotatedComic.Author
		}
		// 完成列表的元数据优先来自下载任务/发现缓存；旧订阅任务可能都没
		// 有存这三项，此时回退到本地作品目录里由下载器写出的 ComicInfo.xml。
		if strings.TrimSpace(items[index].ComicAuthor) == "" ||
			strings.TrimSpace(items[index].ComicDescription) == "" ||
			len(items[index].ComicTags) == 0 {
			metaKey := items[index].SourceID + "\x00" + items[index].ComicID
			local, ok := localMetadata[metaKey]
			if !ok {
				local = s.localMetadataForJob(items[index])
				localMetadata[metaKey] = local
			}
			if strings.TrimSpace(items[index].ComicAuthor) == "" {
				items[index].ComicAuthor = local.Author
			}
			if strings.TrimSpace(items[index].ComicDescription) == "" {
				items[index].ComicDescription = local.Description
			}
			if len(items[index].ComicTags) == 0 && len(local.Tags) > 0 {
				items[index].ComicTags = append([]string(nil), local.Tags...)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func subscriptionSeriesDirName(value, title string, convertToSimplified bool) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(title)
	}
	if convertToSimplified {
		value = downloader.SimplifiedText(value)
	}
	return downloader.SafeName(value)
}

// syncSubscriptionLocalDir 在订阅的作品目录名或下载位置改变后，把本地已下载的
// 作品文件夹迁移到新位置，并把该作品下载任务的目录与文件路径改指到新路径。
// 目标目录已存在时不覆盖，仅保留现有数据。
func (s *Server) syncSubscriptionLocalDir(ctx context.Context, existing model.Subscription, newDownloadDir, newSeriesDir string) {
	oldRoot, errOld := s.cleanDownloadDir(existing.DownloadDir)
	newRoot, errNew := s.cleanDownloadDir(newDownloadDir)
	if errOld != nil || errNew != nil {
		return
	}
	oldSeries := strings.TrimSpace(existing.SeriesDir)
	if oldSeries == "" {
		oldSeries = downloader.SeriesDirectoryName(existing.Title, existing.ConvertToSimplified)
	}
	newSeries := strings.TrimSpace(newSeriesDir)
	if newSeries == "" {
		return
	}
	oldPath := filepath.Join(oldRoot, downloader.SafeName(oldSeries))
	newPath := filepath.Join(newRoot, downloader.SafeName(newSeries))
	if filepath.Clean(oldPath) == filepath.Clean(newPath) {
		return
	}
	if info, err := os.Stat(oldPath); err != nil || !info.IsDir() {
		return
	}
	if _, err := os.Stat(newPath); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return
	}
	jobs, err := s.store.ListDownloadJobs(ctx, 100000)
	if err != nil {
		return
	}
	for _, job := range jobs {
		if job.SourceID != existing.SourceID || job.ComicID != existing.ComicID {
			continue
		}
		dir := job.DownloadDir
		if cleaned, dirErr := s.cleanDownloadDir(job.DownloadDir); dirErr == nil && filepath.Clean(cleaned) == filepath.Clean(oldRoot) {
			dir = newRoot
		}
		filePath := strings.TrimSpace(job.FilePath)
		if filePath != "" {
			if rel, relErr := filepath.Rel(oldPath, filePath); relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				filePath = filepath.Join(newPath, rel)
			}
		}
		_ = s.store.SetDownloadJobLocation(ctx, job.ID, dir, filePath)
	}
}

func (s *Server) localCoverForJob(job model.DownloadJob) string {
	if strings.TrimSpace(job.FilePath) == "" {
		return ""
	}
	root, err := s.cleanDownloadDir(job.DownloadDir)
	if err != nil {
		return ""
	}
	folder, err := filepath.Abs(filepath.Dir(job.FilePath))
	if err != nil {
		return ""
	}
	return s.localCoverForDir(root, folder)
}

type localJobMetadata struct {
	Author      string
	Description string
	Tags        []string
}

// localMetadataForJob reads the series-level ComicInfo.xml written next to a
// downloaded work so completed-list entries can still show author, summary and
// tags when the download job row predates those columns.
func (s *Server) localMetadataForJob(job model.DownloadJob) localJobMetadata {
	root, err := s.cleanDownloadDir(job.DownloadDir)
	if err != nil {
		return localJobMetadata{}
	}
	folders := make([]string, 0, 2)
	if filePath := strings.TrimSpace(job.FilePath); filePath != "" {
		if folder, absErr := filepath.Abs(filepath.Dir(filePath)); absErr == nil {
			folders = append(folders, folder)
		}
	}
	if title := strings.TrimSpace(job.ComicTitle); title != "" {
		folders = append(folders, filepath.Join(root, downloader.SafeName(title)))
	}
	seen := map[string]bool{}
	for _, folder := range folders {
		folder = filepath.Clean(folder)
		if seen[folder] {
			continue
		}
		seen[folder] = true
		raw, readErr := os.ReadFile(filepath.Join(folder, "ComicInfo.xml"))
		if readErr != nil {
			continue
		}
		var info downloader.ComicInfo
		if xml.Unmarshal(raw, &info) != nil {
			continue
		}
		metadata := localJobMetadata{
			Author:      strings.TrimSpace(info.Writer),
			Description: strings.TrimSpace(info.Summary),
		}
		metadata.Tags = normalizeMetadataTags(strings.FieldsFunc(info.Genre, func(r rune) bool {
			return r == ',' || r == '，' || r == ';' || r == '；'
		}))
		if metadata.Author == "" && metadata.Description == "" && len(metadata.Tags) == 0 {
			continue
		}
		return metadata
	}
	return localJobMetadata{}
}

func (s *Server) localCoverForSubscription(subscription model.Subscription) string {
	root, err := s.cleanDownloadDir(subscription.DownloadDir)
	if err != nil {
		return ""
	}
	series := strings.TrimSpace(subscription.SeriesDir)
	if series == "" {
		series = downloader.SeriesDirectoryName(subscription.Title, subscription.ConvertToSimplified)
	}
	return s.localCoverForDir(root, filepath.Join(root, downloader.SafeName(series)))
}

func (s *Server) localCoverForDir(root, folder string) string {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return ""
	}
	folder, err = filepath.Abs(filepath.Clean(folder))
	if err != nil {
		return ""
	}
	key := root + "\x00" + folder
	var folderMod int64
	folderExists := false
	if info, statErr := os.Stat(folder); statErr == nil {
		folderMod = info.ModTime().UnixNano()
		folderExists = true
	}
	if value, ok := s.cachedLocalCover(key, folderMod, folderExists); ok {
		return value
	}
	value := resolveLocalCover(root, folder)
	// 未命中也缓存，但同时记录目录状态。目录或封面发生变化时签名会失效，
	// 因此既不会每轮轮询都扫描 NAS，也不会把刚下载的封面缓存成长期空白。
	s.rememberLocalCover(key, value, folderMod, folderExists)
	return value
}

func (s *Server) cachedLocalCover(key string, folderMod int64, folderExists bool) (string, bool) {
	s.coverCacheMu.Lock()
	defer s.coverCacheMu.Unlock()
	entry, ok := s.coverCache[key]
	if !ok || time.Now().After(entry.expires) || entry.folderMod != folderMod || entry.folderExists != folderExists {
		return "", false
	}
	return entry.value, true
}

func (s *Server) rememberLocalCover(key, value string, folderMod int64, folderExists bool) {
	s.coverCacheMu.Lock()
	defer s.coverCacheMu.Unlock()
	if s.coverCache == nil {
		s.coverCache = make(map[string]coverCacheEntry, 64)
	}
	// 缓存只用于削掉重复的目录扫描，条目太多时整体丢弃即可。
	if len(s.coverCache) > 4096 {
		s.coverCache = make(map[string]coverCacheEntry, 64)
	}
	s.coverCache[key] = coverCacheEntry{
		value:        value,
		expires:      time.Now().Add(localCoverCacheTTL),
		folderMod:    folderMod,
		folderExists: folderExists,
	}
}

func resolveLocalCover(root, folder string) string {
	covers, err := filepath.Glob(filepath.Join(folder, "cover.*"))
	if err != nil || len(covers) == 0 {
		return ""
	}
	sort.Strings(covers)
	for _, candidate := range covers {
		info, statErr := os.Stat(candidate)
		if statErr != nil || info.IsDir() {
			continue
		}
		relative, relErr := filepath.Rel(root, candidate)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			continue
		}
		return localPath(root, filepath.ToSlash(relative))
	}
	return ""
}

func (s *Server) subscriptionWithCovers(ctx context.Context, subscription model.Subscription) model.Subscription {
	subscription.LocalCover = s.localCoverForSubscription(subscription)
	indexedCover := ""
	if s.subscriptionCovers != nil {
		coverSource := localCoverFileFromToken(subscription, subscription.LocalCover)
		if coverSource == "" && s.discover != nil {
			coverSource = s.discover.CachedCoverPath(subscription.SourceID, subscription.ComicID)
		}
		if url, err := s.subscriptionCovers.Update(subscription, coverSource); err == nil {
			indexedCover = url
		} else {
			s.logger.Printf("api: index subscription cover %s/%s: %v", subscription.SourceID, subscription.ComicID, err)
		}
	}
	if subscription.LocalCover == "" {
		switch {
		case indexedCover != "":
			subscription.CachedCover = indexedCover
		case s.discover != nil:
			subscription.CachedCover = s.discover.CachedCoverURL(subscription.SourceID, subscription.ComicID)
		}
	}
	return subscription
}

func localCoverFileFromToken(subscription model.Subscription, token string) string {
	if strings.TrimSpace(token) == "" {
		return ""
	}
	separator := strings.LastIndex(token, "|")
	if separator <= 0 || separator == len(token)-1 {
		return ""
	}
	root := token[:separator]
	relative := filepath.FromSlash(token[separator+1:])
	if filePath, err := filepath.Abs(root); err == nil {
		root = filePath
	}
	candidate := filepath.Join(root, relative)
	relativeToRoot, err := filepath.Rel(root, candidate)
	if err != nil || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(os.PathSeparator)) {
		return ""
	}
	if info, err := os.Stat(candidate); err != nil || info.IsDir() || info.Size() == 0 {
		return ""
	}
	return candidate
}

func (s *Server) flushSubscriptionCoverIndex() {
	if s.subscriptionCovers == nil {
		return
	}
	if err := s.subscriptionCovers.Flush(); err != nil {
		s.logger.Printf("api: flush subscription cover index: %v", err)
	}
}

func (s *Server) localCoverFallbacks(ctx context.Context) map[string]string {
	fallbacks := map[string]string{}
	remember := func(sourceID, comicID, cover string) {
		sourceID = strings.TrimSpace(sourceID)
		comicID = strings.TrimSpace(comicID)
		cover = strings.TrimSpace(cover)
		if sourceID == "" || comicID == "" || cover == "" {
			return
		}
		key := sourceID + "\x00" + comicID
		if fallbacks[key] == "" {
			fallbacks[key] = cover
		}
	}
	if subscriptions, err := s.store.ListAllSubscriptions(ctx); err == nil {
		for _, subscription := range subscriptions {
			remember(subscription.SourceID, subscription.ComicID, subscription.Cover)
		}
	}
	if jobs, err := s.store.ListDownloadJobs(ctx, 600); err == nil {
		for _, job := range jobs {
			remember(job.SourceID, job.ComicID, job.ComicCover)
		}
	}
	return fallbacks
}

func (s *Server) handleDownloadSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "无效的订阅 ID")
		return
	}
	subscription, err := s.store.Subscription(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "订阅不存在")
		return
	}
	job, err := s.scheduler.QueueLatest(r.Context(), subscription)
	if err != nil {
		if errors.Is(err, scheduler.ErrNoChapters) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": job})
}

func (s *Server) handleCreateDownload(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		SourceID            string          `json:"sourceId"`
		ComicID             string          `json:"comicId"`
		ComicTitle          string          `json:"comicTitle"`
		ComicCover          string          `json:"comicCover"`
		ComicAuthor         string          `json:"comicAuthor"`
		ComicDescription    string          `json:"comicDescription"`
		ComicTags           []string        `json:"comicTags"`
		SeriesDir           string          `json:"seriesDir"`
		DownloadDir         string          `json:"downloadDir"`
		ConvertToSimplified *bool           `json:"convertToSimplified"`
		ChapterMarker       *bool           `json:"chapterMarker"`
		CoverBlur           *bool           `json:"coverBlur"`
		AutoDownload        bool            `json:"autoDownload"`
		MergeChapters       bool            `json:"mergeChapters"`
		Chapters            []model.Chapter `json:"chapters"`
		Chapter             *model.Chapter  `json:"chapter"`
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
	downloadDir, err := s.cleanDownloadDir(payload.DownloadDir)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	convertToSimplified := s.loadSettings(r.Context()).ConvertToSimplified
	if payload.ConvertToSimplified != nil {
		convertToSimplified = *payload.ConvertToSimplified
	}
	chapterMarker := payload.ChapterMarker != nil && *payload.ChapterMarker
	seriesDir := subscriptionSeriesDirName(payload.SeriesDir, title, convertToSimplified)
	created := make([]model.DownloadJob, 0, len(chapters))
	queuedCount := 0
	skippedCount := 0
	normalizedChapters := make([]model.Chapter, 0, len(chapters))
	for _, chapter := range chapters {
		if strings.TrimSpace(chapter.ID) != "" {
			normalizedChapters = append(normalizedChapters, chapter)
		}
	}
	sort.SliceStable(normalizedChapters, func(i, j int) bool {
		if normalizedChapters[i].Order == normalizedChapters[j].Order {
			return normalizedChapters[i].ID < normalizedChapters[j].ID
		}
		return normalizedChapters[i].Order < normalizedChapters[j].Order
	})
	if len(normalizedChapters) == 0 {
		writeError(w, http.StatusBadRequest, "缺少可下载章节")
		return
	}
	if payload.MergeChapters && len(normalizedChapters) > 1 {
		hashInput := make([]string, 0, len(normalizedChapters))
		for _, chapter := range normalizedChapters {
			hashInput = append(hashInput, chapter.ID)
		}
		sum := sha256.Sum256([]byte(strings.Join(hashInput, "\x00")))
		first := normalizedChapters[0]
		last := normalizedChapters[len(normalizedChapters)-1]
		chapterTitle := fmt.Sprintf("%s - %s", source.FirstNonEmpty(first.Title, first.ID), source.FirstNonEmpty(last.Title, last.ID))
		saved, err := s.store.CreateDownloadJob(r.Context(), model.DownloadJob{
			SourceID:            payload.SourceID,
			ComicID:             payload.ComicID,
			ComicTitle:          seriesDir,
			ComicCover:          payload.ComicCover,
			ComicAuthor:         strings.TrimSpace(payload.ComicAuthor),
			ComicDescription:    strings.TrimSpace(payload.ComicDescription),
			ComicTags:           append([]string(nil), payload.ComicTags...),
			ChapterID:           "merge-" + hex.EncodeToString(sum[:6]),
			ChapterTitle:        chapterTitle,
			ChapterOrder:        first.Order,
			MergeChapters:       normalizedChapters,
			DownloadDir:         downloadDir,
			ConvertToSimplified: convertToSimplified,
			ChapterMarker:       chapterMarker,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if saved.Status == "completed" {
			skippedCount++
		} else {
			queuedCount++
		}
		created = append(created, saved)
	} else {
		for _, chapter := range normalizedChapters {
			saved, err := s.store.CreateDownloadJob(r.Context(), model.DownloadJob{
				SourceID:            payload.SourceID,
				ComicID:             payload.ComicID,
				ComicTitle:          seriesDir,
				ComicCover:          payload.ComicCover,
				ComicAuthor:         strings.TrimSpace(payload.ComicAuthor),
				ComicDescription:    strings.TrimSpace(payload.ComicDescription),
				ComicTags:           append([]string(nil), payload.ComicTags...),
				ChapterID:           chapter.ID,
				ChapterTitle:        source.FirstNonEmpty(chapter.Title, chapter.ID),
				ChapterOrder:        chapter.Order,
				DownloadDir:         downloadDir,
				ConvertToSimplified: convertToSimplified,
				ChapterMarker:       chapterMarker,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if saved.Status == "completed" {
				skippedCount++
			} else {
				queuedCount++
			}
			created = append(created, saved)
		}
	}
	// 直接「下载」会临时出现在订阅清单里（方便看进度、也能中途取消）。任务全部
	// 结束后由 pruneTemporarySubscriptions 移除卡片，已下载文件保留；如果这部
	// 作品本来就有正式订阅，则不动它。
	if len(created) > 0 {
		if _, subErr := s.store.SubscriptionByComic(r.Context(), payload.SourceID, payload.ComicID); errors.Is(subErr, store.ErrNotFound) {
			lastChapter := normalizedChapters[len(normalizedChapters)-1]
			if _, err := s.store.UpsertSubscription(r.Context(), model.Subscription{
				SourceID:            payload.SourceID,
				ComicID:             payload.ComicID,
				Title:               title,
				Author:              strings.TrimSpace(payload.ComicAuthor),
				SeriesDir:           seriesDir,
				Cover:               payload.ComicCover,
				DownloadDir:         downloadDir,
				Enabled:             true,
				ConvertToSimplified: convertToSimplified,
				ChapterMarker:       chapterMarker,
				CoverBlur:           payload.CoverBlur != nil && *payload.CoverBlur,
				LastChapterID:       lastChapter.ID,
				LastChapterTitle:    source.FirstNonEmpty(lastChapter.Title, lastChapter.ID),
				LastChapterOrder:    lastChapter.Order,
				Temporary:           true,
			}); err != nil {
				s.logger.Printf("api: temporary subscription: %v", err)
			}
		}
	}
	s.engine.Notify()
	writeJSON(w, http.StatusOK, map[string]any{"items": created, "queued": queuedCount, "skipped": skippedCount})
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
			root := source.FirstNonEmpty(job.DownloadDir, s.cfg.DownloadDir)
			if err := removeDownloadedFile(root, job.FilePath); err != nil {
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
	jobs, err := s.store.ListDownloadJobs(r.Context(), 600)
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

type localFileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Ext     string    `json:"ext"`
	Kind    string    `json:"kind"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

type localItemEntry struct {
	Title       string           `json:"title"`
	Path        string           `json:"path"`
	Kind        string           `json:"kind"`
	SourceID    string           `json:"sourceId,omitempty"`
	ComicID     string           `json:"comicId,omitempty"`
	Author      string           `json:"author,omitempty"`
	Description string           `json:"description,omitempty"`
	Tags        []string         `json:"tags,omitempty"`
	Status      string           `json:"status,omitempty"`
	Publisher   string           `json:"publisher,omitempty"`
	Language    string           `json:"language,omitempty"`
	PublishedAt string           `json:"publishedAt,omitempty"`
	Cover       string           `json:"cover,omitempty"`
	CachedCover string           `json:"cachedCover,omitempty"`
	SourceCover string           `json:"sourceCover,omitempty"`
	CoverBlur   bool             `json:"coverBlur,omitempty"`
	Files       []localFileEntry `json:"files"`
	TotalFiles  int              `json:"totalFiles"`
	Size        int64            `json:"size"`
	UpdatedAt   time.Time        `json:"updatedAt"`
}

func (s *Server) localRoots(ctx context.Context) []string {
	seen := map[string]bool{}
	roots := make([]string, 0, 4)
	add := func(raw string) {
		cleaned, err := s.cleanDownloadDir(raw)
		if err != nil || seen[cleaned] {
			return
		}
		seen[cleaned] = true
		roots = append(roots, cleaned)
	}
	if subscriptions, err := s.store.ListAllSubscriptions(ctx); err == nil {
		for _, item := range subscriptions {
			// 本地库目录胶囊按订阅创建顺序排列，包括已归档订阅。
			add(item.DownloadDir)
		}
	}
	if jobs, err := s.store.ListDownloadJobs(ctx, 600); err == nil {
		for _, job := range jobs {
			if job.DownloadDir != "" {
				add(job.DownloadDir)
			}
		}
	}
	// 最后补默认下载目录，兼容没有订阅记录的旧文件。
	add("")
	return roots
}

func localPath(root, rel string) string {
	return strings.TrimRight(root, "/") + "|" + filepath.ToSlash(rel)
}

// handleLocalLibrary lists the download directory directly, so the local
// library reflects the mounted downloads folder even for files that were not
// produced by a recorded download job.
func (s *Server) handleLocalLibrary(w http.ResponseWriter, r *http.Request) {
	roots := s.localRoots(r.Context())
	subscriptions, _ := s.store.ListAllSubscriptions(r.Context())
	coverBlurByComic := make(map[string]bool, len(subscriptions))
	blurSignature := make([]string, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		key := subscription.SourceID + "\x00" + subscription.ComicID
		coverBlurByComic[key] = subscription.CoverBlur
		if subscription.CoverBlur {
			blurSignature = append(blurSignature, key)
		}
	}
	sort.Strings(blurSignature)
	// 60 秒内的重复请求直接用上次扫描结果，刷新页面不再等 NAS 目录遍历。
	cacheKey := strings.Join(roots, "\x00") + "\x01" + strings.Join(blurSignature, "\x00")
	s.libraryMu.Lock()
	if s.libraryItems != nil && s.libraryKey == cacheKey && time.Since(s.libraryAt) < 60*time.Second {
		items := s.libraryItems
		cachedRoots := s.libraryRoots
		s.libraryMu.Unlock()
		s.writeLocalLibrary(w, r, items, cachedRoots)
		return
	}
	s.libraryMu.Unlock()
	fallbacks := s.localCoverFallbacks(r.Context())
	items := make([]localItemEntry, 0, 16)
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			item := s.scanLocalDir(root, entry.Name())
			if len(item.Files) == 0 {
				continue
			}
			item.Path = localPath(root, entry.Name())
			if item.Cover != "" {
				item.Cover = localPath(root, item.Cover)
			}
			sourceID := strings.TrimSpace(item.SourceID)
			comicID := strings.TrimSpace(item.ComicID)
			if sourceID != "" && comicID != "" {
				if s.discover != nil {
					item.CachedCover = s.discover.CachedCoverURL(sourceID, comicID)
				}
				item.SourceCover = fallbacks[sourceID+"\x00"+comicID]
				item.CoverBlur = coverBlurByComic[sourceID+"\x00"+comicID]
			}
			for i := range item.Files {
				item.Files[i].Path = localPath(root, item.Files[i].Path)
			}
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	s.libraryMu.Lock()
	s.libraryItems = items
	s.libraryRoots = roots
	s.libraryKey = cacheKey
	s.libraryAt = time.Now()
	s.libraryMu.Unlock()
	s.writeLocalLibrary(w, r, items, roots)
}

// writeLocalLibrary 按 limit/offset 切页并输出；扫描结果由 handleLocalLibrary 缓存 60 秒。
func (s *Server) writeLocalLibrary(w http.ResponseWriter, r *http.Request, items []localItemEntry, roots []string) {
	total := len(items)
	if limit := intParam(r, "limit", 0); limit > 0 {
		offset := intParam(r, "offset", 0)
		if offset < 0 {
			offset = 0
		}
		if offset > total {
			offset = total
		}
		end := offset + limit
		if end > total {
			end = total
		}
		items = items[offset:end]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "downloadDir": s.cfg.DownloadDir, "downloadDirs": roots})
}

func (s *Server) scanLocalDir(root, name string) localItemEntry {
	item := localItemEntry{Title: name, Path: name, Kind: "comic", Files: []localFileEntry{}}
	kindFromMetadata := false
	folder := filepath.Join(root, name)
	files, err := os.ReadDir(folder)
	if err != nil {
		return item
	}
	for _, file := range files {
		if file.IsDir() {
			if strings.HasPrefix(file.Name(), ".") {
				continue
			}
			nested, size, updated := scanNestedOtherFiles(folder, name, file.Name())
			item.Files = append(item.Files, nested...)
			item.TotalFiles += len(nested)
			item.Size += size
			if updated.After(item.UpdatedAt) {
				item.UpdatedAt = updated
			}
			continue
		}
		info, err := file.Info()
		if err != nil {
			continue
		}
		item.TotalFiles++
		lower := strings.ToLower(file.Name())
		if strings.HasPrefix(lower, "cover.") {
			item.Cover = filepath.ToSlash(filepath.Join(name, file.Name()))
			continue
		}
		switch lower {
		case "book.json":
			item.Kind = "book"
			applyBookMeta(filepath.Join(folder, file.Name()), &item)
			kindFromMetadata = true
			continue
		case "comicinfo.xml":
			applyComicMeta(filepath.Join(folder, file.Name()), &item)
			kindFromMetadata = true
			continue
		case "series.json":
			applySeriesMeta(filepath.Join(folder, file.Name()), &item)
			kindFromMetadata = true
			continue
		case "tvshow.nfo":
			applyOtherNFO(filepath.Join(folder, file.Name()), &item)
			kindFromMetadata = true
			continue
		case "metadata.opf":
			applyOPFMeta(filepath.Join(folder, file.Name()), &item)
			kindFromMetadata = true
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(file.Name())), ".")
		kind := localFileKindForExtension(ext)
		if kind == "" {
			continue
		}
		item.Files = append(item.Files, localFileEntry{
			Name:    file.Name(),
			Path:    filepath.ToSlash(filepath.Join(name, file.Name())),
			Ext:     ext,
			Kind:    kind,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
		item.Size += info.Size()
		if info.ModTime().After(item.UpdatedAt) {
			item.UpdatedAt = info.ModTime()
		}
	}
	sort.SliceStable(item.Files, func(i, j int) bool { return item.Files[i].Name < item.Files[j].Name })
	if !kindFromMetadata && len(item.Files) > 0 {
		allSame := true
		kind := item.Files[0].Kind
		for _, file := range item.Files {
			if file.Kind != kind {
				allSame = false
				break
			}
		}
		if allSame {
			item.Kind = kind
		}
	}
	return item
}

func scanNestedOtherFiles(seriesDir, seriesName, subdir string) ([]localFileEntry, int64, time.Time) {
	entries, err := os.ReadDir(filepath.Join(seriesDir, subdir))
	if err != nil {
		return nil, 0, time.Time{}
	}
	files := make([]localFileEntry, 0, len(entries))
	var size int64
	var updated time.Time
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(entry.Name())), ".")
		kind := localFileKindForExtension(ext)
		if kind == "" {
			continue
		}
		files = append(files, localFileEntry{
			Name:    entry.Name(),
			Path:    filepath.ToSlash(filepath.Join(seriesName, subdir, entry.Name())),
			Ext:     ext,
			Kind:    kind,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
		size += info.Size()
		if info.ModTime().After(updated) {
			updated = info.ModTime()
		}
	}
	return files, size, updated
}

func localFileKindForExtension(extension string) string {
	switch strings.ToLower(strings.TrimSpace(extension)) {
	case "cbz", "zip", "cbr", "rar":
		return "comic"
	case "txt", "epub":
		return "book"
	case "pdf", "mp4", "m4v", "mkv", "webm", "mov", "avi", "mp3", "m4a", "aac", "flac", "wav", "ogg", "opus", "m3u8", "html", "json", "bin", "jpg", "jpeg", "png", "gif", "webp", "avif", "svg":
		return "other"
	default:
		return ""
	}
}

func applyBookMeta(path string, item *localItemEntry) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var payload struct {
		SourceID    string   `json:"source"`
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		Author      string   `json:"author"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	if payload.Title != "" {
		item.Title = payload.Title
	}
	item.SourceID = payload.SourceID
	item.ComicID = payload.ID
	item.Author = payload.Author
	item.Description = payload.Description
	item.Tags = normalizeMetadataTags(payload.Tags)
}

func applyComicMeta(path string, item *localItemEntry) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var info downloader.ComicInfo
	if xml.Unmarshal(raw, &info) != nil {
		return
	}
	var legacyStatus struct {
		Status string `xml:"Status"`
	}
	_ = xml.Unmarshal(raw, &legacyStatus)
	if info.Series != "" {
		item.Title = info.Series
	}
	if info.Writer != "" {
		item.Author = info.Writer
	}
	if info.Summary != "" {
		item.Description = info.Summary
	}
	item.Tags = normalizeMetadataTags(strings.FieldsFunc(info.Genre, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；'
	}))
	// ComicInfo has no dedicated book/comic field, so the single-format downloader
	// encodes the work kind through the genre list it always appends.
	for _, tag := range item.Tags {
		if tag == "书籍" {
			item.Kind = "book"
			break
		}
		if tag == "其他" {
			item.Kind = "other"
			break
		}
	}
	if legacyStatus.Status != "" {
		item.Status = legacyStatus.Status
	}
	if info.Publisher != "" {
		item.Publisher = info.Publisher
	}
	if info.LanguageISO != "" {
		item.Language = info.LanguageISO
	}
	if info.Year > 0 {
		item.PublishedAt = fmt.Sprintf("%04d", info.Year)
		if info.Month > 0 {
			item.PublishedAt += fmt.Sprintf("-%02d", info.Month)
			if info.Day > 0 {
				item.PublishedAt += fmt.Sprintf("-%02d", info.Day)
			}
		}
	}
	for _, part := range strings.Fields(info.Notes) {
		if value, ok := strings.CutPrefix(part, "source="); ok {
			item.SourceID = value
		}
		if value, ok := strings.CutPrefix(part, "comicId="); ok {
			item.ComicID = value
		}
		if value, ok := strings.CutPrefix(part, "status="); ok {
			if decoded, err := url.QueryUnescape(value); err == nil {
				item.Status = decoded
			} else {
				item.Status = value
			}
		}
	}
}

// applySeriesMeta imports the Mylar series.json shape used by Komga. Values
// already populated from a more specific format are preserved by callers that
// need precedence; this function fills the fields the local detail page uses.
func applySeriesMeta(path string, item *localItemEntry) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var payload struct {
		Metadata struct {
			Type                 string          `json:"type"`
			Publisher            string          `json:"publisher"`
			Name                 string          `json:"name"`
			ComicID              json.RawMessage `json:"comicid"`
			Year                 int             `json:"year"`
			DescriptionText      string          `json:"description_text"`
			DescriptionFormatted string          `json:"description_formatted"`
			Volume               *int            `json:"volume"`
			AgeRating            string          `json:"age_rating"`
			ComicImage           string          `json:"comic_image"`
			TotalIssues          json.RawMessage `json:"total_issues"`
			Status               string          `json:"status"`
			Language             string          `json:"language"`
			ReadingDirection     string          `json:"readingDirection"`
			Authors              []string        `json:"authors"`
			Genres               []string        `json:"genres"`
			Tags                 []string        `json:"tags"`
		} `json:"metadata"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	meta := payload.Metadata
	title := strings.TrimSpace(meta.Name)
	if meta.Volume != nil && *meta.Volume > 1 && meta.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, meta.Year)
	}
	if title != "" {
		item.Title = title
	}
	item.ComicID = decodeJSONScalar(meta.ComicID)
	if len(meta.Authors) > 0 {
		item.Author = strings.Join(normalizeMetadataTags(meta.Authors), ", ")
	}
	description := strings.TrimSpace(meta.DescriptionText)
	if strings.TrimSpace(meta.DescriptionFormatted) != "" {
		description = stripHTMLMetadata(meta.DescriptionFormatted)
	}
	if description != "" {
		item.Description = description
	}
	item.Tags = normalizeMetadataTags(append(append([]string{}, meta.Genres...), meta.Tags...))
	item.Status = localStatusLabel(meta.Status)
	item.Publisher = strings.TrimSpace(meta.Publisher)
	item.Language = strings.TrimSpace(meta.Language)
	if meta.Year > 0 {
		item.PublishedAt = fmt.Sprintf("%04d", meta.Year)
	}
}

func applyOtherNFO(path string, item *localItemEntry) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var nfo downloader.OtherNFO
	if xml.Unmarshal(raw, &nfo) != nil {
		return
	}
	item.Kind = "other"
	if title := strings.TrimSpace(nfo.Title); title != "" {
		item.Title = title
	}
	if plot := strings.TrimSpace(nfo.Plot); plot != "" {
		item.Description = plot
	}
	item.Tags = normalizeMetadataTags(append(append([]string{}, nfo.Genres...), nfo.Tags...))
	item.Status = localStatusLabel(nfo.Status)
	item.Publisher = strings.TrimSpace(nfo.Studio)
	if nfo.Year > 0 {
		item.PublishedAt = fmt.Sprintf("%04d", nfo.Year)
	}
	authors := make([]string, 0, len(nfo.Actors))
	for _, actor := range nfo.Actors {
		if name := strings.TrimSpace(actor.Name); name != "" {
			authors = append(authors, name)
		}
	}
	item.Author = strings.Join(normalizeMetadataTags(authors), ", ")
	for _, uniqueID := range nfo.UniqueIDs {
		sourceID, comicID, ok := strings.Cut(strings.TrimSpace(uniqueID.Value), ":")
		if !ok {
			continue
		}
		item.SourceID = strings.TrimSpace(sourceID)
		item.ComicID = strings.TrimSpace(comicID)
		break
	}
}

// applyOPFMeta reads EPUB/OPF package metadata without requiring the callers to
// unzip an EPUB first. The local library files remain the book source of truth;
// this only enriches the detail page when an OPF sidecar is present.
func applyOPFMeta(path string, item *localItemEntry) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var payload struct {
		Metadata struct {
			Title       string   `xml:"title"`
			Creators    []string `xml:"creator"`
			Description string   `xml:"description"`
			Language    string   `xml:"language"`
			Date        string   `xml:"date"`
			Publisher   string   `xml:"publisher"`
			Identifiers []string `xml:"identifier"`
		} `xml:"metadata"`
	}
	if xml.Unmarshal(raw, &payload) != nil {
		return
	}
	meta := payload.Metadata
	if title := strings.TrimSpace(meta.Title); title != "" {
		item.Title = title
	}
	if creators := normalizeMetadataTags(meta.Creators); len(creators) > 0 {
		item.Author = strings.Join(creators, ", ")
	}
	if description := strings.TrimSpace(meta.Description); description != "" {
		item.Description = description
	}
	item.Language = strings.TrimSpace(meta.Language)
	item.Publisher = strings.TrimSpace(meta.Publisher)
	item.PublishedAt = strings.TrimSpace(meta.Date)
	for _, identifier := range meta.Identifiers {
		identifier = strings.TrimSpace(identifier)
		if identifier == "" {
			continue
		}
		identifier = strings.TrimPrefix(identifier, "urn:")
		item.ComicID = identifier
		break
	}
}

func decodeJSONScalar(raw json.RawMessage) string {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text)
	}
	var scalar any
	if json.Unmarshal(raw, &scalar) == nil {
		switch typed := scalar.(type) {
		case float64:
			if typed == float64(int64(typed)) {
				return strconv.FormatInt(int64(typed), 10)
			}
			return strconv.FormatFloat(typed, 'f', -1, 64)
		case string:
			return strings.TrimSpace(typed)
		}
	}
	return strings.Trim(value, `"`)
}

func stripHTMLMetadata(value string) string {
	text := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(text), " ")
}

func localStatusLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ended":
		return "已完结"
	case "continuing", "ongoing":
		return "连载中"
	default:
		return strings.TrimSpace(value)
	}
}

func normalizeMetadataTags(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func (s *Server) resolveLocalTarget(ctx context.Context, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("缺少 path 参数")
	}
	rootValue := s.cfg.DownloadDir
	relative := value
	if rawRoot, rawRelative, ok := strings.Cut(value, "|"); ok {
		rootValue = rawRoot
		relative = rawRelative
	}
	cleanedRoot := filepath.Clean(rootValue)
	allowed := false
	for _, candidate := range s.localRoots(ctx) {
		if candidate == cleanedRoot {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", errors.New("路径不在下载目录内")
	}
	root, err := filepath.Abs(cleanedRoot)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return "", err
	}
	within, err := filepath.Rel(root, target)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(os.PathSeparator)) {
		return "", errors.New("路径不在下载目录内")
	}
	return target, nil
}

func isImageArchiveEntry(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".avif":
		return true
	default:
		return false
	}
}

// handleLocalCBZ lists image entries from a downloaded chapter archive.
func (s *Server) handleLocalCBZ(w http.ResponseWriter, r *http.Request) {
	target, err := s.resolveLocalTarget(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	archive, err := zip.OpenReader(target)
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法读取 CBZ 文件: "+err.Error())
		return
	}
	defer archive.Close()
	type entry struct {
		Name string `json:"name"`
		Size uint64 `json:"size"`
	}
	items := make([]entry, 0, len(archive.File))
	for _, file := range archive.File {
		if file.FileInfo().IsDir() || !isImageArchiveEntry(file.Name) || strings.Contains(file.Name, "..") {
			continue
		}
		items = append(items, entry{Name: file.Name, Size: file.UncompressedSize64})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleLocalCBZFile streams one image entry without extracting the archive.
func (s *Server) handleLocalCBZFile(w http.ResponseWriter, r *http.Request) {
	target, err := s.resolveLocalTarget(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entryName := strings.TrimSpace(r.URL.Query().Get("entry"))
	if entryName == "" || strings.Contains(entryName, "..") || !isImageArchiveEntry(entryName) {
		writeError(w, http.StatusBadRequest, "无效的 CBZ 条目")
		return
	}
	archive, err := zip.OpenReader(target)
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法读取 CBZ 文件: "+err.Error())
		return
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name != entryName || file.FileInfo().IsDir() {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		defer reader.Close()
		contentType := mime.TypeByExtension(strings.ToLower(path.Ext(entryName)))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Header().Set("Content-Length", strconv.FormatUint(file.UncompressedSize64, 10))
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(entryName)+"\"")
		}
		_, _ = io.Copy(w, io.LimitReader(reader, 128<<20))
		return
	}
	writeError(w, http.StatusNotFound, "CBZ 条目不存在")
}

type epubReaderChapter struct {
	Title      string `json:"title"`
	HTML       string `json:"html,omitempty"`
	Href       string `json:"href,omitempty"`
	MediaType  string `json:"mediaType,omitempty"`
	Properties string `json:"properties,omitempty"`
}

// handleLocalEPUB lists the reading order of a downloaded EPUB so the local
// library can render it without extracting the archive to disk.
func (s *Server) handleLocalEPUB(w http.ResponseWriter, r *http.Request) {
	target, err := s.resolveLocalTarget(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	archive, err := zip.OpenReader(target)
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法读取 EPUB 文件: "+err.Error())
		return
	}
	defer archive.Close()

	packagePath, packageDocument, err := readEPUBPackageDocument(archive)
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法解析 EPUB 目录结构: "+err.Error())
		return
	}
	packageDir := path.Dir(packagePath)
	var packageXML struct {
		Manifest struct {
			Items []struct {
				ID         string `xml:"id,attr"`
				Href       string `xml:"href,attr"`
				MediaType  string `xml:"media-type,attr"`
				Properties string `xml:"properties,attr"`
			} `xml:"item"`
		} `xml:"manifest"`
		Spine struct {
			Items []struct {
				IDRef string `xml:"idref,attr"`
			} `xml:"itemref"`
		} `xml:"spine"`
	}
	if err := xml.Unmarshal(packageDocument, &packageXML); err != nil {
		writeError(w, http.StatusBadGateway, "无法解析 EPUB 包文档: "+err.Error())
		return
	}
	manifest := make(map[string]struct {
		href       string
		mediaType  string
		properties string
	}, len(packageXML.Manifest.Items))
	for _, item := range packageXML.Manifest.Items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		manifest[id] = struct {
			href       string
			mediaType  string
			properties string
		}{href: item.Href, mediaType: item.MediaType, properties: item.Properties}
	}

	chapters := make([]epubReaderChapter, 0, len(packageXML.Spine.Items))
	for _, reference := range packageXML.Spine.Items {
		id := strings.TrimSpace(reference.IDRef)
		item, ok := manifest[id]
		if !ok || strings.TrimSpace(item.href) == "" {
			continue
		}
		entryName, err := normalizeEPUBEntryPath(packageDir, item.href)
		if err != nil {
			continue
		}
		mediaType := strings.TrimSpace(item.mediaType)
		if mediaType == "" {
			mediaType = "application/xhtml+xml"
		}
		chapter := epubReaderChapter{
			Title:      id,
			Href:       entryName,
			MediaType:  mediaType,
			Properties: item.properties,
		}
		if !strings.Contains(mediaType, "xhtml") && !strings.Contains(mediaType, "html") {
			chapters = append(chapters, chapter)
			continue
		}
		raw, err := readEPUBEntry(archive, entryName)
		if err != nil {
			writeError(w, http.StatusBadGateway, "无法读取 EPUB 章节: "+err.Error())
			return
		}
		html, title := sanitizeEPUBChapterHTML(raw)
		if strings.TrimSpace(title) != "" {
			chapter.Title = title
		}
		chapter.HTML = html
		chapters = append(chapters, chapter)
	}
	if len(chapters) == 0 {
		writeError(w, http.StatusBadGateway, "EPUB 没有可阅读章节")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": chapters})
}

// handleLocalEPUBFile streams one asset from an EPUB. Image URLs inside the
// chapter HTML are rewritten to this endpoint before the reader receives them.
func (s *Server) handleLocalEPUBFile(w http.ResponseWriter, r *http.Request) {
	target, err := s.resolveLocalTarget(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	entryName := strings.TrimSpace(r.URL.Query().Get("entry"))
	if entryName == "" || strings.Contains(entryName, "..") {
		writeError(w, http.StatusBadRequest, "无效的 EPUB 条目")
		return
	}
	archive, err := zip.OpenReader(target)
	if err != nil {
		writeError(w, http.StatusBadGateway, "无法读取 EPUB 文件: "+err.Error())
		return
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name != entryName || file.FileInfo().IsDir() {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		defer reader.Close()
		contentType := strings.TrimSpace(mime.TypeByExtension(strings.ToLower(path.Ext(entryName))))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Header().Set("Content-Length", strconv.FormatUint(file.UncompressedSize64, 10))
		if r.URL.Query().Get("download") == "1" {
			w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(entryName)+"\"")
		}
		_, _ = io.Copy(w, io.LimitReader(reader, 128<<20))
		return
	}
	writeError(w, http.StatusNotFound, "EPUB 条目不存在")
}

func readEPUBPackageDocument(archive *zip.ReadCloser) (string, []byte, error) {
	container, err := readEPUBEntry(archive, "META-INF/container.xml")
	if err != nil {
		return "", nil, err
	}
	var payload struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(container, &payload); err != nil {
		return "", nil, err
	}
	packagePath := ""
	for _, rootfile := range payload.Rootfiles {
		if value := strings.TrimSpace(rootfile.FullPath); value != "" {
			packagePath = value
			break
		}
	}
	if packagePath == "" {
		return "", nil, errors.New("container.xml 缺少 rootfile")
	}
	packagePath = strings.TrimPrefix(path.Clean(strings.ReplaceAll(packagePath, "\\", "/")), "/")
	document, err := readEPUBEntry(archive, packagePath)
	if err != nil {
		return "", nil, err
	}
	return packagePath, document, nil
}

func readEPUBEntry(archive *zip.ReadCloser, name string) ([]byte, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" || strings.Contains(name, "..") {
		return nil, errors.New("无效的 EPUB 条目")
	}
	for _, file := range archive.File {
		if file.Name != name || file.FileInfo().IsDir() {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(io.LimitReader(reader, 64<<20))
	}
	return nil, fmt.Errorf("EPUB 条目不存在: %s", name)
}

func normalizeEPUBEntryPath(base, reference string) (string, error) {
	reference = strings.TrimSpace(strings.ReplaceAll(reference, "\\", "/"))
	if reference == "" || strings.ContainsAny(reference, "\x00") {
		return "", errors.New("无效的 EPUB 路径")
	}
	lower := strings.ToLower(reference)
	if strings.HasPrefix(lower, "http:") || strings.HasPrefix(lower, "https:") || strings.HasPrefix(lower, "data:") || strings.HasPrefix(reference, "//") {
		return "", errors.New("不支持外部 EPUB 资源")
	}
	if index := strings.IndexAny(reference, "?#"); index >= 0 {
		reference = reference[:index]
	}
	if reference == "" {
		return "", errors.New("无效的 EPUB 路径")
	}
	if strings.HasPrefix(reference, "/") {
		return path.Clean(strings.TrimPrefix(reference, "/")), nil
	}
	joined := path.Join(base, reference)
	cleaned := path.Clean(joined)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("EPUB 路径越界")
	}
	return cleaned, nil
}

func sanitizeEPUBChapterHTML(raw []byte) (string, string) {
	document, err := nethtml.Parse(strings.NewReader(string(raw)))
	if err != nil {
		return string(raw), ""
	}
	title := firstEPUBHeading(document)
	body := findHTMLNode(document, "body")
	if body == nil {
		body = document
	}
	for _, node := range epubHTMLNodes(body) {
		if node.Type != nethtml.ElementNode {
			continue
		}
		tag := strings.ToLower(node.Data)
		if tag == "script" || tag == "iframe" || tag == "object" || tag == "embed" || tag == "form" || tag == "head" || tag == "meta" || tag == "link" {
			node.Type = nethtml.CommentNode
			node.Data = ""
			node.Attr = nil
			node.FirstChild = nil
			node.LastChild = nil
			continue
		}
		attributes := node.Attr[:0]
		for _, attribute := range node.Attr {
			key := strings.ToLower(strings.TrimSpace(attribute.Key))
			if key == "" || strings.HasPrefix(key, "on") {
				continue
			}
			switch key {
			case "style":
				continue
			case "src", "href", "xlink:href":
				rewritten := rewriteEPUBHTMLReference(tag, key, attribute.Val)
				if rewritten == "" {
					continue
				}
				attribute.Val = rewritten
			}
			attributes = append(attributes, attribute)
		}
		node.Attr = attributes
	}
	var output strings.Builder
	if err := renderEPUBXHTMLNode(&output, body); err != nil {
		return string(raw), title
	}
	return output.String(), title
}

func rewriteEPUBHTMLReference(tag, key, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "javascript:") || strings.HasPrefix(lower, "vbscript:") || strings.HasPrefix(lower, "data:") {
		return ""
	}
	if strings.HasPrefix(lower, "http:") || strings.HasPrefix(lower, "https:") || strings.HasPrefix(lower, "mailto:") || strings.HasPrefix(value, "#") || strings.HasPrefix(value, "//") {
		return value
	}
	if key == "href" && tag != "image" && tag != "link" {
		// Chapter-to-chapter links cannot be resolved inside the reader modal;
		// keep them as plain text rather than exposing raw EPUB entries.
		return ""
	}
	name := value
	if index := strings.IndexAny(name, "#?"); index >= 0 {
		name = name[:index]
	}
	name = strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "/")
	if name == "" || strings.Contains(name, "..") {
		return ""
	}
	return "/api/local/epub/file?entry=" + url.QueryEscape(name)
}

func walkEPUBHTML(node *nethtml.Node, visit func(*nethtml.Node) error) error {
	if err := visit(node); err != nil {
		return err
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if err := walkEPUBHTML(child, visit); err != nil {
			return err
		}
	}
	return nil
}

func epubHTMLNodes(root *nethtml.Node) []*nethtml.Node {
	nodes := make([]*nethtml.Node, 0, 32)
	_ = walkEPUBHTML(root, func(node *nethtml.Node) error {
		nodes = append(nodes, node)
		return nil
	})
	return nodes
}

func findHTMLNode(node *nethtml.Node, name string) *nethtml.Node {
	if node.Type == nethtml.ElementNode && strings.EqualFold(node.Data, name) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findHTMLNode(child, name); found != nil {
			return found
		}
	}
	return nil
}

func renderEPUBXHTMLNode(output *strings.Builder, node *nethtml.Node) error {
	switch node.Type {
	case nethtml.TextNode:
		output.WriteString(htmlpkg.EscapeString(node.Data))
		return nil
	case nethtml.ElementNode:
		tag := strings.ToLower(node.Data)
		if tag == "script" || tag == "style" || tag == "head" || tag == "html" || tag == "body" {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if err := renderEPUBXHTMLNode(output, child); err != nil {
					return err
				}
			}
			return nil
		}
		output.WriteByte('<')
		output.WriteString(tag)
		for _, attribute := range node.Attr {
			key := strings.TrimSpace(attribute.Key)
			if key == "" || strings.HasPrefix(strings.ToLower(key), "on") {
				continue
			}
			output.WriteByte(' ')
			output.WriteString(key)
			output.WriteString(`="`)
			output.WriteString(htmlpkg.EscapeString(attribute.Val))
			output.WriteByte('"')
		}
		if isEPUBXHTMLVoidElement(tag) {
			output.WriteString("/>")
			return nil
		}
		output.WriteByte('>')
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := renderEPUBXHTMLNode(output, child); err != nil {
				return err
			}
		}
		output.WriteString("</")
		output.WriteString(tag)
		output.WriteByte('>')
		return nil
	default:
		return nil
	}
}

func isEPUBXHTMLVoidElement(tag string) bool {
	switch tag {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	default:
		return false
	}
}

func firstEPUBHeading(document *nethtml.Node) string {
	var title string
	_ = walkEPUBHTML(document, func(node *nethtml.Node) error {
		if node.Type != nethtml.ElementNode || title != "" {
			return nil
		}
		switch strings.ToLower(node.Data) {
		case "h1", "h2", "title":
			title = strings.Join(strings.Fields(epubNodeText(node)), " ")
		}
		return nil
	})
	return title
}

func epubNodeText(node *nethtml.Node) string {
	var output strings.Builder
	_ = walkEPUBHTML(node, func(current *nethtml.Node) error {
		if current.Type == nethtml.TextNode {
			output.WriteString(current.Data)
		}
		return nil
	})
	return output.String()
}

func (s *Server) handleLocalFile(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if rel == "" {
		writeError(w, http.StatusBadRequest, "缺少 path 参数")
		return
	}
	rootValue := s.cfg.DownloadDir
	if rawRoot, rawRel, ok := strings.Cut(rel, "|"); ok {
		rootValue = rawRoot
		rel = rawRel
	}
	allowed := false
	cleanedRoot := filepath.Clean(rootValue)
	for _, candidate := range s.localRoots(r.Context()) {
		if candidate == cleanedRoot {
			allowed = true
			break
		}
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "路径不在下载目录内")
		return
	}
	root, err := filepath.Abs(cleanedRoot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	target, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	within, err := filepath.Rel(root, target)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(os.PathSeparator)) {
		writeError(w, http.StatusForbidden, "路径不在下载目录内")
		return
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "文件不存在")
		return
	}
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(target)+"\"")
	}
	// 本地封面可以要缩略图（/?w=320），原图只在详情大图时下发。
	if width := requestedThumbWidth(r); width > 0 && s.serveThumbnail(w, r, target, width) {
		return
	}
	http.ServeFile(w, r, target)
}

func (s *Server) handleLocalMerge(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Paths []string `json:"paths"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	targets, err := s.resolveLocalDirectories(r.Context(), payload.Paths)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(targets) < 2 {
		writeError(w, http.StatusBadRequest, "合并至少需要选择两部作品")
		return
	}
	result, err := localops.Merge(targets[0], targets[1:])
	if err != nil {
		s.logger.Printf("local: merge %s: %v", filepath.Base(targets[0]), err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.logger.Printf("local: merged %d file(s) into %s", result.MovedFiles, filepath.Base(result.ParentDir))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"movedFiles": result.MovedFiles,
		"parent":     filepath.Base(result.ParentDir),
	})
}

func (s *Server) handleLocalSimplify(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Paths []string `json:"paths"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	targets, err := s.resolveLocalDirectories(r.Context(), payload.Paths)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	renamed := 0
	merged := 0
	for _, target := range targets {
		result, err := localops.ConvertToSimplified(target)
		if err != nil {
			s.logger.Printf("local: simplify %s: %v", filepath.Base(target), err)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		renamed += result.Renamed
		if result.MergedInto != "" {
			merged++
		}
	}
	s.logger.Printf("local: converted %d path(s), merged %d item(s)", renamed, merged)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"renamed": renamed,
		"merged":  merged,
	})
}

func (s *Server) handleLocalDelete(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Paths []string `json:"paths"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	targets, err := s.resolveLocalDirectories(r.Context(), payload.Paths)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateLocalDeleteTargets(r.Context(), targets); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := localops.Delete(targets)
	if err != nil {
		s.logger.Printf("local: delete: %v", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.logger.Printf("local: deleted %d item(s), %d file(s)", result.DeletedItems, result.DeletedFiles)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"deletedItems": result.DeletedItems,
		"deletedFiles": result.DeletedFiles,
	})
}

func (s *Server) validateLocalDeleteTargets(ctx context.Context, targets []string) error {
	roots := s.localRoots(ctx)
	for _, target := range targets {
		info, err := os.Lstat(target)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("本地作品目录无效: %s", filepath.Base(target))
		}
		targetAbs, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		matched := false
		for _, root := range roots {
			rootAbs, err := filepath.Abs(root)
			if err != nil || sameCleanPath(rootAbs, targetAbs) {
				continue
			}
			rel, err := filepath.Rel(rootAbs, targetAbs)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				continue
			}
			// The local library only exposes direct children of a download root.
			if !strings.Contains(rel, string(os.PathSeparator)) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("只能删除下载目录中的作品目录: %s", filepath.Base(target))
		}
	}
	return nil
}

func sameCleanPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func (s *Server) resolveLocalDirectories(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, errors.New("请选择本地作品")
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(paths))
	for _, value := range paths {
		target, err := s.resolveLocalTarget(ctx, value)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(target)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("本地作品目录不存在: %s", filepath.Base(target))
		}
		clean := filepath.Clean(target)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		result = append(result, clean)
	}
	return result, nil
}

func (s *Server) settingsResponse(ctx context.Context, username string) map[string]any {
	current := s.loadSettings(ctx)
	return map[string]any{
		"username":              username,
		"repoUrl":               current.RepoURL,
		"downloadDir":           s.cfg.DownloadDir,
		"dataDir":               s.cfg.DataDir,
		"scanInterval":          current.ScanInterval.String(),
		"maxChapterConcurrency": current.MaxChapterConcurrency,
		"maxPageConcurrency":    current.MaxPageConcurrency,
		"proxy":                 current.Proxy,
		"proxyUsername":         current.ProxyUsername,
		"proxyPassword":         current.ProxyPassword,
		"cookieSecure":          current.CookieSecure,
		"sessionTtlDays":        current.SessionTTLDays,
		"sourceConcurrency":     current.SourceConcurrency,
		"batchSize":             current.BatchSize,
		"batchIntervalMinutes":  current.BatchIntervalMinutes,
		"convertToSimplified":   current.ConvertToSimplified,
		"staleDays":             current.StaleDays,
		"logMaxEntries":         current.LogMaxEntries,
		"logRetentionDays":      current.LogRetentionDays,
		"telegramEnabled":       current.TelegramEnabled,
		"telegramToken":         current.TelegramToken,
		"telegramChatIds":       current.TelegramChatIDs,
	}
}

// reloadTelegram applies persisted Telegram settings to the bot service.
func (s *Server) reloadTelegram(ctx context.Context) {
	if s.telegram == nil {
		return
	}
	current := s.loadSettings(ctx)
	s.telegram.Configure(telegram.Config{
		Enabled: current.TelegramEnabled,
		Token:   current.TelegramToken,
		ChatIDs: current.TelegramChatIDs,
	})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	username := ""
	if user, ok := userFromContext(r.Context()); ok {
		username = user.Username
	}
	writeJSON(w, http.StatusOK, s.settingsResponse(r.Context(), username))
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		RepoURL               string         `json:"repoUrl"`
		ScanInterval          string         `json:"scanInterval"`
		MaxChapterConcurrency int            `json:"maxChapterConcurrency"`
		MaxPageConcurrency    int            `json:"maxPageConcurrency"`
		ProxyUsername         *string        `json:"proxyUsername"`
		ProxyPassword         *string        `json:"proxyPassword"`
		SessionTTLDays        *int           `json:"sessionTtlDays"`
		SourceConcurrency     map[string]int `json:"sourceConcurrency"`
		BatchSize             *int           `json:"batchSize"`
		BatchIntervalMinutes  *int           `json:"batchIntervalMinutes"`
		ConvertToSimplified   *bool          `json:"convertToSimplified"`
		StaleDays             map[string]int `json:"staleDays"`
		LogMaxEntries         *int           `json:"logMaxEntries"`
		LogRetentionDays      *int           `json:"logRetentionDays"`
		Proxy                 *string        `json:"proxy"`
		CookieSecure          bool           `json:"cookieSecure"`
		Username              string         `json:"username"`
		CurrentPassword       string         `json:"currentPassword"`
		NewPassword           string         `json:"newPassword"`
		TelegramEnabled       *bool          `json:"telegramEnabled"`
		TelegramToken         *string        `json:"telegramToken"`
		TelegramChatIDs       []string       `json:"telegramChatIds"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	currentUser, ok := userFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "无法识别当前用户")
		return
	}
	responseUsername := currentUser.Username
	username := strings.TrimSpace(payload.Username)
	if username != "" && username != currentUser.Username {
		if len(username) < 3 || len(username) > 64 {
			writeError(w, http.StatusBadRequest, "用户名长度需在 3 到 64 个字符之间")
			return
		}
		existing, err := s.store.UserByUsername(r.Context(), username)
		if err == nil && existing.ID != currentUser.ID {
			writeError(w, http.StatusConflict, "用户名已存在")
			return
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	newPasswordHash := ""
	if payload.NewPassword != "" {
		if len(payload.NewPassword) < 8 {
			writeError(w, http.StatusBadRequest, "新密码至少需要 8 个字符")
			return
		}
		if payload.CurrentPassword != "" &&
			bcrypt.CompareHashAndPassword([]byte(currentUser.PasswordHash), []byte(payload.CurrentPassword)) != nil {
			writeError(w, http.StatusUnauthorized, "当前密码不正确")
			return
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(payload.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		newPasswordHash = string(hash)
	}

	if strings.TrimSpace(payload.RepoURL) != "" {
		if _, err := url.ParseRequestURI(strings.TrimSpace(payload.RepoURL)); err != nil {
			writeError(w, http.StatusBadRequest, "拓展仓库地址必须是有效的 URL")
			return
		}
		if err := s.store.SetSetting(r.Context(), "source_repo", strings.TrimSpace(payload.RepoURL)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if strings.TrimSpace(payload.ScanInterval) != "" {
		interval, err := time.ParseDuration(strings.TrimSpace(payload.ScanInterval))
		if err != nil || interval < time.Minute || interval > 24*time.Hour {
			writeError(w, http.StatusBadRequest, "订阅扫描间隔必须在 1m 到 24h 之间，例如 30m")
			return
		}
		if err := s.store.SetSetting(r.Context(), "scan_interval", interval.String()); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.scheduler.SetInterval(interval)
	}

	if payload.MaxChapterConcurrency != 0 {
		if payload.MaxChapterConcurrency < 1 || payload.MaxChapterConcurrency > maxChapterConcurrencyLimit {
			writeError(w, http.StatusBadRequest, "章节并发必须在 1 到 32 之间")
			return
		}
		if err := s.store.SetSetting(r.Context(), "max_chapter_concurrency", strconv.Itoa(payload.MaxChapterConcurrency)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if payload.MaxPageConcurrency != 0 {
		if payload.MaxPageConcurrency < 1 || payload.MaxPageConcurrency > maxPageConcurrencyLimit {
			writeError(w, http.StatusBadRequest, "图片并发必须在 1 到 32 之间")
			return
		}
		if err := s.store.SetSetting(r.Context(), "max_page_concurrency", strconv.Itoa(payload.MaxPageConcurrency)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if payload.Proxy != nil {
		current := s.loadSettings(r.Context())
		rawProxy := strings.TrimSpace(*payload.Proxy)
		if err := source.SetHTTPClientProxyCredentials(s.registry.Client(), rawProxy, current.ProxyUsername, current.ProxyPassword); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.store.SetSetting(r.Context(), "proxy", rawProxy); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if err := s.store.SetSetting(r.Context(), "cookie_secure", strconv.FormatBool(payload.CookieSecure)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if username != "" && username != currentUser.Username {
		if err := s.store.UpdateUsername(r.Context(), currentUser.ID, username); err != nil {
			if errors.Is(err, store.ErrUsernameTaken) {
				writeError(w, http.StatusConflict, "用户名已存在")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		responseUsername = username
	}
	if newPasswordHash != "" {
		if err := s.store.UpdateUserPassword(r.Context(), currentUser.ID, newPasswordHash); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if payload.SessionTTLDays != nil {
		if *payload.SessionTTLDays < 1 || *payload.SessionTTLDays > 3650 {
			writeError(w, http.StatusBadRequest, "会话存活期必须在 1 到 3650 天之间")
			return
		}
		if err := s.store.SetSetting(r.Context(), "session_ttl_days", strconv.Itoa(*payload.SessionTTLDays)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if payload.SourceConcurrency != nil {
		normalized := map[string]int{}
		total := 0
		for sourceID, limit := range payload.SourceConcurrency {
			if !isManagedSource(sourceID) {
				continue
			}
			if limit < 1 || limit > 8 {
				writeError(w, http.StatusBadRequest, "单个漫画源下载线程必须在 1 到 8 之间")
				return
			}
			normalized[sourceID] = limit
			total += limit
		}
		if total < 1 || total > 24 {
			writeError(w, http.StatusBadRequest, "资源源的总下载线程必须在 1 到 24 之间")
			return
		}
		raw, _ := json.Marshal(normalized)
		if err := s.store.SetSetting(r.Context(), "source_concurrency", string(raw)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if payload.BatchSize != nil {
		if *payload.BatchSize < 0 || *payload.BatchSize > 1000 {
			writeError(w, http.StatusBadRequest, "批量下载文件数必须在 0 到 1000 之间")
			return
		}
		if err := s.store.SetSetting(r.Context(), "batch_size", strconv.Itoa(*payload.BatchSize)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if payload.BatchIntervalMinutes != nil {
		if *payload.BatchIntervalMinutes < 0 || *payload.BatchIntervalMinutes > 10080 {
			writeError(w, http.StatusBadRequest, "下载间隔分钟数必须在 0 到 10080 之间")
			return
		}
		if err := s.store.SetSetting(r.Context(), "batch_interval_minutes", strconv.Itoa(*payload.BatchIntervalMinutes)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if payload.ConvertToSimplified != nil {
		if err := s.store.SetSetting(r.Context(), "convert_to_simplified", strconv.FormatBool(*payload.ConvertToSimplified)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if payload.LogMaxEntries != nil {
		if *payload.LogMaxEntries < 100 || *payload.LogMaxEntries > 200000 {
			writeError(w, http.StatusBadRequest, "日志条数必须在 100 到 200000 之间")
			return
		}
		if err := s.store.SetSetting(r.Context(), "log_max_entries", strconv.Itoa(*payload.LogMaxEntries)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if s.logs != nil {
			s.logs.SetMax(*payload.LogMaxEntries)
		}
	}
	if payload.LogRetentionDays != nil {
		if *payload.LogRetentionDays < 0 || *payload.LogRetentionDays > 3650 {
			writeError(w, http.StatusBadRequest, "日志保留天数必须在 0 到 3650 之间")
			return
		}
		if err := s.store.SetSetting(r.Context(), "log_retention_days", strconv.Itoa(*payload.LogRetentionDays)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if s.logs != nil {
			s.logs.SetFileSink(filepath.Join(s.cfg.DataDir, "logs"), *payload.LogRetentionDays)
		}
	}
	if payload.StaleDays != nil {
		normalized := map[string]int{}
		for sourceID, days := range payload.StaleDays {
			if sourceID != "*" && !isManagedSource(sourceID) {
				continue
			}
			if days < 0 || days > 3650 {
				writeError(w, http.StatusBadRequest, "未更新天数必须在 0 到 3650 之间")
				return
			}
			normalized[sourceID] = days
		}
		raw, _ := json.Marshal(normalized)
		if err := s.store.SetSetting(r.Context(), "stale_days", string(raw)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if payload.TelegramEnabled != nil {
		if err := s.store.SetSetting(r.Context(), "telegram_enabled", strconv.FormatBool(*payload.TelegramEnabled)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if payload.TelegramToken != nil {
		if err := s.store.SetSetting(r.Context(), "telegram_token", strings.TrimSpace(*payload.TelegramToken)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if payload.TelegramChatIDs != nil {
		normalized := make([]string, 0, len(payload.TelegramChatIDs))
		seen := map[string]bool{}
		for _, value := range payload.TelegramChatIDs {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			normalized = append(normalized, value)
		}
		raw, _ := json.Marshal(normalized)
		if err := s.store.SetSetting(r.Context(), "telegram_chats", string(raw)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.reloadTelegram(r.Context())
	if payload.ProxyUsername != nil || payload.ProxyPassword != nil {
		current := s.loadSettings(r.Context())
		proxyUsername, proxyPassword := current.ProxyUsername, current.ProxyPassword
		if payload.ProxyUsername != nil {
			proxyUsername = strings.TrimSpace(*payload.ProxyUsername)
		}
		if payload.ProxyPassword != nil {
			proxyPassword = *payload.ProxyPassword
		}
		if err := source.SetHTTPClientProxyCredentials(s.registry.Client(), current.Proxy, proxyUsername, proxyPassword); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.store.SetSetting(r.Context(), "proxy_username", proxyUsername); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.store.SetSetting(r.Context(), "proxy_password", proxyPassword); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.setProxyCredentials(r.Context(), payload.Proxy, payload.ProxyUsername, payload.ProxyPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	current := s.loadSettings(r.Context())
	s.engine.SetConcurrency(current.MaxChapterConcurrency, current.MaxPageConcurrency)
	s.engine.SetSourceConcurrency(current.SourceConcurrency)
	s.engine.SetDownloadPolicy(current.BatchSize, current.BatchIntervalMinutes, current.ConvertToSimplified)
	response := s.settingsResponse(r.Context(), responseUsername)
	response["ok"] = true
	_ = s.configs.PersistAccounts(r.Context(), s.store)
	_ = s.configs.PersistSettings(r.Context(), s.store)
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleDashboardUpcoming(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "limit", 3)
	if limit <= 0 {
		limit = 3
	}
	if limit > 20 {
		limit = 20
	}
	subscriptions, err := s.store.ListSubscriptions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type upcomingItem struct {
		ID          int64     `json:"id"`
		SourceID    string    `json:"sourceId"`
		ComicID     string    `json:"comicId"`
		Title       string    `json:"title"`
		Cover       string    `json:"cover,omitempty"`
		LocalCover  string    `json:"localCover,omitempty"`
		Author      string    `json:"author,omitempty"`
		Description string    `json:"description,omitempty"`
		CoverBlur   bool      `json:"coverBlur,omitempty"`
		CronExpr    string    `json:"cronExpr"`
		NextRun     time.Time `json:"nextRun"`
	}
	now := time.Now()
	type candidate struct {
		subscription model.Subscription
		next         time.Time
	}
	candidates := make([]candidate, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		if !subscription.Enabled || strings.TrimSpace(subscription.CronExpr) == "" {
			continue
		}
		next, err := cronutil.Next(subscription.CronExpr, now)
		if err != nil {
			continue
		}
		candidates = append(candidates, candidate{subscription: subscription, next: next})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].next.Equal(candidates[j].next) {
			return candidates[i].subscription.ID < candidates[j].subscription.ID
		}
		return candidates[i].next.Before(candidates[j].next)
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	items := make([]upcomingItem, 0, len(candidates))
	for _, candidate := range candidates {
		item := s.subscriptionWithCovers(r.Context(), candidate.subscription)
		if s.subscriptionCovers != nil {
			if _, indexed := s.subscriptionCovers.Lookup(item.SourceID, item.ComicID); !indexed {
				go s.materializeSubscriptionCover(candidate.subscription)
			}
		}
		author, description := s.subscriptionLocalMetadata(item)
		if strings.TrimSpace(item.Author) == "" {
			item.Author = author
		}
		if strings.TrimSpace(description) == "" && s.discover != nil {
			if adapter, err := s.registry.Get(item.SourceID); err == nil {
				kind := source.KindOf(adapter.Info())
				if cached, ok := s.discover.Get(item.SourceID, kind, item.ComicID); ok {
					description = strings.TrimSpace(cached.Comic.Description)
				}
			}
		}
		items = append(items, upcomingItem{
			ID:          item.ID,
			SourceID:    item.SourceID,
			ComicID:     item.ComicID,
			Title:       item.Title,
			Cover:       item.Cover,
			LocalCover:  item.LocalCover,
			Author:      strings.TrimSpace(item.Author),
			Description: strings.TrimSpace(description),
			CoverBlur:   item.CoverBlur,
			CronExpr:    item.CronExpr,
			NextRun:     candidate.next,
		})
	}
	s.flushSubscriptionCoverIndex()
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) materializeSubscriptionCover(subscription model.Subscription) {
	if s.subscriptionCovers == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var materialize subscriptioncover.Materializer
	client := http.DefaultClient
	if s.registry != nil {
		client = s.registry.Client()
		if adapter, err := s.registry.Get(subscription.SourceID); err == nil {
			if imageSource, ok := adapter.(source.ImageSource); ok {
				account, accountErr := s.registry.Account(ctx, subscription.SourceID)
				if accountErr == nil {
					materialize = func(ctx context.Context, comic model.Comic) ([]byte, string, error) {
						select {
						case pluginCoverFetchSlots <- struct{}{}:
							defer func() { <-pluginCoverFetchSlots }()
						case <-ctx.Done():
							return nil, "", ctx.Err()
						}
						return imageSource.CoverImage(ctx, account, comic)
					}
				}
			}
		}
	}
	if _, err := s.subscriptionCovers.Materialize(ctx, client, subscription, materialize); err != nil {
		s.logger.Printf("api: materialize subscription cover %s/%s: %v", subscription.SourceID, subscription.ComicID, err)
		return
	}
	s.flushSubscriptionCoverIndex()
}

func (s *Server) subscriptionLocalMetadata(subscription model.Subscription) (string, string) {
	root, err := s.cleanDownloadDir(subscription.DownloadDir)
	if err != nil {
		return "", ""
	}
	series := strings.TrimSpace(subscription.SeriesDir)
	if series == "" {
		series = downloader.SeriesDirectoryName(subscription.Title, subscription.ConvertToSimplified)
	}
	folder := filepath.Join(root, downloader.SafeName(series))
	item := localItemEntry{}
	for _, name := range []string{"series.json", "ComicInfo.xml", "tvshow.nfo"} {
		path := filepath.Join(folder, name)
		switch name {
		case "series.json":
			applySeriesMeta(path, &item)
		case "ComicInfo.xml":
			applyComicMeta(path, &item)
		case "tvshow.nfo":
			applyOtherNFO(path, &item)
		}
		if strings.TrimSpace(item.Author) != "" && strings.TrimSpace(item.Description) != "" {
			break
		}
	}
	return strings.TrimSpace(item.Author), strings.TrimSpace(item.Description)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit := 300
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200000 {
			limit = parsed
		}
	}
	lines := []string{}
	if s.logs != nil {
		lines = s.logs.Lines(limit)
	}
	items := make([]logEntry, 0, len(lines))
	for _, line := range lines {
		items = append(items, parseLogEntry(line))
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "items": items})
}

type logEntry struct {
	Time    string `json:"time,omitempty"`
	Level   string `json:"level"`
	Module  string `json:"module"`
	Message string `json:"message"`
	Raw     string `json:"raw"`
}

func parseLogEntry(line string) logEntry {
	entry := logEntry{Level: "info", Message: line, Raw: line}
	parts := strings.SplitN(strings.TrimSpace(line), " ", 3)
	if len(parts) == 3 && len(parts[0]) == 10 && strings.Count(parts[0], "/") == 2 {
		entry.Time = parts[0] + " " + parts[1]
		entry.Message = parts[2]
	}
	lower := strings.ToLower(entry.Message)
	if strings.Contains(lower, "failed") || strings.Contains(lower, "error") || strings.Contains(lower, "失败") || strings.Contains(lower, "无法") {
		entry.Level = "error"
	} else if strings.Contains(lower, "warn") || strings.Contains(lower, "retry") || strings.Contains(lower, "重试") {
		entry.Level = "warning"
	} else if strings.Contains(lower, "debug") || strings.Contains(lower, "verbose") || strings.Contains(lower, "调试") {
		entry.Level = "debug"
	}
	entry.Module = inferLogModule(lower)
	return entry
}

func inferLogModule(message string) string {
	if strings.HasPrefix(message, "manco:") || strings.HasPrefix(message, "manco listening") {
		return "system"
	}
	switch {
	case strings.Contains(message, "downloader:") || strings.Contains(message, "download job") || strings.Contains(message, "下载任务") || strings.Contains(message, "下载失败"):
		return "downloads"
	case strings.Contains(message, "scheduler:") || strings.Contains(message, "subscription") || strings.Contains(message, "订阅"):
		return "subscriptions"
	case strings.Contains(message, "repository") || strings.Contains(message, "repo ") || strings.Contains(message, "manifest") || strings.Contains(message, "拓展仓库") || strings.Contains(message, "仓库"):
		return "repositories"
	case strings.Contains(message, "local:") || strings.Contains(message, "cbz") || strings.Contains(message, "本地") || strings.Contains(message, "merge") || strings.Contains(message, "合并"):
		return "local"
	case strings.Contains(message, "auth") || strings.Contains(message, "login") || strings.Contains(message, "session") || strings.Contains(message, "账号") || strings.Contains(message, "登录") || strings.Contains(message, "用户"):
		return "accounts"
	case strings.Contains(message, "setting") || strings.Contains(message, "proxy") || strings.Contains(message, "配置") || strings.Contains(message, "设置"):
		return "settings"
	case strings.Contains(message, "source") || strings.Contains(message, "discover") || strings.Contains(message, "browse") || strings.Contains(message, "search") || strings.Contains(message, "资源") || strings.Contains(message, "搜索"):
		return "sources"
	default:
		return "system"
	}
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
	imageAccount := source.Account{}
	if sourceID != "" {
		if account, err := s.registry.Account(r.Context(), sourceID); err == nil {
			imageAccount = account
			if account.Cookie != "" {
				request.Header.Set("Cookie", account.Cookie)
			}
		}
	}
	response, err := s.registry.ClientForAccount(imageAccount).Do(request)
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
	// 列表封面按缩略图下发：封面原图常常几千像素，解码成本远高于网络。
	if width := requestedThumbWidth(r); width > 0 {
		if data, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20)); readErr == nil {
			if thumb, thumbErr := buildThumbnailFromReader(bytes.NewReader(data), width); thumbErr == nil {
				writeThumbBytes(w, r, thumb)
				return
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Cache-Control", "public, max-age=3600")
			_, _ = w.Write(data)
			return
		}
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

func (s *Server) proxySetting(ctx context.Context) string {
	value, _ := s.store.Setting(ctx, "proxy")
	return strings.TrimSpace(value)
}

func (s *Server) cookieSecure(ctx context.Context) bool {
	if value, err := s.store.Setting(ctx, "cookie_secure"); err == nil && value != "" {
		return value == "true"
	}
	return s.cfg.CookieSecure
}

func (s *Server) setProxy(raw string) error {
	return source.SetHTTPClientProxy(s.registry.Client(), raw)
}

// setProxyCredentials applies proxy URL, username and password in one step.
func (s *Server) setProxyCredentials(ctx context.Context, raw, username *string, password *string) error {
	if raw == nil && username == nil && password == nil {
		return nil
	}
	current := s.loadSettings(ctx)
	proxyURL, proxyUsername, proxyPassword := current.Proxy, current.ProxyUsername, current.ProxyPassword
	if raw != nil {
		proxyURL = strings.TrimSpace(*raw)
	}
	if username != nil {
		proxyUsername = strings.TrimSpace(*username)
	}
	if password != nil {
		proxyPassword = *password
	}
	if err := source.SetHTTPClientProxyCredentials(s.registry.Client(), proxyURL, proxyUsername, proxyPassword); err != nil {
		return err
	}
	for key, value := range map[string]string{"proxy": proxyURL, "proxy_username": proxyUsername, "proxy_password": proxyPassword} {
		if err := s.store.SetSetting(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) sessionTTL(ctx context.Context) time.Duration {
	current := s.loadSettings(ctx)
	if current.SessionTTLDays < 1 {
		return sessionTTL
	}
	return time.Duration(current.SessionTTLDays) * 24 * time.Hour
}

// RunSubscriptionMaintenance applies stale/completed/disabled subscription
// rules on a schedule in the background.
func (s *Server) RunSubscriptionMaintenance(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	run := func() {
		current := s.loadSettings(ctx)
		if err := s.store.MaintainSubscriptions(ctx, current.StaleDays, time.Now()); err != nil {
			s.logger.Printf("maintenance: subscriptions: %v", err)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func isManagedSource(sourceID string) bool {
	for _, managed := range managedSourceIDs {
		if managed == sourceID {
			return true
		}
	}
	return false
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

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

func writeCachedJSON(w http.ResponseWriter, raw json.RawMessage, storedAt time.Time) {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		writeError(w, http.StatusInternalServerError, "缓存数据损坏")
		return
	}
	if object, ok := payload.(map[string]any); ok {
		object["cached"] = true
		object["cachedAt"] = storedAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, payload)
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
	if absoluteRoot == string(filepath.Separator) || target == absoluteRoot || !strings.HasPrefix(target, absoluteRoot+string(filepath.Separator)) {
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

func (s *Server) fetchGitHubAtom(ctx context.Context, endpoint string, latest, releaseURL *string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/atom+xml, application/xml;q=0.9, text/xml;q=0.8")
	request.Header.Set("User-Agent", "Manco/"+appVersion)
	response, err := s.registry.Client().Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return response.StatusCode, fmt.Errorf("GitHub 更新服务返回 HTTP %d", response.StatusCode)
	}
	var feed struct {
		Entries []struct {
			ID    string `xml:"id"`
			Links []struct {
				Rel  string `xml:"rel,attr"`
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&feed); err != nil {
		return 0, err
	}
	for _, entry := range feed.Entries {
		version := strings.TrimSpace(entry.ID)
		if slash := strings.LastIndex(version, "/"); slash >= 0 {
			version = version[slash+1:]
		}
		if decoded, err := url.PathUnescape(version); err == nil {
			version = decoded
		}
		version = strings.TrimSpace(version)
		if version == "" {
			continue
		}
		*latest = version
		for _, link := range entry.Links {
			if link.Rel == "alternate" && strings.TrimSpace(link.Href) != "" {
				*releaseURL = strings.TrimSpace(link.Href)
				break
			}
		}
		if *releaseURL == "" {
			*releaseURL = "https://github.com/Hyaeve/manco/releases/tag/" + url.PathEscape(version)
		}
		return http.StatusOK, nil
	}
	return http.StatusOK, nil
}
