// Package pluginruntime runs Kototoro parser plugins in an isolated JVM
// sidecar and exposes them through the same Source interface as built-in
// adapters. The sidecar speaks Kototoro's JSONL source protocol over stdin and
// stdout; no HTTP listener or extra container is required.
package pluginruntime

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	RuntimeKototoro = "kototoro-parser"
	ProtocolVersion = 1
)

var (
	pluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	errNoPlugin     = errors.New("没有已安装的 Kototoro 解析器插件")
)

type Options struct {
	ConfigDir string
	DataDir   string
	Binary    string
	Client    *http.Client
	Logger    *log.Logger
}

type pluginRecord struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
}

type hostConfig struct {
	Version             int            `json:"version"`
	Plugins             []pluginRecord `json:"plugins"`
	PreferenceDirectory string         `json:"preferenceDirectory"`
	ImageDirectory      string         `json:"imageDirectory"`
}

type Source struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Locale      string `json:"locale,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
}

type Config struct {
	Runtime     string `json:"runtime"`
	PluginID    string `json:"pluginId"`
	SourceName  string `json:"sourceName"`
	ContentType string `json:"contentType,omitempty"`
	Locale      string `json:"locale,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
}

type Manager struct {
	mu            sync.Mutex
	configDir     string
	dataDir       string
	binary        string
	client        *http.Client
	logger        *log.Logger
	plugins       []pluginRecord
	process       *hostProcess
	closed        bool
	proxyURL      string
	proxyUsername string
	proxyPassword string
}

