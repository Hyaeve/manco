package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

type proxyFunc func(*http.Request) (*url.URL, error)

// ProxyController keeps the active proxy function in an atomic slot so it can
// be changed from the settings page while download requests are in flight.
type ProxyController struct {
	current atomic.Value
}

func NewProxyController() *ProxyController {
	controller := &ProxyController{}
	controller.current.Store(proxyFunc(http.ProxyFromEnvironment))
	return controller
}

func (c *ProxyController) Proxy(request *http.Request) (*url.URL, error) {
	return c.current.Load().(proxyFunc)(request)
}

// Set updates the proxy resolver. An empty value falls back to the standard
// HTTP(S)_PROXY / NO_PROXY environment variables. Only http and https proxies
// are supported because those are the schemes understood by Go's HTTP client.
func (c *ProxyController) Set(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		c.current.Store(proxyFunc(http.ProxyFromEnvironment))
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid proxy URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("代理只支持 http:// 或 https://")
	}
	if parsed.Host == "" {
		return errors.New("代理地址缺少主机名")
	}
	c.current.Store(proxyFunc(http.ProxyURL(parsed)))
	return nil
}

type proxyTransport struct {
	*http.Transport
	proxy *ProxyController
}

func NewHTTPClient() *http.Client {
	controller := NewProxyController()
	transport := &http.Transport{
		Proxy:                 controller.Proxy,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Transport: &proxyTransport{Transport: transport, proxy: controller},
		Timeout:   60 * time.Second,
	}
}

// SetHTTPClientProxy changes the proxy used by a client created with
// NewHTTPClient. It reports an error for clients that do not carry a runtime
// ProxyController.
func SetHTTPClientProxy(client *http.Client, raw string) error {
	if client == nil {
		return errors.New("HTTP client is nil")
	}
	transport, ok := client.Transport.(*proxyTransport)
	if !ok {
		return errors.New("HTTP client does not support runtime proxy configuration")
	}
	return transport.proxy.Set(raw)
}

func FetchText(ctx context.Context, client *http.Client, address string, headers map[string]string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", DefaultUserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			request.Header.Set(key, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 12<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(raw))
		if len(message) > 300 {
			message = message[:300]
		}
		if response.StatusCode == http.StatusForbidden {
			return "", fmt.Errorf("HTTP %d: source rejected the request; configure its Cookie in source settings", response.StatusCode)
		}
		return "", fmt.Errorf("HTTP %d: %s", response.StatusCode, message)
	}
	return string(raw), nil
}

// FetchTextFallback tries each base URL in order and returns the first
// successful response together with the base that served it. Bases must
// include a scheme, for example "https://18comic.vip".
func FetchTextFallback(ctx context.Context, client *http.Client, bases []string, path string, values url.Values, headers func(base string) map[string]string) (string, string, error) {
	var failures []string
	for _, base := range bases {
		address := BuildURL(base, path, values)
		var extra map[string]string
		if headers != nil {
			extra = headers(base)
		}
		text, err := FetchText(ctx, client, address, extra)
		if err == nil {
			return text, base, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", base, err))
	}
	return "", "", fmt.Errorf("all mirror sites failed: %s", strings.Join(failures, "; "))
}

func BuildURL(base, path string, query url.Values) string {
	base = strings.TrimRight(base, "/")
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	address := base + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	return address
}

func Absolutize(base, reference string) string {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return ""
	}
	if strings.HasPrefix(reference, "//") {
		return "https:" + reference
	}
	parsed, err := url.Parse(reference)
	if err == nil && parsed.IsAbs() {
		return reference
	}
	root, err := url.Parse(base)
	if err != nil {
		return reference
	}
	return root.ResolveReference(parsed).String()
}

func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func HeaderCookie(cookie string) map[string]string {
	if strings.TrimSpace(cookie) == "" {
		return nil
	}
	return map[string]string{"Cookie": cookie}
}
