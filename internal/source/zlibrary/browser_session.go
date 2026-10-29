package zlibrary

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hyaeve/manco/internal/source"
)

var errBrowserVerification = errors.New("Z-Library 仍要求浏览器验证")

func normalizeBrowserAccount(account source.Account) (source.Account, error) {
	raw := strings.TrimSpace(account.Cookie)
	if len(raw) > 64<<10 {
		return account, errors.New("浏览器 Cookie 过长，请只填写该网站的请求 Cookie")
	}
	var parts []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r", "\n"), "\n") {
		line = stripHeaderPrefix(line, "Cookie")
		line = strings.TrimRight(line, "; \t")
		if line == "" {
			continue
		}
		// ParseCookie rejects malformed pairs instead of silently dropping them.
		cookies, err := http.ParseCookie(line)
		if err != nil {
			return account, errors.New("浏览器 Cookie 格式无效，请填写请求 Cookie 的名称=值，不要粘贴其他请求头或 Cookie 属性表")
		}
		for _, cookie := range cookies {
			parts = append(parts, cookie.String())
		}
	}
	if raw != "" && len(parts) == 0 {
		return account, errors.New("浏览器 Cookie 为空，请填写请求 Cookie 的名称=值")
	}
	account.Cookie = strings.Join(parts, "; ")
	account.UserAgent = stripHeaderPrefix(account.UserAgent, "User-Agent")
	if len(account.UserAgent) > 4096 || strings.ContainsAny(account.UserAgent, "\r\n") {
		return account, errors.New("User-Agent 格式无效，请只填写浏览器标识，不要粘贴其他请求头")
	}
	return account, nil
}

func stripHeaderPrefix(value, name string) string {
	value = strings.TrimSpace(value)
	if prefix, rest, ok := strings.Cut(value, ":"); ok && strings.EqualFold(strings.TrimSpace(prefix), name) {
		return strings.TrimSpace(rest)
	}
	return value
}

func browserVerificationFailure(err error, response *http.Response, request *http.Request, account source.Account) error {
	if !errors.Is(err, errBrowserVerification) {
		return err
	}
	status := "重定向受限"
	if response != nil {
		status = fmt.Sprintf("HTTP %d", response.StatusCode)
		if response.Request != nil {
			request = response.Request
		}
	}
	var cookies []*http.Cookie
	if request != nil {
		cookies = request.Cookies()
	}
	var protection, userID, userKey bool
	for _, cookie := range cookies {
		if cookie.Value == "" {
			continue
		}
		switch cookie.Name {
		case "__diamwall", "cf_clearance":
			protection = true
		case "remix_userid":
			userID = true
		case "remix_userkey":
			userKey = true
		}
	}
	yesNo := func(value bool) string {
		if value {
			return "有"
		}
		return "无"
	}
	proxyState := "关闭"
	if source.AccountUsesProxy(account) {
		proxyState = "开启"
	}
	// Report only presence/counts from the actual final request, never values.
	return fmt.Errorf("%w（%s；发送 Cookie %d 项；防护字段%s；登录字段%s；自定义 UA %s；源代理开关%s）。字段存在不代表验证有效",
		errBrowserVerification, status, len(cookies), yesNo(protection),
		yesNo(userID && userKey), yesNo(account.UserAgent != ""), proxyState)
}