func New(options Options) (*Manager, error) {
	if strings.TrimSpace(options.ConfigDir) == "" {
		return nil, errors.New("pluginruntime: config directory is required")
	}
	configDir, err := filepath.Abs(options.ConfigDir)
	if err != nil {
		return nil, err
	}
	dataDir := options.DataDir
	if strings.TrimSpace(dataDir) == "" {
		dataDir = filepath.Join(filepath.Dir(configDir), "data")
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	if options.Logger == nil {
		options.Logger = log.Default()
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 90 * time.Second}
	}
	manager := &Manager{
		configDir: configDir,
		dataDir:   dataDir,
		binary:    strings.TrimSpace(options.Binary),
		client:    options.Client,
		logger:    options.Logger,
	}
	if manager.binary == "" {
		manager.binary = strings.TrimSpace(os.Getenv("MANCO_KOTOTORO_HOST"))
	}
	if manager.binary == "" {
		manager.binary = "/usr/local/lib/manco/kototoro-host/bin/manco-kototoro-host"
	}
	if err := manager.loadConfig(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Sources(ctx context.Context) ([]Source, error) {
	m.mu.Lock()
	plugins := append([]pluginRecord(nil), m.plugins...)
	m.mu.Unlock()
	if len(plugins) == 0 {
		return nil, nil
	}
	result, err := m.call(ctx, "metadata", nil)
	if err != nil {
		return nil, err
	}
	return decodeSources(result)
}

// ConfigureProxy applies the system proxy to the isolated parser host. The
// sidecar is restarted only when a running host needs the new proxy.
func (m *Manager) ConfigureProxy(proxyURL, username, password string) error {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return errors.New("代理地址无效，请填写 http:// 或 socks5:// 地址")
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return errors.New("代理仅支持 http、https、socks5 地址")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("plugin runtime 已关闭")
	}
	if m.proxyURL == proxyURL && m.proxyUsername == username && m.proxyPassword == password {
		return nil
	}
	oldURL, oldUsername, oldPassword := m.proxyURL, m.proxyUsername, m.proxyPassword
	m.proxyURL, m.proxyUsername, m.proxyPassword = proxyURL, username, password
	if err := m.writeConfigLocked(); err != nil {
		m.proxyURL, m.proxyUsername, m.proxyPassword = oldURL, oldUsername, oldPassword
		return err
	}
	if m.process != nil {
		m.process.close()
		m.process = nil
		if err := m.startLocked(); err != nil {
			m.proxyURL, m.proxyUsername, m.proxyPassword = oldURL, oldUsername, oldPassword
			_ = m.writeConfigLocked()
			return err
		}
	}
	return nil
}

// Install downloads and installs one Kototoro parser plugin JAR, then returns
// the sources exposed by that plugin. The existing plugin is restored if the
// new sidecar process cannot load it.
func (m *Manager) Install(ctx context.Context, id, downloadURL string) ([]Source, error) {
	id = strings.TrimSpace(id)
	if !pluginIDPattern.MatchString(id) {
		return nil, fmt.Errorf("插件 ID 无效: %q", id)
	}
	downloadURL = strings.TrimSpace(downloadURL)
	if downloadURL == "" {
		return nil, errors.New("插件下载地址为空")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("plugin runtime 已关闭")
	}
	if err := os.MkdirAll(filepath.Join(m.configDir, "plugins"), 0o755); err != nil {
		return nil, err
	}
	target := filepath.Join(m.configDir, "plugins", safeFileName(id)+".jar")
	temporary := target + ".part"
	if err := m.download(ctx, downloadURL, temporary); err != nil {
		return nil, err
	}
	backup := target + ".backup"
	_ = os.Remove(backup)
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, backup); err != nil {
			_ = os.Remove(temporary)
			return nil, err
		}
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Rename(backup, target)
		return nil, err
	}

	previousPlugins := append([]pluginRecord(nil), m.plugins...)
	record := pluginRecord{ID: id, Path: filepath.ToSlash(filepath.Join("plugins", filepath.Base(target)))}
	replaced := false
	for index := range m.plugins {
		if m.plugins[index].ID == id {
			m.plugins[index] = record
			replaced = true
			break
		}
	}
	if !replaced {
		m.plugins = append(m.plugins, record)
	}
	sort.SliceStable(m.plugins, func(i, j int) bool { return m.plugins[i].ID < m.plugins[j].ID })
	if err := m.writeConfigLocked(); err != nil {
		_ = os.Remove(target)
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, target)
		}
		m.plugins = previousPlugins
		return nil, err
	}
	if err := m.restartLocked(); err != nil {
		_ = os.Remove(target)
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, target)
		}
		m.plugins = previousPlugins
		_ = m.writeConfigLocked()
		_ = m.restartLocked()
		return nil, err
	}
	sources, err := m.callLocked(ctx, "metadata", nil)
	if err != nil {
		_ = os.Remove(target)
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, target)
		}
		m.plugins = previousPlugins
		_ = m.writeConfigLocked()
		_ = m.restartLocked()
		return nil, err
	}
	_ = os.Remove(backup)
	return decodeSources(sources)
}

func (m *Manager) Call(ctx context.Context, operation string, fields map[string]any) (map[string]any, error) {
	return m.CallWithProxy(ctx, operation, fields, nil)
}

// CallWithProxy issues one sidecar operation and tells the host whether the
// parser may use the configured proxy for this call. Each content source keeps
// its own preference, so a per-call flag is required instead of a process-wide
// switch. A nil useProxy leaves the host default (proxy enabled).
func (m *Manager) CallWithProxy(ctx context.Context, operation string, fields map[string]any, useProxy *bool) (map[string]any, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("plugin runtime 已关闭")
	}
	if len(m.plugins) == 0 {
		m.mu.Unlock()
		return nil, errNoPlugin
	}
	if m.process != nil && m.process.isClosed() {
		m.process = nil
	}
	if m.process == nil {
		if err := m.startLocked(); err != nil {
			m.mu.Unlock()
			return nil, err
		}
	}
	process := m.process
	m.mu.Unlock()

	// The sidecar multiplexes requests by requestId, so the wait must happen
	// outside the manager lock; otherwise every call is globally serialized.
	call := map[string]any{"operation": operation}
	for key, value := range fields {
		call[key] = value
	}
	if useProxy != nil {
		call["useProxy"] = *useProxy
	}
	return process.request(ctx, call)
}

