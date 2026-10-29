package zlibrary

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

func TestBrowserHeaderInputKeepsEveryCookie(t *testing.T) {
	for _, input := range []string{
		"__diamwall=fixture%2Bvalue==; remix_userid=42; remix_userkey=fixture-key",
		"Cookie: __diamwall=fixture%2Bvalue==; remix_userid=42; remix_userkey=fixture-key",
		"cookie: __diamwall=fixture%2Bvalue==;\r\nremix_userid=42;\nremix_userkey=fixture-key",
	} {
		t.Run(input, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for name, want := range map[string]string{
					"__diamwall": "fixture%2Bvalue==", "remix_userid": "42", "remix_userkey": "fixture-key",
				} {
					cookie, err := r.Cookie(name)
					if err != nil || cookie.Value != want {
						t.Errorf("browser cookie %s was lost or changed", name)
					}
				}
				if r.UserAgent() != "fixture-browser" {
					t.Error("User-Agent header prefix was not normalized")
				}
				_, _ = w.Write([]byte(`{"success":1,"books":[]}`))
			}))
			defer server.Close()
			_, err := New(server.Client()).Browse(t.Context(), source.Account{
				HomeURL: server.URL, Cookie: input, UserAgent: "User-Agent: fixture-browser",
			}, model.BrowseOptions{}, 1)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSelectedWebsiteDoesNotFallBackToOtherDomains(t *testing.T) {
	got := New(nil).bases(source.Account{HomeURL: "https://selected.example/"})
	if len(got) != 1 || got[0] != "https://selected.example" {
		t.Fatalf("selected site was not pinned: %v", got)
	}
}

func TestMalformedBrowserCookieIsNotSilentlyDropped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid browser cookie reached the network")
	}))
	defer server.Close()
	for _, input := range []string{
		"Cookie: broken secret-format",
		"__diamwall=valid; broken secret-format",
		"Host: secret.example\n__diamwall=valid",
		"Cookie:",
	} {
		_, err := New(server.Client()).request(t.Context(), source.Account{Cookie: input}, server.URL,
			http.MethodGet, "/eapi/book/most-popular", nil, nil)
		if err == nil || !strings.Contains(err.Error(), "Cookie") {
			t.Fatalf("invalid Cookie was accepted: %v", err)
		}
		if strings.Contains(err.Error(), "secret-format") || strings.Contains(err.Error(), "secret.example") {
			t.Fatal("Cookie error included private input")
		}
	}
}

func TestBrowserCookieRequiresSelectedDomain(t *testing.T) {
	_, err := New(nil).Browse(t.Context(), source.Account{Cookie: "__diamwall=private-guard"}, model.BrowseOptions{}, 1)
	if err == nil || !strings.Contains(err.Error(), "选择完成验证的网站地址") {
		t.Fatalf("unbound browser Cookie error = %v", err)
	}
	if strings.Contains(err.Error(), "private-guard") {
		t.Fatal("unbound cookie error exposed its value")
	}
}

func TestInvalidUserAgentIsRejectedBeforeRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid User-Agent reached the network")
	}))
	defer server.Close()
	_, err := New(server.Client()).request(t.Context(), source.Account{
		Cookie: "session=fixture", UserAgent: "User-Agent: private-agent\nCookie: private-cookie",
	}, server.URL, http.MethodGet, "/eapi/book/most-popular", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "User-Agent") ||
		strings.Contains(err.Error(), "private-agent") || strings.Contains(err.Error(), "private-cookie") {
		t.Fatalf("User-Agent error exposed input or accepted invalid headers: %v", err)
	}
}

func TestVerificationDiagnosticsContainOnlyPresenceAndCounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(513)
		_, _ = w.Write([]byte("<html>DiamWall private-response-body</html>"))
	}))
	defer server.Close()
	_, err := New(server.Client()).Browse(t.Context(), source.Account{
		HomeURL: server.URL, Cookie: "__diamwall=private-guard; remix_userid=private-id; remix_userkey=private-key",
		UserAgent: "private-browser-agent",
	}, model.BrowseOptions{}, 1)
	if err == nil {
		t.Fatal("expected verification rejection")
	}
	message := err.Error()
	for _, want := range []string{"HTTP 513", "发送 Cookie 4 项", "防护字段有", "登录字段有", "自定义 UA 有"} {
		if !strings.Contains(message, want) {
			t.Errorf("verification diagnostic missing %q: %s", want, message)
		}
	}
	for _, secret := range []string{"private-guard", "private-id", "private-key", "private-browser-agent", "private-response-body"} {
		if strings.Contains(message, secret) {
			t.Errorf("diagnostic exposed %s", secret)
		}
	}
}

func TestVerificationDiagnosticsUseActualRequestNotStoredCookie(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://file.example/book", nil)
	err := browserVerificationFailure(verificationError(), &http.Response{
		StatusCode: 513, Request: request,
	}, request, source.Account{
		Cookie: "__diamwall=private-guard; remix_userid=42; remix_userkey=private-key",
	})
	if !errors.Is(err, errBrowserVerification) || !strings.Contains(err.Error(), "发送 Cookie 0 项") ||
		!strings.Contains(err.Error(), "防护字段无") || !strings.Contains(err.Error(), "登录字段无") {
		t.Fatalf("diagnostic did not reflect the final request: %v", err)
	}
}
