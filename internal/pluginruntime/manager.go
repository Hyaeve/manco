// Package pluginruntime runs Kototoro parser plugins in an isolated JVM
// sidecar and exposes them through the same Source interface as built-in
// adapters. The sidecar speaks Kototoro's JSONL source protocol over stdin and
// stdout; no HTTP listener or extra container is required.
package pluginruntime

import (
	"bufio"
	"bytes"
	"context"
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
	"unicode"

	"github.com/mozillazg/go-pinyin"
)

const (
	RuntimeMihon    = "mihon"
	RuntimeKototoro = "kototoro-parser"
	ProtocolVersion = 1

	defaultKototoroHostJVMOptions = "-Xms32m -Xmx384m -XX:MaxMetaspaceSize=192m -XX:ReservedCodeCacheSize=64m -XX:ActiveProcessorCount=2 -XX:+UseSerialGC"
	defaultMihonHostJVMOptions    = "-Xms48m -Xmx512m -XX:MaxMetaspaceSize=256m -XX:ReservedCodeCacheSize=96m -XX:ActiveProcessorCount=2 -XX:+UseSerialGC"
)

var (
	pluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	errNoPlugin     = errors.New("没有已安装的 Kototoro 解析器插件")
)

type Options struct {
	ConfigDir   string
	DataDir     string
	Binary      string
	MihonBinary string
	Client      *http.Client
	Logger      *log.Logger
}