// BrowserSession asks the plugin runtime where a user should complete a site
// challenge (Cloudflare, gatekeeper, ...). The returned user agent matters:
// clearance cookies are bound to it.
func (m *Manager) BrowserSession(ctx context.Context, sourceName string) (string, string, error) {
	sourceName = strings.TrimSpace(sourceName)
	if sourceName == "" {
		return "", "", errors.New("内容源名称为空")
	}
	result, err := m.Call(ctx, "browserSession", map[string]any{"sourceName": sourceName})
	if err != nil {
		return "", "", err
	}
	session, ok := result["session"].(map[string]any)
	if !ok || session == nil {
		return "", "", errors.New("解析器没有返回浏览器验证地址")
	}
	return asString(session["url"]), asString(session["userAgent"]), nil
}

// ImportCookies stores browser cookies (Set-Cookie syntax, e.g. cf_clearance)
// in the sidecar's shared cookie jar so source requests can reuse them.
func (m *Manager) ImportCookies(ctx context.Context, sourceName, address string, cookies []string) (int, error) {
	sourceName = strings.TrimSpace(sourceName)
	address = strings.TrimSpace(address)
	if sourceName == "" || address == "" {
		return 0, errors.New("内容源名称和验证地址不能为空")
	}
	cleaned := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if value := strings.TrimSpace(cookie); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	if len(cleaned) == 0 {
		return 0, errors.New("没有可导入的 Cookie")
	}
	result, err := m.Call(ctx, "importCookies", map[string]any{
		"sourceName": sourceName,
		"url":        address,
		"cookies":    cleaned,
	})
	if err != nil {
		return 0, err
	}
	count := 0
	switch value := result["count"].(type) {
	case float64:
		count = int(value)
	case json.Number:
		count, _ = strconv.Atoi(value.String())
	case string:
		count, _ = strconv.Atoi(value)
	}
	return count, nil
}

// AuthCapabilities reports whether a Kototoro parser supports credential or
// browser authorization. The sidecar reads this from the parser interface
// instead of guessing from the source name.
func (m *Manager) AuthCapabilities(ctx context.Context, sourceName string) (map[string]any, error) {
	sourceName = strings.TrimSpace(sourceName)
	if sourceName == "" {
		return nil, errors.New("内容源名称为空")
	}
	return m.Call(ctx, "authCapabilities", map[string]any{"sourceName": sourceName})
}

// LoginCredentials delegates username/password login to parsers that implement
// Kototoro's optional credential auth provider.
func (m *Manager) LoginCredentials(ctx context.Context, sourceName, username, password string) (map[string]any, error) {
	sourceName = strings.TrimSpace(sourceName)
	username = strings.TrimSpace(username)
	if sourceName == "" || username == "" || password == "" {
		return nil, errors.New("内容源、账号和密码不能为空")
	}
	return m.Call(ctx, "loginCredentials", map[string]any{
		"sourceName": sourceName,
		"username":   username,
		"password":   password,
	})
}

// Preferences returns the parser-declared settings screen (domain, user agent,
// toggles and text preferences), matching what Kototoro displays.
func (m *Manager) Preferences(ctx context.Context, sourceName string) (map[string]any, error) {
	sourceName = strings.TrimSpace(sourceName)
	if sourceName == "" {
		return nil, errors.New("内容源名称为空")
	}
	return m.Call(ctx, "preferences", map[string]any{"sourceName": sourceName})
}

// UpdatePreference applies one parser-declared preference and returns the
// refreshed screen, so the UI can refresh derived defaults and revisions.
func (m *Manager) UpdatePreference(ctx context.Context, sourceName, revision, nodeID string, value map[string]any) (map[string]any, error) {
	sourceName = strings.TrimSpace(sourceName)
	revision = strings.TrimSpace(revision)
	nodeID = strings.TrimSpace(nodeID)
	if sourceName == "" || revision == "" || nodeID == "" || value == nil {
		return nil, errors.New("内容源、修订、配置项和值不能为空")
	}
	return m.Call(ctx, "updatePreference", map[string]any{
		"sourceName": sourceName,
		"revision":   revision,
		"nodeId":     nodeID,
		"value":      value,
	})
}

