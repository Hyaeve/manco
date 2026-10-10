// Package api exposes the Manco HTTP API and serves the embedded Vue frontend.
package api

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
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
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/hyaeve/manco/internal/config"
	"github.com/hyaeve/manco/internal/configstore"
	"github.com/hyaeve/manco/internal/cronutil"
	"github.com/hyaeve/manco/internal/diskcache"
	"github.com/hyaeve/manco/internal/downloader"
	"github.com/hyaeve/manco/internal/iconcache"
	"github.com/hyaeve/manco/internal/logbuf"
	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/repository"
	"github.com/hyaeve/manco/internal/scheduler"
	"github.com/hyaeve/manco/internal/secret"
	"github.com/hyaeve/manco/internal/source"
	"github.com/hyaeve/manco/internal/source/generic"
	"github.com/hyaeve/manco/internal/sources"
	"github.com/hyaeve/manco/internal/store"
)

const (
	sessionCookie = "manco_session"
	sessionTTL    = 30 * 24 * time.Hour
	appVersion    = "v0.1.4"
)

var managedSourceIDs = []string{"picacg", "jmcomic", "baozimh", "biquge"}

var sourceIconSources = map[string]string{
	"picacg":  "https://bica.mom/favicon.jpeg",
	"jmcomic": "https://jmcomicapp.net/favicon.ico",
	"baozimh": "https://www.webmota.com/favicon.ico",
	"biquge":  "https://www.biquge345.com/favicon.ico",
}

var sourceIconFallbacks = map[string]string{
	"picacg":  "/source-icons/picacg.png",
	"jmcomic": "/source-icons/jmcomic.png",
	"baozimh": "/source-icons/baozimh.png",
	"biquge":  "/source-icons/biquge345.ico",
}

type Server struct {
	cfg       config.Config
	store     *store.Store
	configs   *configstore.Files
	cache     *diskcache.Cache
	box       *secret.Box
	registry  *sources.Registry
	engine    *downloader.Engine
	icons     *iconcache.Cache
	scheduler *scheduler.Scheduler
	logger    *log.Logger
	logs      *logbuf.Buffer
	assets    fs.FS
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
}

