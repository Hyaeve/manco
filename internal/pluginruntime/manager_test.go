package pluginruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
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

func newProtocolTestManager(t *testing.T) (*Manager, <-chan map[string]any) {
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

	calls := make(chan map[string]any, 1)
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
			"result":    map[string]any{"content": map[string]any{}},
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
