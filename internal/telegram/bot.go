// Package telegram provides an optional Telegram bot for Manco: command
// interaction (search, subscribe, check, archive, fill, full refresh, delete)
// plus routine notifications such as scheduled update checks and download
// summaries.
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hyaeve/manco/internal/model"
)

// App is the subset of the server the bot needs. Keeping it narrow avoids a
// dependency cycle between the HTTP API and the bot.
type App interface {
	BotSources(ctx context.Context) ([]BotSource, error)
	BotSearch(ctx context.Context, sourceID, query string, limit int) ([]model.Comic, error)
	BotSubscribe(ctx context.Context, sourceID, comicID string) (model.Subscription, error)
	BotSubscriptions(ctx context.Context) ([]BotSubscription, error)
	BotCheck(ctx context.Context, subscriptionID int64) (BotCheckResult, error)
	BotArchive(ctx context.Context, subscriptionID int64) error
	BotFill(ctx context.Context, subscriptionID int64) (int, int, error)
	BotFullDownload(ctx context.Context, subscriptionID int64) (int, error)
	BotDelete(ctx context.Context, subscriptionID int64) error
}

// BotSource is the display shape of a content source for the bot.
type BotSource struct {
	ID   string
	Name string
	Kind string
}

// BotSubscription is the display shape of a subscription for the bot.
type BotSubscription struct {
	ID      int64
	Source  string
	Title   string
	Enabled bool
}

// BotCheckResult summarises one subscription check.
type BotCheckResult struct {
	Title     string
	New       int
	Queued    int
	Completed bool
}

// Config holds the Telegram settings persisted in the app settings store.
type Config struct {
	Enabled  bool
	Token    string
	ChatIDs  []string
	ProxyURL string
}

type updateEnvelope struct {
	OK     bool     `json:"ok"`
	Result []Update `json:"result"`
}

// Update is one Telegram update payload.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Message is a Telegram chat message.
type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      *Chat  `json:"chat"`
	Text      string `json:"text"`
}

// Chat identifies a Telegram chat.
type Chat struct {
	ID int64 `json:"id"`
}

// CallbackQuery is an inline button press.
type CallbackQuery struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *Message `json:"message"`
}

// Service polls Telegram and dispatches commands.
type Service struct {
	app    App
	client *http.Client
	logger *log.Logger

	mu       sync.RWMutex
	cfg      Config
	cancel   context.CancelFunc
	offset   int64
	started  bool
	sessions map[int64]*session
}

type session struct {
	results []resultEntry
}

type resultEntry struct {
	SourceID   string
	SourceName string
	ComicID    string
	Title      string
	Author     string
	Latest     string
}

// New creates a bot service. The app binding is set later with SetApp so the
// HTTP server and the bot can reference each other.
func New(logger *log.Logger) *Service {
	return &Service{
		client:   &http.Client{Timeout: 70 * time.Second},
		logger:   logger,
		sessions: map[int64]*session{},
	}
}

// SetApp binds the host application the bot queries.
func (s *Service) SetApp(app App) {
	s.mu.Lock()
	s.app = app
	s.mu.Unlock()
}

// Configure swaps the runtime configuration. A token or proxy change restarts
// the polling loop.
func (s *Service) Configure(cfg Config) {
	cfg.Token = strings.TrimSpace(cfg.Token)
	normalized := make([]string, 0, len(cfg.ChatIDs))
	for _, id := range cfg.ChatIDs {
		if value := strings.TrimSpace(id); value != "" {
			normalized = append(normalized, value)
		}
	}
	cfg.ChatIDs = normalized
	s.mu.Lock()
	previous := s.cfg
	s.cfg = cfg
	restart := s.started && (previous.Token != cfg.Token || previous.ProxyURL != cfg.ProxyURL)
	if restart {
		if s.cancel != nil {
			s.cancel()
		}
		s.started = false
	}
	s.mu.Unlock()
	if restart {
		s.Start(context.Background())
	}
}