type Options struct {
	Config    config.Config
	Store     *store.Store
	Configs   *configstore.Files
	Cache     *diskcache.Cache
	Box       *secret.Box
	Registry  *sources.Registry
	Engine    *downloader.Engine
	Icons     *iconcache.Cache
	Scheduler *scheduler.Scheduler
	Logger    *log.Logger
	Logs      *logbuf.Buffer
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
		configs:   options.Configs,
		cache:     options.Cache,
		box:       options.Box,
		registry:  options.Registry,
		engine:    options.Engine,
		icons:     options.Icons,
		scheduler: options.Scheduler,
		logger:    logger,
		logs:      options.Logs,
		assets:    options.Assets,
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
		CookieSecure:          s.cfg.CookieSecure,
		SessionTTLDays:        30,
		SourceConcurrency: map[string]int{
			"picacg":  s.cfg.MaxChapterConcurrency,
			"jmcomic": s.cfg.MaxChapterConcurrency,
			"baozimh": s.cfg.MaxChapterConcurrency,
			"biquge":  s.cfg.MaxChapterConcurrency,
		},
		StaleDays: map[string]int{
			"picacg":  0,
			"jmcomic": 0,
			"baozimh": 0,
			"biquge":  0,
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
				if limit >= 1 && limit <= 8 {
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
	mux.HandleFunc("GET /api/icons/{id}", s.requireAuth(s.handleSourceIcon))
	mux.HandleFunc("GET /api/source-repo", s.requireAuth(s.handleSourceRepo))
	mux.HandleFunc("GET /api/repositories", s.requireAuth(s.handleListRepositories))
	mux.HandleFunc("POST /api/repositories", s.requireAuth(s.handleCreateRepository))
	mux.HandleFunc("POST /api/repositories/{id}/sync", s.requireAuth(s.handleSyncRepository))
	mux.HandleFunc("DELETE /api/repositories/{id}", s.requireAuth(s.handleDeleteRepository))
	mux.HandleFunc("POST /api/repositories/{id}/extensions/{extensionId}", s.requireAuth(s.handleImportRepositoryExtension))
	mux.HandleFunc("POST /api/custom-sources", s.requireAuth(s.handleCreateCustomSource))
	mux.HandleFunc("GET /api/custom-sources/{id}", s.requireAuth(s.handleGetCustomSource))
	mux.HandleFunc("PUT /api/custom-sources/{id}", s.requireAuth(s.handleUpdateCustomSource))
	mux.HandleFunc("DELETE /api/custom-sources/{id}", s.requireAuth(s.handleDeleteCustomSource))
	mux.HandleFunc("PATCH /api/sources/{id}", s.requireAuth(s.handleUpdateSource))
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
	mux.HandleFunc("POST /api/subscriptions/{id}/download", s.requireAuth(s.handleDownloadSubscription))
	mux.HandleFunc("POST /api/subscriptions/{id}/archive", s.requireAuth(s.handleArchiveSubscription))

	mux.HandleFunc("GET /api/downloads", s.requireAuth(s.handleListDownloads))
	mux.HandleFunc("POST /api/downloads", s.requireAuth(s.handleCreateDownload))
	mux.HandleFunc("POST /api/downloads/{id}/retry", s.requireAuth(s.handleRetryDownload))
	mux.HandleFunc("DELETE /api/downloads/{id}", s.requireAuth(s.handleDeleteDownload))

	mux.HandleFunc("GET /api/library", s.requireAuth(s.handleLibrary))
	mux.HandleFunc("GET /api/local", s.requireAuth(s.handleLocalLibrary))
	mux.HandleFunc("GET /api/local/file", s.requireAuth(s.handleLocalFile))
	mux.HandleFunc("GET /api/local/cbz", s.requireAuth(s.handleLocalCBZ))
	mux.HandleFunc("GET /api/local/cbz/file", s.requireAuth(s.handleLocalCBZFile))
	mux.HandleFunc("GET /api/settings", s.requireAuth(s.handleGetSettings))
	mux.HandleFunc("GET /api/version", s.requireAuth(s.handleVersion))
	mux.HandleFunc("GET /api/update-check", s.requireAuth(s.handleUpdateCheck))
	mux.HandleFunc("PUT /api/settings", s.requireAuth(s.handlePutSettings))
	mux.HandleFunc("GET /api/stats", s.requireAuth(s.handleStats))
	mux.HandleFunc("GET /api/logs", s.requireAuth(s.handleLogs))
	mux.HandleFunc("GET /api/activity", s.requireAuth(s.handleActivity))
	mux.HandleFunc("GET /api/download-directories", s.requireAuth(s.handleDownloadDirectories))
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
		Settings  model.SourceDownloadSettings `json:"settings"`
		SourceID  string                       `json:"sourceId"`
		Username  string                       `json:"username,omitempty"`
		Password  string                       `json:"password,omitempty"`
		HomeURL   string                       `json:"homeUrl,omitempty"`
		HasToken  bool                         `json:"hasToken"`
		HasCookie bool                         `json:"hasCookie"`
		UpdatedAt time.Time                    `json:"updatedAt"`
	}
	accounts := map[string]accountView{}
	stored, err := s.store.ListSourceAccounts(r.Context())
	if err == nil {
		for _, account := range stored {
			plainPassword, _ := s.box.Decrypt(account.SecretCipher)
			accounts[account.SourceID] = accountView{
				Settings:  s.sourceDownloadSettings(account.Extra),
				SourceID:  account.SourceID,
				Username:  account.Username,
				Password:  plainPassword,
				HomeURL:   account.HomeURL,
				HasToken:  account.TokenCipher != "",
				HasCookie: account.CookieCipher != "",
				UpdatedAt: account.UpdatedAt,
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
	sourceURL, ok := sourceIconSources[id]
	if !ok || s.icons == nil {
		http.NotFound(w, r)
		return
	}
	if reader, _, err := s.icons.Open(id); err == nil {
		_ = reader.Close()
		file, entry, err := s.icons.Open(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		http.ServeContent(w, r, entry.File, entry.UpdatedAt, file)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := s.icons.Fetch(ctx, id, sourceURL); err != nil {
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

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": appVersion})
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var latest, releaseURL string
	releaseState, releaseErr := s.fetchGitHubUpdate(ctx, "https://api.github.com/repos/Hyaeve/manco/releases/latest", true, &latest, &releaseURL)
	if releaseState != http.StatusOK {
		tagState, tagErr := s.fetchGitHubUpdate(ctx, "https://api.github.com/repos/Hyaeve/manco/tags?per_page=1", false, &latest, &releaseURL)
		if tagState != http.StatusOK {
			message := "GitHub 更新服务不可用"
			if releaseErr != nil {
				message = releaseErr.Error()
			} else if tagErr != nil {
				message = tagErr.Error()
			}
			writeError(w, http.StatusBadGateway, message)
			return
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
		ChapterConcurrency: s.cfg.MaxChapterConcurrency,
		PageConcurrency:    s.cfg.MaxPageConcurrency,
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
	for index := range items {
		if _, ok := sourceIconSources[items[index].ID]; ok && s.icons != nil {
			items[index].Icon = s.icons.URL(items[index].ID)
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
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = "拓展仓库"
	}
	item := model.ExtensionRepository{
		ID:          newConfigID("repo", name+payload.URL),
		Name:        name,
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
	items, err := repository.Sync(ctx, s.registry.Client(), item)
	if err != nil {
		return model.ExtensionRepository{}, err
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
	if !selected.Installable || len(selected.Config) == 0 || strings.TrimSpace(selected.Homepage) == "" {
		writeError(w, http.StatusBadRequest, "该扩展没有可直接导入的选择器配置；JAR/Mihon 插件需要兼容运行时")
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
	if id == "" || strings.TrimSpace(payload.Name) == "" || strings.TrimSpace(payload.Homepage) == "" {
		writeError(w, http.StatusBadRequest, "源 ID、名称和首页地址不能为空")
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
	if _, err := generic.New(s.registry.Client(), item); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
		Hidden             *bool     `json:"hidden"`
		DisabledCategories *[]string `json:"disabledCategories"`
	}
	if err := decodeJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if payload.Hidden == nil && payload.DisabledCategories == nil {
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
		valid := map[string]bool{}
		for _, group := range adapter.Info().Filters {
			for _, option := range group.Options {
				if option.Value != "" {
					valid[option.Value] = true
				}
			}
		}
		normalized := make([]string, 0, len(*payload.DisabledCategories))
		seen := map[string]bool{}
		for _, value := range *payload.DisabledCategories {
			value = strings.TrimSpace(value)
			if value == "" || !valid[value] || seen[value] {
				continue
			}
			seen[value] = true
			normalized = append(normalized, value)
		}
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
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"hidden":             hidden[id],
		"disabledCategories": s.disabledCategories(r.Context())[id],
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
		Settings *model.SourceDownloadSettings `json:"settings"`
		Username string                        `json:"username"`
		Password string                        `json:"password"`
		Token    string                        `json:"token"`
		Cookie   string                        `json:"cookie"`
		HomeURL  *string                       `json:"homeUrl"`
		Login    bool                          `json:"login"`
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
	if payload.HomeURL != nil {
		account.HomeURL = strings.TrimRight(strings.TrimSpace(*payload.HomeURL), "/")
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
		SourceID:     id,
		Username:     account.Username,
		SecretCipher: secretCipher,
		TokenCipher:  tokenCipher,
		CookieCipher: cookieCipher,
		HomeURL:      account.HomeURL,
		Extra:        extraJSON,
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
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "缺少搜索关键词")
		return
	}
	page := intParam(r, "page", 1)
	cacheKey := "search:" + r.URL.RequestURI()
	if raw, storedAt, ok := s.cache.Get(cacheKey); ok {
		writeCachedJSON(w, raw, storedAt)
		return
	}
	result, err := item.Search(r.Context(), account, query, page)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.cache.Set(cacheKey, result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	item, account, ok := s.resolveSource(w, r)
	if !ok {
		return
	}
	options := model.BrowseOptions{
		Category: strings.TrimSpace(r.URL.Query().Get("category")),
		Sort:     strings.TrimSpace(r.URL.Query().Get("sort")),
		State:    strings.TrimSpace(r.URL.Query().Get("state")),
		Region:   strings.TrimSpace(r.URL.Query().Get("region")),
	}
	if options.Category == "" {
		options.Category = strings.TrimSpace(r.URL.Query().Get("kind"))
	}
	page := intParam(r, "page", 1)
	cacheKey := "browse:" + r.URL.RequestURI()
	if raw, storedAt, ok := s.cache.Get(cacheKey); ok {
		writeCachedJSON(w, raw, storedAt)
		return
	}
	result, err := item.Browse(r.Context(), account, options, page)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.cache.Set(cacheKey, result)
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
	_ = s.configs.PersistSubscriptions(r.Context(), s.store)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		SourceID            string  `json:"sourceId"`
		ComicID             string  `json:"comicId"`
		Title               string  `json:"title"`
		Cover               string  `json:"cover"`
		Author              string  `json:"author"`
		AutoDownload        *bool   `json:"autoDownload"`
		Enabled             *bool   `json:"enabled"`
		CronExpr            string  `json:"cronExpr"`
		DownloadDir         string  `json:"downloadDir"`
		ConvertToSimplified *bool   `json:"convertToSimplified"`
		Baseline            string  `json:"baseline"`
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
	cronExpr := strings.TrimSpace(payload.CronExpr)
	if cronExpr == "" {
		cronExpr = cronutil.Default(time.Now())
	}
	if err := cronutil.Validate(cronExpr); err != nil {
		writeError(w, http.StatusBadRequest, "Cron 表达式无效: "+err.Error())
		return
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
	subscription := model.Subscription{
		SourceID:            payload.SourceID,
		ComicID:             payload.ComicID,
		Title:               title,
		SeriesDir:           downloader.SeriesDirectoryName(title, convertToSimplified),
		Cover:               payload.Cover,
		Author:              payload.Author,
		Enabled:             payload.Enabled == nil || *payload.Enabled,
		AutoDownload:        payload.AutoDownload == nil || *payload.AutoDownload,
		CronExpr:            cronExpr,
		DownloadDir:         downloadDir,
		ConvertToSimplified: convertToSimplified,
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
		Enabled             *bool   `json:"enabled"`
		AutoDownload        *bool   `json:"autoDownload"`
		CronExpr            *string `json:"cronExpr"`
		DownloadDir         *string `json:"downloadDir"`
		ConvertToSimplified *bool   `json:"convertToSimplified"`
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
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	if payload.AutoDownload != nil {
		auto = *payload.AutoDownload
	}
	if payload.CronExpr != nil {
		cronExpr = strings.TrimSpace(*payload.CronExpr)
		if cronExpr == "" {
			cronExpr = existing.CronExpr
		}
		if err := cronutil.Validate(cronExpr); err != nil {
			writeError(w, http.StatusBadRequest, "Cron 表达式无效: "+err.Error())
			return
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
		seriesDir = downloader.SeriesDirectoryName(existing.Title, convertToSimplified)
	}
	if strings.TrimSpace(seriesDir) == "" {
		seriesDir = downloader.SeriesDirectoryName(existing.Title, convertToSimplified)
	}
	if err := s.store.UpdateSubscriptionConfig(r.Context(), id, enabled, auto, cronExpr, downloadDir, seriesDir, convertToSimplified); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updated, _ := s.store.Subscription(r.Context(), id)
	_ = s.configs.PersistSubscriptions(r.Context(), s.store)
	writeJSON(w, http.StatusOK, updated)
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
	add(defaultDir, true)
	if raw, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}
			mountpoint := unescapeMountPath(fields[4])
			if mountpoint == "" || mountpoint == "/" {
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
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "default": defaultDir})
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func unescapeMountPath(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListDownloadJobs(r.Context(), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type activityItem struct {
		ID      int64     `json:"id"`
		Message string    `json:"message"`
		Level   string    `json:"level"`
		Time    time.Time `json:"time"`
	}
	result := make([]activityItem, 0, len(items))
	for _, job := range items {
		level := "info"
		switch job.Status {
		case "completed":
			level = "success"
		case "failed", "canceled":
			level = "error"
		}
		message := fmt.Sprintf("%s：%s", job.ComicTitle, job.ChapterTitle)
		switch job.Status {
		case "completed":
			message += " 下载完成"
		case "failed":
			message += " 下载失败"
		case "running":
			message += " 下载中"
		case "queued":
			message += " 等待下载"
		case "paused":
			message += " 已暂停"
		}
		stamp := job.UpdatedAt
		if job.FinishedAt != nil {
			stamp = *job.FinishedAt
		}
		result = append(result, activityItem{ID: job.ID, Message: message, Level: level, Time: stamp})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
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
	_ = s.configs.PersistSubscriptions(r.Context(), s.store)
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
	go func() {
		if err := s.scheduler.CheckOne(context.Background(), id); err != nil {
			s.logger.Printf("api: check subscription %d: %v", id, err)
		}
	}()
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
		DownloadDir         string          `json:"downloadDir"`
		ConvertToSimplified *bool           `json:"convertToSimplified"`
		AutoDownload        bool            `json:"autoDownload"`
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
	created := make([]model.DownloadJob, 0, len(chapters))
	queuedCount := 0
	skippedCount := 0
	for _, chapter := range chapters {
		if strings.TrimSpace(chapter.ID) == "" {
			continue
		}
		saved, err := s.store.CreateDownloadJob(r.Context(), model.DownloadJob{
			SourceID:            payload.SourceID,
			ComicID:             payload.ComicID,
			ComicTitle:          downloader.SeriesDirectoryName(title, convertToSimplified),
			ComicCover:          payload.ComicCover,
			ChapterID:           chapter.ID,
			ChapterTitle:        source.FirstNonEmpty(chapter.Title, chapter.ID),
			ChapterOrder:        chapter.Order,
			DownloadDir:         downloadDir,
			ConvertToSimplified: convertToSimplified,
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
	if payload.AutoDownload && len(created) > 0 {
		if _, err := s.store.UpsertSubscription(r.Context(), model.Subscription{
			SourceID:         payload.SourceID,
			ComicID:          payload.ComicID,
			Title:            title,
			SeriesDir:        downloader.SeriesDirectoryName(title, convertToSimplified),
			Cover:            payload.ComicCover,
			Enabled:          true,
			AutoDownload:     true,
			CronExpr:         cronutil.Default(time.Now()),
			LastChapterID:    created[len(created)-1].ChapterID,
			LastChapterTitle: created[len(created)-1].ChapterTitle,
			LastChapterOrder: created[len(created)-1].ChapterOrder,
		}); err != nil {
			s.logger.Printf("api: auto subscribe: %v", err)
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
	Cover       string           `json:"cover,omitempty"`
	Files       []localFileEntry `json:"files"`
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
	add("")
	if subscriptions, err := s.store.ListSubscriptions(ctx); err == nil {
		for _, item := range subscriptions {
			if item.DownloadDir != "" {
				add(item.DownloadDir)
			}
		}
	}
	if jobs, err := s.store.ListDownloadJobs(ctx, 500); err == nil {
		for _, job := range jobs {
			if job.DownloadDir != "" {
				add(job.DownloadDir)
			}
		}
	}
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
			for i := range item.Files {
				item.Files[i].Path = localPath(root, item.Files[i].Path)
			}
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "downloadDir": s.cfg.DownloadDir, "downloadDirs": roots})
}

func (s *Server) scanLocalDir(root, name string) localItemEntry {
	item := localItemEntry{Title: name, Path: name, Kind: "comic", Files: []localFileEntry{}}
	folder := filepath.Join(root, name)
	files, err := os.ReadDir(folder)
	if err != nil {
		return item
	}
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		info, err := file.Info()
		if err != nil {
			continue
		}
		lower := strings.ToLower(file.Name())
		if strings.HasPrefix(lower, "cover.") {
			item.Cover = filepath.ToSlash(filepath.Join(name, file.Name()))
			continue
		}
		switch lower {
		case "book.json":
			item.Kind = "book"
			applyBookMeta(filepath.Join(folder, file.Name()), &item)
			continue
		case "comicinfo.xml":
			applyComicMeta(filepath.Join(folder, file.Name()), &item)
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(file.Name())), ".")
		kind := ""
		switch ext {
		case "cbz", "zip":
			kind = "comic"
		case "txt", "epub":
			kind = "book"
		}
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
	if item.Kind == "comic" && len(item.Files) > 0 {
		allBook := true
		for _, file := range item.Files {
			if file.Kind != "book" {
				allBook = false
				break
			}
		}
		if allBook {
			item.Kind = "book"
		}
	}
	return item
}

func applyBookMeta(path string, item *localItemEntry) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var payload struct {
		SourceID    string `json:"source"`
		ID          string `json:"id"`
		Title       string `json:"title"`
		Author      string `json:"author"`
		Description string `json:"description"`
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
	if info.Series != "" {
		item.Title = info.Series
	}
	item.Author = info.Writer
	item.Description = info.Summary
	for _, part := range strings.Fields(info.Notes) {
		if value, ok := strings.CutPrefix(part, "source="); ok {
			item.SourceID = value
		}
		if value, ok := strings.CutPrefix(part, "comicId="); ok {
			item.ComicID = value
		}
	}
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
	http.ServeFile(w, r, target)
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
	}
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
		Proxy                 *string        `json:"proxy"`
		CookieSecure          bool           `json:"cookieSecure"`
		Username              string         `json:"username"`
		CurrentPassword       string         `json:"currentPassword"`
		NewPassword           string         `json:"newPassword"`
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
		if bcrypt.CompareHashAndPassword([]byte(currentUser.PasswordHash), []byte(payload.CurrentPassword)) != nil {
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
		if payload.MaxChapterConcurrency < 1 || payload.MaxChapterConcurrency > 8 {
			writeError(w, http.StatusBadRequest, "章节并发必须在 1 到 8 之间")
			return
		}
		if err := s.store.SetSetting(r.Context(), "max_chapter_concurrency", strconv.Itoa(payload.MaxChapterConcurrency)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if payload.MaxPageConcurrency != 0 {
		if payload.MaxPageConcurrency < 1 || payload.MaxPageConcurrency > 16 {
			writeError(w, http.StatusBadRequest, "图片并发必须在 1 到 16 之间")
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
			writeError(w, http.StatusBadRequest, "三个漫画源的总下载线程必须在 1 到 24 之间")
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
	if payload.StaleDays != nil {
		normalized := map[string]int{}
		for sourceID, days := range payload.StaleDays {
			if !isManagedSource(sourceID) {
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
	s.engine.SetSourceConcurrency(current.SourceConcurrency)
	s.engine.SetDownloadPolicy(current.BatchSize, current.BatchIntervalMinutes, current.ConvertToSimplified)
	response := s.settingsResponse(r.Context(), responseUsername)
	response["ok"] = true
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

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit := 300
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 2000 {
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
	}
	return entry
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