type pluginRecord struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
	Type    string `json:"type,omitempty"`
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
	PluginName  string `json:"pluginName,omitempty"`
	SourceName  string `json:"sourceName"`
	ContentType string `json:"contentType,omitempty"`
	Locale      string `json:"locale,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	// InstallURL lets the API re-install the plugin when a user adds a source
	// after unused plugins were pruned.
	InstallURL string `json:"installUrl,omitempty"`
}

type Manager struct {
	mu              sync.Mutex
	configDir       string
	dataDir         string
	binary          string
	mihonBinary     string
	client          *http.Client
	logger          *log.Logger
	plugins         []pluginRecord
	process         *hostProcess
	mihonProcess    *hostProcess
	runtimeBySource map[string]string
	closed          bool
	proxyURL        string
	proxyUsername   string
	proxyPassword   string
	idleTimeout     time.Duration
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
		configDir:       configDir,
		dataDir:         dataDir,
		binary:          strings.TrimSpace(options.Binary),
		mihonBinary:     strings.TrimSpace(options.MihonBinary),
		client:          options.Client,
		logger:          options.Logger,
		runtimeBySource: make(map[string]string),
		idleTimeout:     5 * time.Minute,
	}
	if manager.binary == "" {
		manager.binary = strings.TrimSpace(os.Getenv("MANCO_KOTOTORO_HOST"))
	}
	if manager.binary == "" {
		manager.binary = "/usr/local/lib/manco/kototoro-host/bin/manco-kototoro-host"
	}
	if manager.mihonBinary == "" {
		manager.mihonBinary = strings.TrimSpace(os.Getenv("MANCO_MIHON_HOST"))
	}
	if manager.mihonBinary == "" {
		manager.mihonBinary = "/usr/local/lib/manco/mihon-host/bin/manco-mihon-host"
	}
	if err := manager.loadConfig(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Sources(ctx context.Context) ([]Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	plugins := append([]pluginRecord(nil), m.plugins...)
	if len(plugins) == 0 {
		return nil, nil
	}
	var result []Source
	for _, runtimeType := range []string{RuntimeKototoro, RuntimeMihon} {
		if !m.hasPluginsForRuntimeLocked(runtimeType) {
			continue
		}
		metadata, err := m.callRuntimeLocked(ctx, runtimeType, "metadata", nil)
		if err != nil {
			return nil, err
		}
		sources, err := decodeSources(metadata)
		if err != nil {
			return nil, err
		}
		m.rememberSourcesLocked(runtimeType, sources)
		result = append(result, sources...)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i].Title) < strings.ToLower(result[j].Title)
	})
	return result, nil
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
	for _, runtimeType := range []string{RuntimeKototoro, RuntimeMihon} {
		if m.processForRuntimeLocked(runtimeType) != nil {
			if err := m.restartLocked(runtimeType); err != nil {
				m.proxyURL, m.proxyUsername, m.proxyPassword = oldURL, oldUsername, oldPassword
				_ = m.writeConfigLocked()
				for _, rollbackType := range []string{RuntimeKototoro, RuntimeMihon} {
					_ = m.restartLocked(rollbackType)
				}
				return err
			}
		}
	}
	return nil
}

// Install downloads and installs one Kototoro parser plugin JAR, then returns
// the sources exposed by that plugin. The existing plugin is restored if the
// new sidecar process cannot load it.
func (m *Manager) Install(ctx context.Context, id, downloadURL string, displayName ...string) ([]Source, error) {
	return m.InstallWithRuntime(ctx, id, downloadURL, RuntimeKototoro, displayName...)
}

// InstallWithRuntime downloads one extension and tells the JVM sidecar which
// runtime should load it. Kototoro parser JARs and Mihon-family APKs share the
// same JSONL protocol but need different conversion and registry paths.
func (m *Manager) InstallWithRuntime(ctx context.Context, id, downloadURL, runtimeType string, displayName ...string) ([]Source, error) {
	runtimeType = normalizeRuntime(runtimeType)
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
	oldPath := ""
	for _, item := range m.plugins {
		if item.ID == id {
			oldPath = strings.TrimSpace(item.Path)
			break
		}
	}
	if err := os.MkdirAll(filepath.Join(m.configDir, "plugins"), 0o755); err != nil {
		return nil, err
	}
	extension := ".jar"
	if runtimeType == RuntimeMihon {
		extension = ".apk"
	}
	target := filepath.Join(m.configDir, "plugins", pluginFileStem(displayName, id)+extension)
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
	record := pluginRecord{
		ID:   id,
		Path: filepath.ToSlash(filepath.Join("plugins", filepath.Base(target))),
		Type: runtimeType,
	}
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
	if err := m.restartLocked(runtimeType); err != nil {
		_ = os.Remove(target)
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, target)
		}
		m.plugins = previousPlugins
		_ = m.writeConfigLocked()
		_ = m.restartLocked(runtimeType)
		return nil, err
	}
	metadata, err := m.callRuntimeLocked(ctx, runtimeType, "metadata", nil)
	if err != nil {
		_ = os.Remove(target)
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, target)
		}
		m.plugins = previousPlugins
		_ = m.writeConfigLocked()
		_ = m.restartLocked(runtimeType)
		return nil, err
	}
	sources, err := decodeSources(metadata)
	if err != nil {
		return nil, err
	}
	m.rememberSourcesLocked(runtimeType, sources)
	_ = os.Remove(backup)
	if oldPath != "" && !strings.EqualFold(oldPath, record.Path) {
		_ = os.Remove(filepath.Join(m.configDir, filepath.FromSlash(oldPath)))
	}
	return sources, nil
}

// EnsureWithRuntime registers one extension only when it is not already in the
// plugin registry, so re-adding a source after PrunePlugins does not need a
// second download of a plugin that is still on disk.
func (m *Manager) EnsureWithRuntime(ctx context.Context, id, downloadURL, runtimeType string, displayName ...string) error {
	id = strings.TrimSpace(id)
	if !pluginIDPattern.MatchString(id) {
		return fmt.Errorf("插件 ID 无效: %q", id)
	}
	m.mu.Lock()
	installed := m.hasPluginLocked(id)
	m.mu.Unlock()
	if installed {
		return nil
	}
	_, err := m.InstallWithRuntime(ctx, id, downloadURL, runtimeType, displayName...)
	return err
}

// sourcesForRuntime reads the sources one runtime currently declares. It does
// not start the sidecar when the runtime has no plugins registered.
func (m *Manager) sourcesForRuntime(ctx context.Context, runtimeType string) ([]Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("plugin runtime 已关闭")
	}
	if !m.hasPluginsForRuntimeLocked(runtimeType) {
		return nil, nil
	}
	metadata, err := m.callRuntimeLocked(ctx, runtimeType, "metadata", nil)
	if err != nil {
		return nil, err
	}
	sources, err := decodeSources(metadata)
	if err != nil {
		return nil, err
	}
	m.rememberSourcesLocked(runtimeType, sources)
	return sources, nil
}

// InstallIsolated installs one extension and returns only the sources that this
// extension introduced. The sidecar's metadata call reports every source of the
// runtime, so the previously declared names are diffed out; without that the
// add-source dialog would attribute another plugin's sources to this extension.
func (m *Manager) InstallIsolated(ctx context.Context, id, downloadURL, runtimeType string, displayName ...string) ([]Source, error) {
	before, err := m.sourcesForRuntime(ctx, runtimeType)
	if err != nil {
		return nil, err
	}
	known := make(map[string]struct{}, len(before))
	for _, item := range before {
		known[strings.ToLower(strings.TrimSpace(item.Name))] = struct{}{}
	}
	all, err := m.InstallWithRuntime(ctx, id, downloadURL, runtimeType, displayName...)
	if err != nil {
		return nil, err
	}
	return diffSources(known, all), nil
}

// diffSources returns the entries of all whose name is not in the before set.
// Names are compared case-insensitively because the sidecar preserves the
// plugin's own casing and the same source can be reported differently.
func diffSources(before map[string]struct{}, all []Source) []Source {
	added := make([]Source, 0, len(all))
	seen := make(map[string]struct{}, len(all))
	for _, item := range all {
		key := strings.ToLower(strings.TrimSpace(item.Name))
		if key == "" {
			continue
		}
		if _, ok := before[key]; ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		added = append(added, item)
	}
	return added
}

// PrunePlugins unregisters and deletes every installed extension whose ID is
// not in keep, then restarts each runtime that lost plugins. The add-source
// dialog enumerates candidates by installing extensions, so without this the
// sidecar ends up loading every extension of every repository on each restart.
func (m *Manager) PrunePlugins(keep map[string]struct{}) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, errors.New("plugin runtime 已关闭")
	}
	removed := make([]pluginRecord, 0)
	kept := make([]pluginRecord, 0, len(m.plugins))
	for _, item := range m.plugins {
		if _, ok := keep[strings.TrimSpace(item.ID)]; ok {
			kept = append(kept, item)
			continue
		}
		removed = append(removed, item)
	}
	if len(removed) == 0 {
		return 0, nil
	}

	previous := append([]pluginRecord(nil), m.plugins...)
	m.plugins = kept
	if err := m.writeConfigLocked(); err != nil {
		m.plugins = previous
		return 0, err
	}
	for _, runtimeType := range []string{RuntimeKototoro, RuntimeMihon} {
		if m.processForRuntimeLocked(runtimeType) == nil {
			continue
		}
		if !m.hasPluginsForRuntimeLocked(runtimeType) {
			// Nothing left for this runtime: stop the sidecar instead of
			// restarting it with an empty plugin list.
			if current := m.processForRuntimeLockedMust(runtimeType); current != nil {
				current.close()
				m.setProcessForRuntimeLocked(runtimeType, nil)
			}
			continue
		}
		if err := m.restartLocked(runtimeType); err != nil {
			m.plugins = previous
			_ = m.writeConfigLocked()
			for _, rollbackType := range []string{RuntimeKototoro, RuntimeMihon} {
				_ = m.restartLocked(rollbackType)
			}
			return 0, err
		}
	}
	for _, item := range removed {
		path := strings.TrimSpace(item.Path)
		if path == "" {
			continue
		}
		_ = os.Remove(filepath.Join(m.configDir, filepath.FromSlash(path)))
	}
	return len(removed), nil
}

func (m *Manager) hasPluginLocked(id string) bool {
	for _, item := range m.plugins {
		if item.ID == id {
			return true
		}
	}
	return false
}

// HasPlugin reports whether an extension is currently registered.
func (m *Manager) HasPlugin(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hasPluginLocked(id)
}

// CleanImageCache deletes materialised plugin images that have not been touched
// for maxAge. The sidecar writes every plugin cover and page image into
// <dataDir>/<host>/images as a content-addressed blob and never removes them, so
// a busy instance accumulates tens of thousands of files. Images are only read
// right after the host materialises them, so anything older than a day is dead
// weight; the caller should run this while the sidecar is idle.
func (m *Manager) CleanImageCache(maxAge time.Duration) (int, int64, error) {
	if maxAge <= 0 {
		return 0, 0, nil
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	var freed int64
	for _, hostDirectory := range []string{"kototoro-host", "mihon-host"} {
		directory := filepath.Join(m.dataDir, hostDirectory, "images")
		entries, err := os.ReadDir(directory)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return removed, freed, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if err != nil || info.ModTime().After(cutoff) {
				continue
			}
			if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
				continue
			}
			removed++
			freed += info.Size()
		}
	}
	return removed, freed, nil
}

// InstallFile 安装本地上传的 .apk / .jar（添加 APK 源用），流程与
// InstallWithRuntime 相同，只是插件文件来自本地而不是下载地址。
func (m *Manager) InstallFile(ctx context.Context, id, localPath, runtimeType string, displayName ...string) ([]Source, error) {
	runtimeType = normalizeRuntime(runtimeType)
	id = strings.TrimSpace(id)
	if !pluginIDPattern.MatchString(id) {
		return nil, fmt.Errorf("插件 ID 无效: %q", id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("plugin runtime 已关闭")
	}
	oldPath := ""
	for _, item := range m.plugins {
		if item.ID == id {
			oldPath = strings.TrimSpace(item.Path)
			break
		}
	}
	if err := os.MkdirAll(filepath.Join(m.configDir, "plugins"), 0o755); err != nil {
		return nil, err
	}
	extension := ".jar"
	if runtimeType == RuntimeMihon {
		extension = ".apk"
	}
	target := filepath.Join(m.configDir, "plugins", pluginFileStem(displayName, id)+extension)
	temporary := target + ".part"
	source, err := os.Open(localPath)
	if err != nil {
		return nil, err
	}
	output, err := os.Create(temporary)
	if err != nil {
		_ = source.Close()
		return nil, err
	}
	_, copyErr := io.Copy(output, io.LimitReader(source, 256<<20))
	_ = output.Close()
	_ = source.Close()
	if copyErr != nil {
		_ = os.Remove(temporary)
		return nil, copyErr
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
	record := pluginRecord{ID: id, Path: filepath.ToSlash(filepath.Join("plugins", filepath.Base(target))), Type: runtimeType}
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
		m.plugins = previousPlugins
		_ = os.Remove(target)
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, target)
		}
		return nil, err
	}
	if err := m.restartLocked(runtimeType); err != nil {
		m.plugins = previousPlugins
		_ = os.Remove(target)
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, target)
		}
		_ = m.writeConfigLocked()
		_ = m.restartLocked(runtimeType)
		return nil, err
	}
	metadata, err := m.callRuntimeLocked(ctx, runtimeType, "metadata", nil)
	if err != nil {
		m.plugins = previousPlugins
		_ = os.Remove(target)
		if _, statErr := os.Stat(backup); statErr == nil {
			_ = os.Rename(backup, target)
		}
		_ = m.writeConfigLocked()
		_ = m.restartLocked(runtimeType)
		return nil, err
	}
	sources, err := decodeSources(metadata)
	if err != nil {
		return nil, err
	}
	m.rememberSourcesLocked(runtimeType, sources)
	_ = os.Remove(backup)
	if oldPath != "" && !strings.EqualFold(oldPath, record.Path) {
		_ = os.Remove(filepath.Join(m.configDir, filepath.FromSlash(oldPath)))
	}
	return sources, nil
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
	runtimeType, err := m.runtimeForFieldsLocked(ctx, fields)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	operation = runtimeOperation(runtimeType, operation)
	process := m.processForRuntimeLocked(runtimeType)
	if process != nil && process.isClosed() {
		process.close()
		m.setProcessForRuntimeLocked(runtimeType, nil)
		process = nil
	}
	if process == nil {
		if err := m.startLocked(runtimeType); err != nil {
			m.mu.Unlock()
			return nil, err
		}
		process = m.processForRuntimeLockedMust(runtimeType)
	}
	process.inFlight++
	process.idleGeneration++
	if process.idleTimer != nil {
		process.idleTimer.Stop()
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		process.inFlight--
		if m.processForRuntimeLocked(runtimeType) == process && !m.closed {
			m.scheduleIdleStopLocked(runtimeType, process)
		}
	}()

	// The sidecar multiplexes requests by requestId, so the wait must happen
	// outside the manager lock; otherwise every call is globally serialized.
	call := map[string]any{"operation": operation}
	for key, value := range fields {
		call[key] = value
	}
	if useProxy != nil {
		call["useProxy"] = *useProxy
	}
	result, err := process.request(ctx, call)
	if err != nil {
		return nil, err
	}
	if operation == "list" {
		result = trimListResult(result)
	}
	return result, nil
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
		"value":      normalizePreferenceValue(value),
	})
}

func normalizePreferenceValue(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	if _, ok := value["operation"]; ok {
		return value
	}
	legacy, ok := value["type"].(string)
	if !ok || strings.TrimSpace(legacy) == "" {
		return value
	}
	normalized := make(map[string]any, len(value))
	for key, item := range value {
		if key == "type" {
			continue
		}
		normalized[key] = item
	}
	normalized["operation"] = legacy
	return normalized
}

func asString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func (m *Manager) ImagePath(runtimeType, relativePath string) string {
	relativePath = filepath.Clean(strings.TrimSpace(relativePath))
	if relativePath == "." || relativePath == "" || filepath.IsAbs(relativePath) || strings.HasPrefix(relativePath, "..") {
		return ""
	}
	hostDirectory := "kototoro-host"
	if normalizeRuntime(runtimeType) == RuntimeMihon {
		hostDirectory = "mihon-host"
	}
	return filepath.Join(m.dataDir, hostDirectory, "images", relativePath)
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
	if m.mihonProcess != nil {
		m.mihonProcess.close()
		m.mihonProcess = nil
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
	runtimeType, err := m.runtimeForFieldsLocked(ctx, fields)
	if err != nil {
		return nil, err
	}
	return m.callRuntimeLocked(ctx, runtimeType, operation, fields)
}

func (m *Manager) callRuntimeLocked(ctx context.Context, runtimeType, operation string, fields map[string]any) (map[string]any, error) {
	runtimeType = normalizeRuntime(runtimeType)
	operation = runtimeOperation(runtimeType, operation)
	process := m.processForRuntimeLocked(runtimeType)
	if process != nil && process.isClosed() {
		process.close()
		m.setProcessForRuntimeLocked(runtimeType, nil)
		process = nil
	}
	if process == nil {
		if err := m.startLocked(runtimeType); err != nil {
			return nil, err
		}
		process = m.processForRuntimeLockedMust(runtimeType)
	}
	defer m.scheduleIdleStopLocked(runtimeType, process)
	call := map[string]any{"operation": operation}
	for key, value := range fields {
		call[key] = value
	}
	return process.request(ctx, call)
}

func (m *Manager) startLocked(runtimeType string) error {
	if err := m.writeConfigLocked(); err != nil {
		return err
	}
	runtimeType = normalizeRuntime(runtimeType)
	binary := m.binary
	environmentPrefix := "MANCO_KOTOTORO"
	label := "Kototoro"
	if runtimeType == RuntimeMihon {
		binary = m.mihonBinary
		environmentPrefix = "MANCO_MIHON"
		label = "Mihon"
	}
	process, err := startHost(binary, m.configPath(runtimeType), label, environmentPrefix, m.proxyURL, m.proxyUsername, m.proxyPassword, m.logger)
	if err != nil {
		return err
	}
	m.setProcessForRuntimeLocked(runtimeType, process)
	m.scheduleIdleStopLocked(runtimeType, process)
	return nil
}

// Direct calls hold m.mu; multiplexed calls track inFlight under that lock.
func (m *Manager) scheduleIdleStopLocked(runtimeType string, process *hostProcess) {
	if process == nil || m.idleTimeout <= 0 || process.inFlight > 0 {
		return
	}
	if process.idleTimer != nil {
		process.idleTimer.Stop()
	}
	process.idleGeneration++
	generation := process.idleGeneration
	process.idleTimer = time.AfterFunc(m.idleTimeout, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closed || m.processForRuntimeLocked(runtimeType) != process || process.idleGeneration != generation || process.inFlight > 0 {
			return
		}
		process.close()
		m.setProcessForRuntimeLocked(runtimeType, nil)
		if m.logger != nil {
			m.logger.Printf("pluginruntime: %s 空闲运行时已释放，下次调用自动启动", runtimeLabel(runtimeType))
		}
	})
}

func (m *Manager) restartLocked(runtimeType string) error {
	runtimeType = normalizeRuntime(runtimeType)
	if current := m.processForRuntimeLockedMust(runtimeType); current != nil {
		current.close()
		m.setProcessForRuntimeLocked(runtimeType, nil)
	}
	return m.startLocked(runtimeType)
}

func (m *Manager) configPath(runtimeType string) string {
	if normalizeRuntime(runtimeType) == RuntimeMihon {
		return filepath.Join(m.configDir, "mihon-host.json")
	}
	return filepath.Join(m.configDir, "kototoro-host.json")
}

func (m *Manager) loadConfig() error {
	m.plugins = nil
	seen := make(map[string]bool)
	for _, runtimeType := range []string{RuntimeKototoro, RuntimeMihon} {
		raw, err := os.ReadFile(m.configPath(runtimeType))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var config hostConfig
		if err := json.Unmarshal(raw, &config); err != nil {
			return fmt.Errorf("读取 %s 插件配置: %w", runtimeLabel(runtimeType), err)
		}
		for _, item := range config.Plugins {
			if !pluginIDPattern.MatchString(item.ID) || strings.TrimSpace(item.Path) == "" || seen[item.ID] {
				continue
			}
			item.Type = normalizeRuntime(item.Type)
			m.plugins = append(m.plugins, item)
			seen[item.ID] = true
		}
	}
	return nil
}

func (m *Manager) writeConfigLocked() error {
	for _, runtimeType := range []string{RuntimeKototoro, RuntimeMihon} {
		hostDirectory := "kototoro-host"
		if runtimeType == RuntimeMihon {
			hostDirectory = "mihon-host"
		}
		config := hostConfig{
			Version:             1,
			Plugins:             append([]pluginRecord(nil), m.pluginsForRuntimeLocked(runtimeType)...),
			PreferenceDirectory: filepath.ToSlash(filepath.Join(m.dataDir, hostDirectory, "preferences")),
			ImageDirectory:      filepath.ToSlash(filepath.Join(m.dataDir, hostDirectory, "images")),
		}
		if err := writeJSONAtomic(m.configPath(runtimeType), config); err != nil {
			return err
		}
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".part"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		// Windows fails when the destination exists; retry after removing it.
		if removeErr := os.Remove(path); removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
			if retryErr := os.Rename(temporary, path); retryErr == nil {
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

func normalizeRuntime(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), RuntimeMihon) {
		return RuntimeMihon
	}
	return RuntimeKototoro
}

// runtimeOperation translates Kototoro's source-list operation to the
// Mihon host protocol while leaving all shared source operations unchanged.
func runtimeOperation(runtimeType, operation string) string {
	if normalizeRuntime(runtimeType) == RuntimeMihon && operation == "metadata" {
		return "sources"
	}
	return operation
}

// trimListResult removes heavyweight chapter arrays from list/search results.
// Detail pages request those chapters explicitly, so discovery only carries
// the card metadata needed to render covers, titles, authors, and tags.
func trimListResult(result map[string]any) map[string]any {
	if result == nil {
		return result
	}
	values, ok := result["content"].([]any)
	if !ok {
		return result
	}
	for index, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if stringValue(item["latestChapterTitle"]) == "" {
			if latest := latestChapterTitle(item); latest != "" {
				item["latestChapterTitle"] = latest
			}
		}
		delete(item, "chapters")
		values[index] = item
	}
	result["content"] = values
	return result
}

func runtimeLabel(runtimeType string) string {
	if normalizeRuntime(runtimeType) == RuntimeMihon {
		return "Mihon"
	}
	return "Kototoro"
}

func (m *Manager) pluginsForRuntimeLocked(runtimeType string) []pluginRecord {
	runtimeType = normalizeRuntime(runtimeType)
	result := make([]pluginRecord, 0, len(m.plugins))
	for _, item := range m.plugins {
		if normalizeRuntime(item.Type) == runtimeType {
			result = append(result, item)
		}
	}
	return result
}

func (m *Manager) hasPluginsForRuntimeLocked(runtimeType string) bool {
	return len(m.pluginsForRuntimeLocked(runtimeType)) > 0
}

func (m *Manager) processForRuntimeLocked(runtimeType string) *hostProcess {
	if normalizeRuntime(runtimeType) == RuntimeMihon {
		return m.mihonProcess
	}
	return m.process
}

func (m *Manager) processForRuntimeLockedMust(runtimeType string) *hostProcess {
	return m.processForRuntimeLocked(runtimeType)
}

func (m *Manager) setProcessForRuntimeLocked(runtimeType string, process *hostProcess) {
	if normalizeRuntime(runtimeType) == RuntimeMihon {
		m.mihonProcess = process
		return
	}
	m.process = process
}

func (m *Manager) runtimeForFieldsLocked(ctx context.Context, fields map[string]any) (string, error) {
	sourceName := strings.TrimSpace(asString(fields["sourceName"]))
	if sourceName == "" {
		return m.singleRuntimeLocked()
	}
	if runtimeType, ok := m.runtimeBySource[sourceName]; ok {
		return runtimeType, nil
	}
	if runtimeType, err := m.singleRuntimeLocked(); err == nil {
		return runtimeType, nil
	}
	for _, runtimeType := range []string{RuntimeKototoro, RuntimeMihon} {
		if !m.hasPluginsForRuntimeLocked(runtimeType) {
			continue
		}
		metadata, err := m.callRuntimeLocked(ctx, runtimeType, "metadata", nil)
		if err != nil {
			continue
		}
		sources, err := decodeSources(metadata)
		if err != nil {
			continue
		}
		m.rememberSourcesLocked(runtimeType, sources)
		if _, ok := m.runtimeBySource[sourceName]; ok {
			return runtimeType, nil
		}
	}
	return m.singleRuntimeLocked()
}

func (m *Manager) singleRuntimeLocked() (string, error) {
	hasKototoro := m.hasPluginsForRuntimeLocked(RuntimeKototoro)
	hasMihon := m.hasPluginsForRuntimeLocked(RuntimeMihon)
	switch {
	case hasKototoro && hasMihon:
		return "", fmt.Errorf("无法确定内容源所属插件运行时")
	case hasMihon:
		return RuntimeMihon, nil
	default:
		return RuntimeKototoro, nil
	}
}

func (m *Manager) rememberSourcesLocked(runtimeType string, sources []Source) {
	if m.runtimeBySource == nil {
		m.runtimeBySource = make(map[string]string)
	}
	runtimeType = normalizeRuntime(runtimeType)
	for _, item := range sources {
		if name := strings.TrimSpace(item.Name); name != "" {
			m.runtimeBySource[name] = runtimeType
		}
	}
}

func pluginFileStem(displayName []string, id string) string {
	for _, value := range append(append([]string(nil), displayName...), id) {
		if stem := pinyinFileStem(value); stem != "" {
			return stem
		}
	}
	return "plugin"
}

func pinyinFileStem(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	args := pinyin.NewArgs()
	args.Separator = "-"
	var builder strings.Builder
	var han strings.Builder
	pendingSeparator := false
	appendSeparator := func() {
		if builder.Len() > 0 && !strings.HasSuffix(builder.String(), "-") {
			builder.WriteByte('-')
		}
	}
	flushHan := func() {
		if han.Len() == 0 {
			return
		}
		converted := pinyin.Slug(han.String(), args)
		han.Reset()
		for _, part := range strings.Split(converted, "-") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "" {
				continue
			}
			if builder.Len() > 0 {
				appendSeparator()
			}
			builder.WriteString(part)
		}
		pendingSeparator = builder.Len() > 0
	}
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			flushHan()
			han.WriteRune(r)
			continue
		}
		flushHan()
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if pendingSeparator && builder.Len() > 0 {
				appendSeparator()
			}
			pendingSeparator = false
			builder.WriteRune(unicode.ToLower(r))
			continue
		}
		if builder.Len() > 0 {
			pendingSeparator = true
		}
	}
	flushHan()
	stem := strings.Trim(builder.String(), "-")
	if len(stem) > 80 {
		stem = strings.TrimRight(stem[:80], "-")
		if index := strings.LastIndexByte(stem, '-'); index >= 40 {
			stem = stem[:index]
		}
	}
	return stem
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
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stdout         *bufio.Reader
	logger         *log.Logger
	pending        map[string]chan hostResponse
	writeMu        sync.Mutex
	stateMu        sync.Mutex
	closed         bool
	nextID         atomic.Uint64
	idleTimer      *time.Timer
	idleGeneration uint64
	inFlight       int
}

type hostResponse struct {
	value map[string]any
	err   error
}

func startHost(binary, configPath, label, environmentPrefix, proxyURL, proxyUsername, proxyPassword string, logger *log.Logger) (*hostProcess, error) {
	if strings.TrimSpace(binary) == "" {
		return nil, fmt.Errorf("没有配置 %s 解析器运行时", label)
	}
	command := exec.Command(binary, "serve", configPath)
	command.Env = append(os.Environ(),
		environmentPrefix+"_HOST_OPTS="+hostJVMOptions(environmentPrefix),
		"JAVA_OPTS="+hostJVMOptions(environmentPrefix),
		environmentPrefix+"_PROXY_URL="+proxyURL,
		environmentPrefix+"_PROXY_USERNAME="+proxyUsername,
		environmentPrefix+"_PROXY_PASSWORD="+proxyPassword,
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
		return nil, fmt.Errorf("启动 %s 解析器运行时: %w", label, err)
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
			process.fail(fmt.Errorf("%s 解析器运行时退出: %w", label, err))
		}
	}()
	return process, nil
}

func hostJVMOptions(environmentPrefix string) string {
	key := environmentPrefix + "_HOST_OPTS"
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	if environmentPrefix == "MANCO_MIHON" {
		return defaultMihonHostJVMOptions
	}
	return defaultKototoroHostJVMOptions
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
	for {
		line, err := readHostResponseLine(p.stdout, 32<<20)
		if err != nil {
			p.fail(fmt.Errorf("读取 Kototoro 解析器响应: %w", err))
			return
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
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
		if err := json.Unmarshal(line, &envelope); err != nil {
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
}

// Unlike Scanner, oversized response buffers live only for one message.
func readHostResponseLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > limit {
			return nil, errors.New("插件响应超过大小限制")
		}
		if len(line) == 0 && err == nil {
			return fragment, nil
		}
		line = append(line, fragment...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return line, nil
		}
		return line, err
	}
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
	if p.idleTimer != nil {
		p.idleTimer.Stop()
	}
	defer func() {
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
	}()
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
}

func (p *hostProcess) isClosed() bool {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.closed
}
