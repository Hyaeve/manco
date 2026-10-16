package pluginruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

func TestAdapterChaptersUsesAccountProxyPreference(t *testing.T) {
	manager, calls := newProtocolTestManager(t)
	adapter, err := NewAdapter(manager, model.CustomSource{
		ID:     "demo",
		Name:   "Demo",
		Config: json.RawMessage(`{"runtime":"kototoro-parser","pluginId":"demo","sourceName":"demo"}`),
	})
	if err != nil {
		t.Fatalf("NewAdapter() error = %v", err)
	}
	comicID, err := encodeObject(map[string]any{"id": "comic"})
	if err != nil {
		t.Fatalf("encodeObject() error = %v", err)
	}
	_, err = adapter.Chapters(context.Background(), source.Account{Extra: map[string]any{"useProxy": false}}, comicID)
	if err != nil {
		t.Fatalf("Chapters() error = %v", err)
	}
	select {
	case call := <-calls:
		if call["useProxy"] != false {
			t.Fatalf("detail call useProxy = %v, want false; call=%v", call["useProxy"], call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for detail request")
	}
}

func TestAdapterDetailWithChaptersReturnsOneCombinedResult(t *testing.T) {
	manager, calls := newProtocolTestManagerResult(t, map[string]any{
		"content": map[string]any{
			"id":          "book-1",
			"title":       "测试书籍",
			"description": "简介",
			"chapters": []any{
				map[string]any{"id": "chapter-1", "title": "第一章", "number": 1},
			},
		},
	})
	adapter, err := NewAdapter(manager, model.CustomSource{
		ID:     "demo",
		Name:   "Demo",
		Config: json.RawMessage(`{"runtime":"kototoro-parser","pluginId":"demo","sourceName":"demo"}`),
	})
	if err != nil {
		t.Fatalf("NewAdapter() error = %v", err)
	}
	comicID, err := encodeObject(map[string]any{"id": "book-1"})
	if err != nil {
		t.Fatalf("encodeObject() error = %v", err)
	}
	detail, err := adapter.DetailWithChapters(context.Background(), source.Account{}, comicID)
	if err != nil {
		t.Fatalf("DetailWithChapters() error = %v", err)
	}
	if detail.Comic.Title != "测试书籍" || len(detail.Chapters) != 1 || detail.Chapters[0].Title != "第一章" {
		t.Fatalf("detail = %#v", detail)
	}

	select {
	case call := <-calls:
		if call["operation"] != "details" {
			t.Fatalf("operation = %v, want details", call["operation"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for detail request")
	}
	select {
	case call := <-calls:
		t.Fatalf("unexpected second call: %v", call)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCallWithProxyAddsPerCallPreference(t *testing.T) {
	useProxy := false
	tests := []struct {
		name        string
		useProxy    *bool
		wantPresent bool
		wantValue   bool
	}{
		{name: "disabled", useProxy: &useProxy, wantPresent: true, wantValue: false},
		{name: "unspecified", useProxy: nil, wantPresent: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, calls := newProtocolTestManager(t)
			if _, err := manager.CallWithProxy(context.Background(), "list", map[string]any{"sourceName": "example"}, test.useProxy); err != nil {
				t.Fatalf("CallWithProxy() error = %v", err)
			}

			select {
			case call := <-calls:
				value, present := call["useProxy"]
				if present != test.wantPresent {
					t.Fatalf("useProxy present = %v, want %v; call=%v", present, test.wantPresent, call)
				}
				if test.wantPresent && value != test.wantValue {
					t.Fatalf("useProxy = %v, want %v", value, test.wantValue)
				}
				if call["operation"] != "list" || call["sourceName"] != "example" {
					t.Fatalf("unexpected call envelope: %v", call)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for sidecar request")
			}
		})
	}
}

func TestRuntimeForFieldsRoutesKnownMihonAndKototoroSources(t *testing.T) {
	manager := &Manager{
		plugins: []pluginRecord{
			{ID: "mihon-copymanga", Type: RuntimeMihon},
			{ID: "kototoro-parsers", Type: RuntimeKototoro},
		},
		runtimeBySource: map[string]string{
			"CopyManga": RuntimeMihon,
			"BAOZIMH":   RuntimeKototoro,
		},
	}
	tests := []struct {
		sourceName string
		want       string
	}{
		{sourceName: "CopyManga", want: RuntimeMihon},
		{sourceName: "BAOZIMH", want: RuntimeKototoro},
	}
	for _, test := range tests {
		t.Run(test.sourceName, func(t *testing.T) {
			got, err := manager.runtimeForFieldsLocked(context.Background(), map[string]any{
				"sourceName": test.sourceName,
			})
			if err != nil {
				t.Fatalf("runtimeForFieldsLocked() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("runtimeForFieldsLocked() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWriteConfigSeparatesPluginRuntimes(t *testing.T) {
	root := t.TempDir()
	manager := &Manager{
		configDir: filepath.Join(root, "config"),
		dataDir:   filepath.Join(root, "data"),
		plugins: []pluginRecord{
			{ID: "mihon-copymanga", Path: "plugins/mihon-copymanga.apk", Type: RuntimeMihon},
			{ID: "kototoro-parsers", Path: "plugins/kototoro-parsers.jar", Type: RuntimeKototoro},
		},
	}
	if err := os.MkdirAll(manager.configDir, 0o755); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := manager.writeConfigLocked(); err != nil {
		t.Fatalf("writeConfigLocked() error = %v", err)
	}
	tests := []struct {
		runtimeType string
		wantID      string
	}{
		{runtimeType: RuntimeMihon, wantID: "mihon-copymanga"},
		{runtimeType: RuntimeKototoro, wantID: "kototoro-parsers"},
	}
	for _, test := range tests {
		t.Run(test.runtimeType, func(t *testing.T) {
			raw, err := os.ReadFile(manager.configPath(test.runtimeType))
			if err != nil {
				t.Fatalf("read config: %v", err)
			}
			var config hostConfig
			if err := json.Unmarshal(raw, &config); err != nil {
				t.Fatalf("decode config: %v", err)
			}
			if len(config.Plugins) != 1 || config.Plugins[0].ID != test.wantID {
				t.Fatalf("plugins = %+v, want only %q", config.Plugins, test.wantID)
			}
		})
	}
}

func TestImagePathUsesRuntimeDirectory(t *testing.T) {
	manager := &Manager{dataDir: filepath.Join("root", "data")}
	if got := manager.ImagePath(RuntimeMihon, "cover.jpg"); got != filepath.Join("root", "data", "mihon-host", "images", "cover.jpg") {
		t.Fatalf("Mihon ImagePath() = %q", got)
	}
	if got := manager.ImagePath(RuntimeKototoro, "cover.jpg"); got != filepath.Join("root", "data", "kototoro-host", "images", "cover.jpg") {
		t.Fatalf("Kototoro ImagePath() = %q", got)
	}
}

func TestRuntimeOperationTranslatesSourceListPerRuntime(t *testing.T) {
	tests := []struct {
		name        string
		runtimeType string
		operation   string
		want        string
	}{
		{name: "Mihon source list", runtimeType: RuntimeMihon, operation: "metadata", want: "sources"},
		{name: "Kototoro source list", runtimeType: RuntimeKototoro, operation: "metadata", want: "metadata"},
		{name: "Mihon shared browse", runtimeType: RuntimeMihon, operation: "list", want: "list"},
		{name: "Mihon shared details", runtimeType: RuntimeMihon, operation: "details", want: "details"},
		{name: "Mihon shared pages", runtimeType: RuntimeMihon, operation: "pages", want: "pages"},
		{name: "Mihon shared image", runtimeType: RuntimeMihon, operation: "image", want: "image"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := runtimeOperation(test.runtimeType, test.operation); got != test.want {
				t.Fatalf("runtimeOperation(%q, %q) = %q, want %q", test.runtimeType, test.operation, got, test.want)
			}
		})
	}
}

func TestHostJVMOptionsAreBoundedAndOverridable(t *testing.T) {
	t.Setenv("MANCO_KOTOTORO_HOST_OPTS", "")
	t.Setenv("MANCO_MIHON_HOST_OPTS", "")

	kototoro := hostJVMOptions("MANCO_KOTOTORO")
	if !strings.Contains(kototoro, "-Xmx384m") {
		t.Fatalf("Kototoro JVM options are not bounded: %q", kototoro)
	}
	mihon := hostJVMOptions("MANCO_MIHON")
	if !strings.Contains(mihon, "-Xmx512m") {
		t.Fatalf("Mihon JVM options are not bounded: %q", mihon)
	}

	t.Setenv("MANCO_KOTOTORO_HOST_OPTS", "-Xmx128m")
	if got := hostJVMOptions("MANCO_KOTOTORO"); got != "-Xmx128m" {
		t.Fatalf("Kototoro override = %q, want -Xmx128m", got)
	}
}

func TestTrimListResultDropsChaptersOnlyFromListPayloads(t *testing.T) {
	result := map[string]any{
		"content": []any{
			map[string]any{
				"title": "示例作品",
				"chapters": []any{
					map[string]any{"title": "第1话", "number": float64(1)},
					map[string]any{"title": "第12话 新章", "number": float64(12)},
				},
			},
		},
	}
	trimmed := trimListResult(result)
	items, ok := trimmed["content"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("trimmed content = %#v", trimmed["content"])
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("trimmed item = %#v", items[0])
	}
	if _, exists := item["chapters"]; exists {
		t.Fatalf("chapters were retained: %#v", item)
	}
	if got := item["title"]; got != "示例作品" {
		t.Fatalf("title = %#v", got)
	}
	if got := item["latestChapterTitle"]; got != "第12话 新章" {
		t.Fatalf("latestChapterTitle = %#v", got)
	}
}

func TestNormalizePreferenceValueSupportsMihonOperationDiscriminator(t *testing.T) {
	legacy := normalizePreferenceValue(map[string]any{"type": "string", "value": "api.example.com"})
	if legacy["operation"] != "string" || legacy["value"] != "api.example.com" {
		t.Fatalf("legacy preference value was not normalized: %v", legacy)
	}
	if _, exists := legacy["type"]; exists {
		t.Fatalf("legacy discriminator remained in normalized value: %v", legacy)
	}

	current := map[string]any{"operation": "stringSet", "values": []any{"one", "two"}}
	if got := normalizePreferenceValue(current); got["operation"] != "stringSet" {
		t.Fatalf("current preference value changed: %v", got)
	}
}

func newProtocolTestManager(t *testing.T) (*Manager, <-chan map[string]any) {
	return newProtocolTestManagerResult(t, map[string]any{"content": map[string]any{}})
}

func newProtocolTestManagerResult(t *testing.T, result map[string]any) (*Manager, <-chan map[string]any) {
	t.Helper()
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	t.Cleanup(func() {
		_ = stdinReader.Close()
		_ = stdinWriter.Close()
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
	})

	process := &hostProcess{
		stdin:   stdinWriter,
		stdout:  bufio.NewReader(stdoutReader),
		pending: map[string]chan hostResponse{},
	}
	go process.readLoop()

	calls := make(chan map[string]any, 8)
	go func() {
		line, err := bufio.NewReader(stdinReader).ReadString('\n')
		if err != nil {
			return
		}
		var envelope struct {
			RequestID string         `json:"requestId"`
			Call      map[string]any `json:"call"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			return
		}
		calls <- envelope.Call
		response, err := json.Marshal(map[string]any{
			"version":   ProtocolVersion,
			"requestId": envelope.RequestID,
			"result":    result,
		})
		if err != nil {
			return
		}
		_, _ = stdoutWriter.Write(append(response, '\n'))
	}()

	return &Manager{
		plugins: []pluginRecord{{ID: "test"}},
		process: process,
	}, calls
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestPrunePluginsRemovesUnreferencedExtensions(t *testing.T) {
	configDir := t.TempDir()
	pluginDir := filepath.Join(configDir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir plugins: %v", err)
	}
	keepPath := filepath.Join(pluginDir, "keep.jar")
	dropPath := filepath.Join(pluginDir, "drop.jar")
	for _, path := range []string{keepPath, dropPath} {
		if err := os.WriteFile(path, []byte("jar"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	manager := &Manager{
		configDir: configDir,
		plugins: []pluginRecord{
			{ID: "keep", Path: "plugins/keep.jar", Type: RuntimeKototoro},
			{ID: "drop", Path: "plugins/drop.jar", Type: RuntimeKototoro},
		},
	}

	removed, err := manager.PrunePlugins(map[string]struct{}{"keep": {}})
	if err != nil {
		t.Fatalf("PrunePlugins() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if len(manager.plugins) != 1 || manager.plugins[0].ID != "keep" {
		t.Fatalf("plugins = %#v", manager.plugins)
	}
	if _, err := os.Stat(keepPath); err != nil {
		t.Fatalf("kept plugin file missing: %v", err)
	}
	if _, err := os.Stat(dropPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dropped plugin file still present: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(configDir, "kototoro-host.json"))
	if err != nil {
		t.Fatalf("read host config: %v", err)
	}
	if strings.Contains(string(raw), `"drop"`) || !strings.Contains(string(raw), `"keep"`) {
		t.Fatalf("host config = %s", raw)
	}
}

func TestPrunePluginsKeepsReferencedExtensions(t *testing.T) {
	manager := &Manager{
		configDir: t.TempDir(),
		plugins:   []pluginRecord{{ID: "keep", Path: "plugins/keep.jar", Type: RuntimeKototoro}},
	}

	removed, err := manager.PrunePlugins(map[string]struct{}{"keep": {}})
	if err != nil {
		t.Fatalf("PrunePlugins() error = %v", err)
	}
	if removed != 0 || len(manager.plugins) != 1 {
		t.Fatalf("removed = %d, plugins = %#v", removed, manager.plugins)
	}
}

func TestEnsureWithRuntimeSkipsInstalledPlugin(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("download must not be attempted for an installed plugin")
	})}
	manager := &Manager{
		configDir: t.TempDir(),
		client:    client,
		plugins:   []pluginRecord{{ID: "demo", Path: "plugins/demo.jar", Type: RuntimeKototoro}},
	}

	if err := manager.EnsureWithRuntime(context.Background(), "demo", "https://example.invalid/demo.jar", RuntimeKototoro); err != nil {
		t.Fatalf("EnsureWithRuntime() error = %v", err)
	}
	if err := manager.EnsureWithRuntime(context.Background(), "bad id", "https://example.invalid/demo.jar", RuntimeKototoro); err == nil {
		t.Fatal("EnsureWithRuntime() accepted an invalid plugin id")
	}
}

func TestDiffSourcesReturnsOnlyNewNames(t *testing.T) {
	before := map[string]struct{}{"old": {}}
	all := []Source{
		{Name: "old", Title: "旧来源"},
		{Name: "New", Title: "新来源"},
		{Name: " NEW ", Title: "带空白的重复"},
		{Name: "", Title: "空名称"},
	}

	added := diffSources(before, all)
	if len(added) != 1 || added[0].Name != "New" {
		t.Fatalf("added = %#v", added)
	}
}
