package source

import (
	"net/http"
	"strings"
)

// DefaultAccountUseProxy reports whether a source should use the configured
// proxy before the user has saved an explicit choice.
func DefaultAccountUseProxy(sourceID string) bool {
	return !strings.EqualFold(strings.TrimSpace(sourceID), "baozimh")
}

// ApplyDefaultAccountUseProxy materializes the source default only when no
// explicit preference exists, preserving user overrides.
func ApplyDefaultAccountUseProxy(sourceID string, account *Account) {
	if account == nil {
		return
	}
	if account.Extra != nil {
		if value, ok := account.Extra["useProxy"]; ok && value != nil {
			return
		}
	}
	SetAccountUseProxy(account, DefaultAccountUseProxy(sourceID))
}

// AccountUsesProxy reports whether a source account should route its
// requests through the configured proxy. Sources default to using the proxy
// and only opt out when the stored value is explicitly false.
func AccountUsesProxy(account Account) bool {
	if account.Extra == nil {
		return true
	}
	value, ok := account.Extra["useProxy"]
	if !ok || value == nil {
		return true
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return !strings.EqualFold(strings.TrimSpace(typed), "false") &&
			strings.TrimSpace(typed) != "0"
	case float64:
		return typed != 0
	case int:
		return typed != 0
	default:
		return true
	}
}

// SetAccountUseProxy stores the per-source proxy preference on the account.
func SetAccountUseProxy(account *Account, useProxy bool) {
	if account == nil {
		return
	}
	if account.Extra == nil {
		account.Extra = map[string]any{}
	}
	account.Extra["useProxy"] = useProxy
}

// SourceClient returns the HTTP client a source should use for this account.
// Accounts default to the shared client (proxy enabled) and only receive an
// isolated direct client when the user turns the proxy off for that source.
func SourceClient(defaultClient *http.Client, direct **http.Client, account Account) *http.Client {
	if AccountUsesProxy(account) {
		if defaultClient != nil {
			return defaultClient
		}
		return NewHTTPClient()
	}
	if direct == nil {
		return NewHTTPClientDirect()
	}
	if *direct == nil {
		*direct = NewHTTPClientDirect()
	}
	return *direct
}
