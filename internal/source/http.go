package source

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func NewHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{Transport: transport, Timeout: 60 * time.Second}
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
