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
	"sync/atomic"
	"time"

	"github.com/hyaeve/manco/internal/model"
)

const transientMessageTTL = 10 * time.Minute

// App is the subset of the server the bot needs. Keeping it narrow avoids a
// dependency cycle between the HTTP API and the bot.
type App interface {
	BotSources(ctx context.Context) ([]BotSource, error)
	BotRememberGlobalSearch(ctx context.Context, query string) error
	BotSearch(ctx context.Context, sourceID, query string, page, limit int) (BotSearchPage, error)
	BotBrowse(ctx context.Context, sourceID string, page int) (BotBrowsePage, error)
	BotSubscriptionVisual(ctx context.Context, sourceID, comicID string) (BotSubscriptionVisual, error)
	BotSubscribe(ctx context.Context, sourceID, comicID string) (model.Subscription, int, error)
	BotSubscriptions(ctx context.Context) ([]BotSubscription, error)
	BotSubscriptionDetail(ctx context.Context, subscriptionID int64) (BotSubscription, error)
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
	ID        string
	Name      string
	Kind      string
	CoverBlur bool
}

// BotSubscription is the display shape of a subscription for the bot.
type BotSubscription struct {
	ID          int64
	Source      string
	Title       string
	Author      string
	Description string
	CoverURL    string
	CoverFile   string
	CoverBlur   bool
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

type BotSearchPage struct {
	Items   []model.Comic
	HasMore bool
}

type BotSubscriptionVisual struct {
	Title     string
	Author    string
	Source    string
	CoverURL  string
	CoverFile string
	CoverBlur bool
	InPlan    bool
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

	mu           sync.RWMutex
	cfg          Config
	cancel       context.CancelFunc
	offset       int64
	started      bool
	sessions     map[string]*session
	sessionSeq   atomic.Uint64
	transient    map[int64]map[int64]*transientMessage
	transientTTL time.Duration
	retryMu      sync.Mutex
	retryNotices map[string]*dailyRetryNotice
	retryLoaded  bool
}

type transientMessage struct {
	chat  string
	timer *time.Timer
}

type session struct {
	ChatID int64
	// UpdatedAt keeps a short-lived interactive session after its message is
	// sent. Messages themselves still expire through the transient TTL.
	UpdatedAt time.Time
	results   []resultEntry
	// 搜索结果的原始列表文本，返回时直接复用，不再重新拼接。
	resultsText string
	// 搜索结果的 message_id：订阅成功后要把那条消息删掉。
	resultsMessageID int64
	// 作品详情的 message_id：返回时删掉它，再重新发一份搜索结果。
	detailMessageID    int64
	detailPhoto        bool
	detailIndex        int
	browse             *browseSession
	globalSearch       *globalSearchPager
	search             *sourceSearchPager
	subscriptionDelete *subscriptionDeletePager
	subscriptionList   *subscriptionListPager
}

type subscriptionListPager struct {
	Directory string
	Items     []BotSubscription
	Page      int
}

type browseSession struct {
	SourceID        string
	SourceName      string
	SourceCoverBlur bool
	SourcePage      int
	Page            int
	HasMore         bool
	HasNext         bool
	Pool            []resultEntry
}

type globalSearchPager struct {
	Query string
	Page  int
	Pages []globalSearchPage
}

type sourceSearchPager struct {
	Query      string
	ScopeLabel string
	Sources    []BotSource
	Page       int
	SourcePage int
	HasMore    bool
	Pool       []resultEntry
}

type subscriptionDeletePager struct {
	Query string
	Items []BotSubscription
	Index int
}

type globalSearchPage struct {
	Kind    string
	Label   string
	Entries []resultEntry
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
	CoverBlur    bool
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
	s.forgetTransientMessage(chat, messageID)
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
		client:       &http.Client{Timeout: 70 * time.Second},
		logger:       logger,
		sessions:     map[string]*session{},
		transient:    map[int64]map[int64]*transientMessage{},
		transientTTL: transientMessageTTL,
	}
}

func (s *Service) allocateSession(chatID int64, value *session) string {
	if value == nil {
		value = &session{}
	}
	id := strconv.FormatUint(s.sessionSeq.Add(1), 36)
	now := time.Now()
	ttl := s.transientTTL
	if ttl <= 0 {
		ttl = transientMessageTTL
	}
	s.mu.Lock()
	for key, existing := range s.sessions {
		if existing != nil && existing.ChatID == chatID {
			existing.UpdatedAt = now
			continue
		}
		if existing == nil || now.Sub(existing.UpdatedAt) > ttl {
			delete(s.sessions, key)
		}
	}
	value.ChatID = chatID
	value.UpdatedAt = now
	s.sessions[id] = value
	s.mu.Unlock()
	return id
}

func (s *Service) session(chatID int64, id string) *session {
	s.mu.RLock()
	value := s.sessions[id]
	s.mu.RUnlock()
	if value == nil || value.ChatID != chatID {
		return nil
	}
	return value
}

func (s *Service) touchSession(chatID int64, id string) {
	s.mu.Lock()
	if value := s.sessions[id]; value != nil && value.ChatID == chatID {
		value.UpdatedAt = time.Now()
	}
	s.mu.Unlock()
}

func (s *Service) touchChatSessions(chatID int64) {
	now := time.Now()
	s.mu.Lock()
	for _, value := range s.sessions {
		if value != nil && value.ChatID == chatID {
			value.UpdatedAt = now
		}
	}
	s.mu.Unlock()
}