// Enabled reports whether the bot is configured and enabled. Automatic
// notifications are suppressed while it is disabled.
func (s *Service) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Enabled && s.cfg.Token != ""
}

// Start begins polling if the bot is enabled.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started || !s.cfg.Enabled || s.cfg.Token == "" {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.started = true
	s.mu.Unlock()
	go s.loop(ctx)
}

// Stop terminates polling.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.started = false
}

// NotifyAll sends a message to every configured chat. Empty text is ignored.
func (s *Service) NotifyAll(ctx context.Context, text string) {
	if !s.Enabled() || strings.TrimSpace(text) == "" {
		return
	}
	s.mu.RLock()
	chats := append([]string(nil), s.cfg.ChatIDs...)
	s.mu.RUnlock()
	for _, chat := range chats {
		s.sendMessage(ctx, chat, text, nil)
	}
}

func (s *Service) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := s.pollOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.logger.Printf("telegram: poll: %v", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (s *Service) pollOnce(ctx context.Context) error {
	s.mu.RLock()
	token := s.cfg.Token
	offset := s.offset
	s.mu.RUnlock()
	if token == "" {
		return nil
	}
	values := url.Values{}
	values.Set("timeout", "50")
	if offset > 0 {
		values.Set("offset", strconv.FormatInt(offset, 10))
	}
	values.Set("allowed_updates", `["message","callback_query"]`)
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?%s", token, values.Encode())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := s.httpClient().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	var envelope updateEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode updates: %w", err)
	}
	if !envelope.OK {
		return fmt.Errorf("telegram returned an error: %s", strings.TrimSpace(string(body)))
	}
	for _, update := range envelope.Result {
		s.mu.Lock()
		if update.UpdateID >= s.offset {
			s.offset = update.UpdateID + 1
		}
		s.mu.Unlock()
		s.handleUpdate(ctx, update)
	}
	return nil
}

func (s *Service) httpClient() *http.Client {
	s.mu.RLock()
	proxy := s.cfg.ProxyURL
	s.mu.RUnlock()
	if proxy == "" {
		return s.client
	}
	parsed, err := url.Parse(proxy)
	if err != nil {
		return s.client
	}
	return &http.Client{
		Timeout: 70 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(parsed),
		},
	}
}

func (s *Service) allowed(chatID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.cfg.ChatIDs) == 0 {
		return false
	}
	target := strconv.FormatInt(chatID, 10)
	for _, id := range s.cfg.ChatIDs {
		if id == target {
			return true
		}
	}
	return false
}

func (s *Service) handleUpdate(ctx context.Context, update Update) {
	if update.CallbackQuery != nil {
		s.handleCallback(ctx, update.CallbackQuery)
		return
	}
	message := update.Message
	if message == nil || message.Chat == nil {
		return
	}
	if !s.allowed(message.Chat.ID) {
		s.sendMessage(ctx, strconv.FormatInt(message.Chat.ID, 10),
			"未授权：请在 Manco 系统设置里把该 Chat ID 加入 TG 通知允许列表。", nil)
		return
	}
	s.handleText(ctx, message.Chat.ID, strings.TrimSpace(message.Text))
}

func (s *Service) sendMessage(ctx context.Context, chatID, text string, keyboard any) {
	s.mu.RLock()
	token := s.cfg.Token
	s.mu.RUnlock()
	if token == "" || strings.TrimSpace(text) == "" {
		return
	}
	payload := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	if keyboard != nil {
		payload["reply_markup"] = keyboard
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.httpClient().Do(request)
	if err != nil {
		s.logger.Printf("telegram: send: %v", err)
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
}

func (s *Service) answerCallback(ctx context.Context, callbackID string) {
	s.mu.RLock()
	token := s.cfg.Token
	s.mu.RUnlock()
	if token == "" || callbackID == "" {
		return
	}
	payload := fmt.Sprintf(`{"callback_query_id":%q}`, callbackID)
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", token)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.httpClient().Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
}

var errNoResults = errors.New("没有搜索结果")
