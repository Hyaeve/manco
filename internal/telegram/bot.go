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
	BotBrowse(ctx context.Context, sourceID string, page int) (BotBrowsePage, error)
	BotSubscribe(ctx context.Context, sourceID, comicID string) (model.Subscription, int, error)
	BotSubscriptions(ctx context.Context) ([]BotSubscription, error)
	BotDownloading(ctx context.Context) (BotDownloadingSnapshot, error)
	BotCheck(ctx context.Context, subscriptionID int64) (BotCheckResult, error)
	BotArchive(ctx context.Context, subscriptionID int64) error
	BotFill(ctx context.Context, subscriptionID int64) (int, int, error)
	BotScrape(ctx context.Context, subscriptionID int64) (bool, error)
	BotSetSubscriptionEnabled(ctx context.Context, subscriptionID int64, enabled bool) error
	BotComicDetail(ctx context.Context, sourceID, comicID string) (model.Comic, error)
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
	ID          int64
	Source      string
	Title       string
	Enabled     bool
	CronExpr    string
	DownloadDir string
	ParentDir   string
}

type BotDownloadItem struct {
	ComicTitle   string
	ChapterTitle string
}

type BotDownloadingSnapshot struct {
	Running         []BotDownloadItem
	NextQueued      *BotDownloadItem
	CooldownMinutes int
}

type BotBrowsePage struct {
	Items   []model.Comic
	Page    int
	HasMore bool
}

// BotCheckResult summarises one subscription check.
type BotCheckResult struct {
	Title     string
	New       int
	Queued    int
	Retried   int
	Completed bool
}

// Config holds the Telegram settings persisted in the app settings store.
type Config struct {
	Enabled bool
	Token   string
	ChatIDs []string
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
	// 搜索结果的原始列表文本，返回时直接复用，不再重新拼接。
	resultsText string
	// 搜索结果的 message_id：订阅成功后要把那条消息删掉。
	resultsMessageID int64
	// 作品详情的 message_id：返回时删掉它，再重新发一份搜索结果。
	detailMessageID int64
	detailPhoto     bool
	detailIndex     int
	browse          *browseSession
}

type browseSession struct {
	SourceID   string
	SourceName string
	SourcePage int
	Page       int
	HasMore    bool
	HasNext    bool
	Pool       []resultEntry
}

type resultEntry struct {
	SourceID     string
	SourceName   string
	ComicID      string
	Title        string
	Author       string
	Latest       string
	ChapterCount int
	Cover        string
	Summary      string
}

// callMethod 调用一个 Bot API 方法并返回 result.message_id（没有则返回 0）。
// sendMessage / sendPhoto / deleteMessage 的返回信封是同一套结构。
func (s *Service) callMethod(ctx context.Context, method string, payload map[string]any) (int64, error) {
	s.mu.RLock()
	token := s.cfg.Token
	s.mu.RUnlock()
	if token == "" {
		return 0, errors.New("telegram token 未配置")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	var envelope struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return 0, err
	}
	if !envelope.OK {
		return 0, fmt.Errorf("telegram %s: %s", method, strings.TrimSpace(envelope.Description))
	}
	return envelope.Result.MessageID, nil
}

// deleteMessage 删除一条消息（用户按「返回」时收起作品详情）。
func (s *Service) deleteMessage(ctx context.Context, chat string, messageID int64) {
	if messageID == 0 {
		return
	}
	if _, err := s.callMethod(ctx, "deleteMessage", map[string]any{"chat_id": chat, "message_id": messageID}); err != nil {
		s.logger.Printf("telegram: deleteMessage: %v", err)
	}
}

// deleteMessageAsync 让「切换消息框」不必等待删除请求结束。
func (s *Service) deleteMessageAsync(chat string, messageID int64) {
	if messageID == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		s.deleteMessage(ctx, chat, messageID)
	}()
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

// SetHTTPClient makes the bot reuse the application's HTTP client, so Telegram
// traffic follows the system proxy configured on the settings page instead of
// needing a dedicated proxy field.
func (s *Service) SetHTTPClient(client *http.Client) {
	if client == nil {
		return
	}
	s.mu.Lock()
	s.client = client
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
	shouldRun := cfg.Enabled && cfg.Token != ""
	wasRunning := s.started
	tokenChanged := previous.Token != cfg.Token
	// 停用、换 Token，或本来没在跑但现在该跑（比如用户刚在设置里填好 Token），
	// 都要先把旧的长轮询停掉。
	if wasRunning && (!shouldRun || tokenChanged) {
		if s.cancel != nil {
			s.cancel()
			s.cancel = nil
		}
		s.started = false
	}
	s.mu.Unlock()
	// 之前没启动过（启动时未配置）而现在启用了，这里必须真正开始轮询，
	// 否则用户在设置里填了 Token 也不会收到任何响应。
	if shouldRun && (!wasRunning || tokenChanged) {
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

func (s *Service) NotificationReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Enabled && s.cfg.Token != "" && len(s.cfg.ChatIDs) > 0
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
	go s.registerCommands(ctx)
}

// registerCommands 把英文快捷指令注册到 Telegram，客户端才会在输入框旁显示
// 命令菜单（以前完全没调 setMyCommands，所以看不到任何快捷指令）。
func (s *Service) registerCommands(ctx context.Context) {
	s.mu.RLock()
	token := s.cfg.Token
	s.mu.RUnlock()
	if token == "" {
		return
	}
	payload := `{"commands":[` +
		`{"command":"start","description":"开始 / 查看帮助"},` +
		`{"command":"subscriptions","description":"查看全部或指定父目录订阅"},` +
		`{"command":"check","description":"检查全部、父目录或指定作品"},` +
		`{"command":"downloading","description":"查看正在下载和暂停状态"},` +
		`{"command":"view","description":"查看指定内容源榜单"},` +
		`{"command":"sources","description":"查看已添加内容源"},` +
		`{"command":"sh","description":"搜索作品"}` +
		`]}`
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/setMyCommands", token)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(payload))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		s.logger.Printf("telegram: setMyCommands: %v", err)
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
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
	// 共用应用的 HTTP 客户端（60 秒超时），长轮询留出足够余量。
	values.Set("timeout", "40")
	if offset > 0 {
		values.Set("offset", strconv.FormatInt(offset, 10))
	}
	values.Set("allowed_updates", `["message","callback_query"]`)
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?%s", token, values.Encode())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := s.client.Do(request)
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
	response, err := s.client.Do(request)
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
	response, err := s.client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
}

// answerCallbackAsync 先让消息更新和回调应答并行，减少按钮点击后的等待。
func (s *Service) answerCallbackAsync(callbackID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		s.answerCallback(ctx, callbackID)
	}()
}

// editMessage 原位更新作品详情或检查结果，photo 表示消息是否带图片。
func (s *Service) editMessage(ctx context.Context, chat string, messageID int64, photo bool, text string, keyboard any) error {
	if messageID == 0 || strings.TrimSpace(text) == "" {
		return nil
	}
	method := "editMessageText"
	textKey := "text"
	if photo {
		method = "editMessageCaption"
		textKey = "caption"
	}
	payload := map[string]any{
		"chat_id":    chat,
		"message_id": messageID,
		textKey:      text,
		"parse_mode": "HTML",
	}
	if keyboard != nil {
		payload["reply_markup"] = keyboard
	}
	_, err := s.callMethod(ctx, method, payload)
	return err
}

var errNoResults = errors.New("没有搜索结果")
