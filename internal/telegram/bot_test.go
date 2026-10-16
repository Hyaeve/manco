package telegram

import (
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type stubRoundTripper func(*http.Request) (*http.Response, error)

func (f stubRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

// 回归：启动时没配置 TG，后来在设置里填好 Token 并启用，之前 Configure 只在
// 「已经在跑且 Token 变了」时重启，所以 bot 永远不会开始轮询，表现为填了也没反应。
func TestConfigureStartsPollingWhenEnabledAfterBoot(t *testing.T) {
	service := New(log.New(io.Discard, "", 0))
	var calls int32
	service.client = &http.Client{Transport: stubRoundTripper(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		// 返回错误让 loop 进入 5 秒退避，测试只需观察是否真的开始轮询。
		return nil, errors.New("stub")
	})}
	t.Cleanup(service.Stop)

	// 启动时未启用：不应开始轮询。
	service.Configure(Config{Enabled: false})
	if service.Enabled() {
		t.Fatal("未配置时不应处于启用状态")
	}
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("未启用时不应轮询，实际调用 %d 次", got)
	}

	// 用户在设置里填好 Token 并启用：必须真正开始长轮询。
	service.Configure(Config{Enabled: true, Token: "123:abc", ChatIDs: []string{"1"}})
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&calls) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("启用后没有开始轮询")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 关掉后再 Configure 不应继续轮询。
	service.Stop()
	before := atomic.LoadInt32(&calls)
	service.Configure(Config{Enabled: false, Token: "123:abc"})
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != before {
		t.Fatalf("停用后仍在轮询：%d -> %d", before, got)
	}
}

func TestConfigureRestartsWhenTokenChanges(t *testing.T) {
	service := New(log.New(io.Discard, "", 0))
	var lastPath atomic.Value
	service.client = &http.Client{Transport: stubRoundTripper(func(request *http.Request) (*http.Response, error) {
		lastPath.Store(request.URL.Path)
		return nil, errors.New("stub")
	})}
	t.Cleanup(service.Stop)

	service.Configure(Config{Enabled: true, Token: "123:first", ChatIDs: []string{"1"}})
	if !service.Enabled() {
		t.Fatal("启用后 Enabled() 应为 true")
	}
	if err := waitForRequestPath(t, &lastPath, "123:first"); err != nil {
		t.Fatalf("首次启用没有轮询: %v", err)
	}

	service.Configure(Config{Enabled: true, Token: "123:second", ChatIDs: []string{"1"}})
	if !service.Enabled() {
		t.Fatal("换 Token 后应该继续运行")
	}
	// 旧 loop 失败后最多退避 5 秒，这里等足够久确认已经换成新 Token 轮询。
	if err := waitForRequestPath(t, &lastPath, "123:second"); err != nil {
		t.Fatalf("换 Token 后没有用新 Token 轮询: %v", err)
	}
}

// TG 复用应用统一的 HTTP 客户端，因此系统设置里的代理对 TG 同样生效。
func TestSetHTTPClientIsUsedForPolling(t *testing.T) {
	service := New(log.New(io.Discard, "", 0))
	var used int32
	shared := &http.Client{Transport: stubRoundTripper(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&used, 1)
		return nil, errors.New("stub")
	})}
	service.SetHTTPClient(shared)
	// 传 nil 不应覆盖已注入的客户端。
	service.SetHTTPClient(nil)
	service.Configure(Config{Enabled: true, Token: "123:shared", ChatIDs: []string{"1"}})
	t.Cleanup(service.Stop)

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&used) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("没有使用注入的共享 HTTP 客户端轮询")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForRequestPath(t *testing.T, lastPath *atomic.Value, want string) error {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if value, ok := lastPath.Load().(string); ok && strings.Contains(value, want) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("等待超时")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