func asString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func (m *Manager) ImagePath(relativePath string) string {
	relativePath = filepath.Clean(strings.TrimSpace(relativePath))
	if relativePath == "." || relativePath == "" || filepath.IsAbs(relativePath) || strings.HasPrefix(relativePath, "..") {
		return ""
	}
	return filepath.Join(m.dataDir, "kototoro-host", "images", relativePath)
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	if m.process != nil {
		m.process.close()
		m.process = nil
	}
	return nil
}

func (m *Manager) call(ctx context.Context, operation string, fields map[string]any) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callLocked(ctx, operation, fields)
}

func (m *Manager) callLocked(ctx context.Context, operation string, fields map[string]any) (map[string]any, error) {
	if len(m.plugins) == 0 {
		return nil, errNoPlugin
	}
	if m.process != nil && m.process.isClosed() {
		m.process = nil
	}
	if m.process == nil {
		if err := m.startLocked(); err != nil {
			return nil, err
		}
	}
	call := map[string]any{"operation": operation}
	for key, value := range fields {
		call[key] = value
	}
	return m.process.request(ctx, call)
}

func (m *Manager) startLocked() error {
	if err := m.writeConfigLocked(); err != nil {
		return err
	}
	process, err := startHost(m.binary, m.configPath(), m.proxyURL, m.proxyUsername, m.proxyPassword, m.logger)
	if err != nil {
		return err
	}
	m.process = process
	return nil
}

func (m *Manager) restartLocked() error {
	if m.process != nil {
		m.process.close()
		m.process = nil
	}
	return m.startLocked()
}

func (m *Manager) configPath() string {
	return filepath.Join(m.configDir, "kototoro-host.json")
}

func (m *Manager) loadConfig() error {
	raw, err := os.ReadFile(m.configPath())
	if errors.Is(err, os.ErrNotExist) {
		m.plugins = nil
		return nil
	}
	if err != nil {
		return err
	}
	var config hostConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("读取 Kototoro 插件配置: %w", err)
	}
	for _, item := range config.Plugins {
		if pluginIDPattern.MatchString(item.ID) && strings.TrimSpace(item.Path) != "" {
			m.plugins = append(m.plugins, item)
		}
	}
	return nil
}