func (s *Service) mutateSession(chatID int64, id string, mutate func(*session)) {
	s.mu.Lock()
	if value := s.sessions[id]; value != nil && value.ChatID == chatID {
		mutate(value)
		value.UpdatedAt = time.Now()
	}
	s.mu.Unlock()
}

func (s *Service) deleteSession(chatID int64, id string) {
	s.mu.Lock()
	if value := s.sessions[id]; value != nil && value.ChatID == chatID {
		delete(s.sessions, id)
	}
	s.mu.Unlock()
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
		`{"command":"subscriptions","description":"查看订阅计划"},` +
		`{"command":"check","description":"检查订阅作品"},` +
		`{"command":"downloading","description":"查看下载队列"},` +
		`{"command":"view","description":"查看榜单"},` +
		`{"command":"sources","description":"查看内容源列表"},` +
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
	for _, messages := range s.transient {
		for _, message := range messages {
			if message != nil && message.timer != nil {
				message.timer.Stop()
			}
		}
	}
	s.transient = map[int64]map[int64]*transientMessage{}
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
		s.sendPersistentMessage(ctx, chat, text, nil)
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
	chat := strconv.FormatInt(message.Chat.ID, 10)
	if !s.allowed(message.Chat.ID) {
		s.sendPersistentMessage(ctx, chat, "未授权：请在 Manco 系统设置里把该 Chat ID 加入 TG 通知允许列表。", nil)
		return
	}
	s.trackTransientMessage(message.Chat.ID, chat, message.MessageID)
	s.handleText(ctx, message.Chat.ID, strings.TrimSpace(message.Text))
}

func (s *Service) sendMessage(ctx context.Context, chat string, text string, keyboard any) int64 {
	messageID := s.sendPersistentMessage(ctx, chat, text, keyboard)
	if messageID == 0 {
		return 0
	}
	s.trackTransientMessageForChat(chat, messageID)
	return messageID
}

func (s *Service) trackTransientMessageForChat(chat string, messageID int64) {
	chatID, _ := strconv.ParseInt(chat, 10, 64)
	s.trackTransientMessage(chatID, chat, messageID)
}

func (s *Service) sendPersistentMessage(ctx context.Context, chat string, text string, keyboard any) int64 {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	payload := map[string]any{
		"chat_id":    chat,
		"text":       text,
		"parse_mode": "HTML",
	}
	if keyboard != nil {
		payload["reply_markup"] = keyboard
	}
	messageID, err := s.callMethod(ctx, "sendMessage", payload)
	if err != nil {
		s.logger.Printf("telegram: send: %v", err)
		return 0
	}
	return messageID
}

func (s *Service) trackTransientMessage(chatID int64, chat string, messageID int64) {
	if chatID == 0 || messageID == 0 {
		return
	}
	s.mu.Lock()
	if s.transient == nil {
		s.transient = map[int64]map[int64]*transientMessage{}
	}
	messages := s.transient[chatID]
	if messages == nil {
		messages = map[int64]*transientMessage{}
		s.transient[chatID] = messages
	}
	nowMessages := make([]int64, 0, len(messages)+1)
	for id := range messages {
		nowMessages = append(nowMessages, id)
	}
	if _, ok := messages[messageID]; !ok {
		nowMessages = append(nowMessages, messageID)
	}
	if existing := messages[messageID]; existing != nil && existing.timer != nil {
		existing.timer.Stop()
	}
	messages[messageID] = &transientMessage{chat: chat}
	ttl := s.transientTTL
	if ttl <= 0 {
		ttl = transientMessageTTL
	}
	for _, id := range nowMessages {
		entry := messages[id]
		if entry == nil {
			continue
		}
		if entry.timer != nil {
			entry.timer.Stop()
		}
		entry.chat = chat
		entry.timer = time.AfterFunc(ttl, func() {
			s.expireTransientMessage(chatID, id)
		})
	}
	s.mu.Unlock()
}

func (s *Service) refreshTransientChat(chatID int64) {
	s.mu.RLock()
	messages := make(map[int64]*transientMessage, len(s.transient[chatID]))
	for id, entry := range s.transient[chatID] {
		messages[id] = entry
	}
	s.mu.RUnlock()
	for id, entry := range messages {
		if entry != nil {
			s.trackTransientMessage(chatID, entry.chat, id)
		}
	}
}

func (s *Service) forgetTransientMessage(chat string, messageID int64) {
	chatID, _ := strconv.ParseInt(chat, 10, 64)
	if chatID == 0 || messageID == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := s.transient[chatID]
	if entry := messages[messageID]; entry != nil && entry.timer != nil {
		entry.timer.Stop()
	}
	delete(messages, messageID)
	if len(messages) == 0 {
		delete(s.transient, chatID)
	}
}

func (s *Service) expireTransientMessage(chatID, messageID int64) {
	s.mu.Lock()
	messages := s.transient[chatID]
	entry := messages[messageID]
	if entry == nil {
		s.mu.Unlock()
		return
	}
	chat := entry.chat
	delete(messages, messageID)
	if len(messages) == 0 {
		delete(s.transient, chatID)
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.callMethod(ctx, "deleteMessage", map[string]any{"chat_id": chat, "message_id": messageID}); err != nil {
		s.logger.Printf("telegram: expire transient message: %v", err)
	}
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