func (m *Manager) writeConfigLocked() error {
	config := hostConfig{
		Version:             1,
		Plugins:             append([]pluginRecord(nil), m.plugins...),
		PreferenceDirectory: filepath.ToSlash(filepath.Join(m.dataDir, "kototoro-host", "preferences")),
		ImageDirectory:      filepath.ToSlash(filepath.Join(m.dataDir, "kototoro-host", "images")),
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	temporary := m.configPath() + ".part"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, m.configPath()); err != nil {
		// Windows fails when the destination exists; retry after removing it.
		if removeErr := os.Remove(m.configPath()); removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
			if retryErr := os.Rename(temporary, m.configPath()); retryErr == nil {
				return nil
			}
		}
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (m *Manager) download(ctx context.Context, address, target string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/octet-stream, application/java-archive, */*")
	request.Header.Set("User-Agent", "Manco/0.1")
	response, err := m.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("下载插件返回 HTTP %d", response.StatusCode)
	}
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	defer output.Close()
	_, err = io.Copy(output, io.LimitReader(response.Body, 128<<20))
	return err
}

func safeFileName(value string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	return "plugin-" + hex.EncodeToString(sum[:8])
}

func decodeSources(result map[string]any) ([]Source, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Sources []Source `json:"sources"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	items := envelope.Sources
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items, nil
}

type hostProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	logger  *log.Logger
	pending map[string]chan hostResponse
	writeMu sync.Mutex
	stateMu sync.Mutex
	closed  bool
	nextID  atomic.Uint64
}

type hostResponse struct {
	value map[string]any
	err   error
}

func startHost(binary, configPath, proxyURL, proxyUsername, proxyPassword string, logger *log.Logger) (*hostProcess, error) {
	if strings.TrimSpace(binary) == "" {
		return nil, errors.New("没有配置 Kototoro 解析器运行时")
	}
	command := exec.Command(binary, "serve", configPath)
	command.Env = append(os.Environ(),
		"MANCO_KOTOTORO_PROXY_URL="+proxyURL,
		"MANCO_KOTOTORO_PROXY_USERNAME="+proxyUsername,
		"MANCO_KOTOTORO_PROXY_PASSWORD="+proxyPassword,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("启动 Kototoro 解析器运行时: %w", err)
	}
	process := &hostProcess{
		cmd:     command,
		stdin:   stdin,
		stdout:  bufio.NewReaderSize(stdout, 1<<20),
		logger:  logger,
		pending: make(map[string]chan hostResponse),
	}
	go process.readLoop()
	go func() {
		err := command.Wait()
		if err != nil && !process.isClosed() {
			process.fail(fmt.Errorf("Kototoro 解析器运行时退出: %w", err))
		}
	}()
	return process, nil
}

func (p *hostProcess) request(ctx context.Context, call map[string]any) (map[string]any, error) {
	id := strconv.FormatUint(p.nextID.Add(1), 10)
	envelope := map[string]any{
		"version":   ProtocolVersion,
		"requestId": id,
		"call":      call,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	response := make(chan hostResponse, 1)
	p.stateMu.Lock()
	if p.closed {
		p.stateMu.Unlock()
		return nil, errors.New("Kototoro 解析器运行时已关闭")
	}
	p.pending[id] = response
	p.stateMu.Unlock()
	defer func() {
		p.stateMu.Lock()
		delete(p.pending, id)
		p.stateMu.Unlock()
	}()
	p.writeMu.Lock()
	_, err = p.stdin.Write(append(raw, '\n'))
	p.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case result := <-response:
		return result.value, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *hostProcess) readLoop() {
	scanner := bufio.NewScanner(p.stdout)
	scanner.Buffer(make([]byte, 64*1024), 32<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var envelope struct {
			RequestID string         `json:"requestId"`
			Result    map[string]any `json:"result"`
			Error     *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			if p.logger != nil {
				p.logger.Printf("kototoro-host: invalid response: %v", err)
			}
			continue
		}
		p.stateMu.Lock()
		receiver := p.pending[envelope.RequestID]
		p.stateMu.Unlock()
		if receiver == nil {
			continue
		}
		if envelope.Error != nil {
			receiver <- hostResponse{err: fmt.Errorf("%s: %s", envelope.Error.Code, envelope.Error.Message)}
		} else {
			receiver <- hostResponse{value: envelope.Result}
		}
	}
	if err := scanner.Err(); err != nil {
		p.fail(fmt.Errorf("读取 Kototoro 解析器响应: %w", err))
		return
	}
	p.fail(errors.New("Kototoro 解析器运行时已停止"))
}

func (p *hostProcess) fail(err error) {
	p.stateMu.Lock()
	if p.closed {
		p.stateMu.Unlock()
		return
	}
	p.closed = true
	pending := p.pending
	p.pending = make(map[string]chan hostResponse)
	p.stateMu.Unlock()
	for _, receiver := range pending {
		receiver <- hostResponse{err: err}
	}
	_ = p.stdin.Close()
}

func (p *hostProcess) close() {
	p.stateMu.Lock()
	if p.closed {
		p.stateMu.Unlock()
		return
	}
	p.closed = true
	pending := p.pending
	p.pending = make(map[string]chan hostResponse)
	p.stateMu.Unlock()
	for _, receiver := range pending {
		receiver <- hostResponse{err: errors.New("Kototoro 解析器运行时已重启")}
	}
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

func (p *hostProcess) isClosed() bool {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.closed
}
