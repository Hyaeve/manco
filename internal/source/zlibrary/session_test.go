package zlibrary

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hyaeve/manco/internal/model"
	"github.com/hyaeve/manco/internal/source"
)

func TestRequestsKeepProtectionCookiesWithoutChangingSharedClient(t *testing.T) {
	var redirects atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != "browser-session-agent" {
			t.Errorf("browser User-Agent not reused: %q", r.UserAgent())
		}
		if cookie, err := r.Cookie("imported"); err != nil || cookie.Value != "browser-value" {
			t.Error("imported browser cookie was not sent")
		}
		if _, err := r.Cookie("remix_userid"); err != nil {
			t.Error("account session cookie was not sent")
		}
		if _, err := r.Cookie("protection"); err != nil {
			redirects.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "protection", Value: "accepted", Path: "/"})
			http.Redirect(w, r, r.URL.String(), http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":1,"books":[{"id":1,"name":"Book"}]}`))
	}))
	defer server.Close()
	token, _ := encodeSession("42", "key")
	account := source.Account{HomeURL: server.URL, Token: token, Cookie: "imported=browser-value", UserAgent: "browser-session-agent"}
	shared := server.Client()
	item := New(shared)
	for range 2 {
		result, err := item.Browse(t.Context(), account, model.BrowseOptions{}, 1)
		if err != nil || len(result.Items) != 1 {
			t.Fatalf("browse after cookie redirect: %+v, %v", result, err)
		}
	}
	if redirects.Load() != 1 {
		t.Fatalf("protection cookie not retained: %d redirects", redirects.Load())
	}
	if shared.Jar != nil || shared.CheckRedirect != nil {
		t.Fatal("source changed the shared HTTP client")
	}
}

func TestLoginCookieRedirectRetainsPostBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != loginPath {
			t.Errorf("login redirect changed method or path: %s %s", r.Method, r.URL.Path)
		}
		_ = r.ParseForm()
		if r.Form.Get("email") != "reader@example.com" || r.Form.Get("password") != "fixture-password" {
			t.Error("login redirect lost its form")
		}
		if _, err := r.Cookie("protection"); err != nil {
			http.SetCookie(w, &http.Cookie{Name: "protection", Value: "accepted", Path: "/"})
			http.Redirect(w, r, loginPath, http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":1,"user":{"id":42,"remix_userkey":"key"}}`))
	}))
	defer server.Close()
	if _, err := New(server.Client()).Login(t.Context(), source.Account{
		HomeURL: server.URL, Username: "reader@example.com", Password: "fixture-password",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRedirectLoopStopsEarlyWithActionableError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, r.URL.String(), http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := New(server.Client()).request(t.Context(), source.Account{Cookie: "session=fixture"}, server.URL,
		http.MethodGet, "/eapi/book/most-popular", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "浏览器验证") || requests.Load() != 5 {
		t.Fatalf("redirect loop: requests=%d error=%v", requests.Load(), err)
	}
}

func TestBrowserChallengesAndExpiredSessionsHaveClearErrors(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"diamwall", 513, "<html>Verifying your browser | DiamWall</html>", "浏览器验证"},
		{"challenge OK", 200, "<html>Checking your browser ...</html>", "浏览器验证"},
		{"login page", 200, "<html>Log in</html>", "网页而不是 API"},
		{"expired", 401, `{"success":0}`, "登录已失效"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(fixture.status)
				_, _ = w.Write([]byte(fixture.body))
			}))
			defer server.Close()
			_, err := New(server.Client()).request(t.Context(), source.Account{Cookie: "session=fixture"}, server.URL,
				http.MethodGet, "/eapi/book/most-popular", nil, nil)
			if err == nil || !strings.Contains(err.Error(), fixture.want) || strings.Contains(err.Error(), "<html>") {
				t.Fatalf("response error = %v, want %q without raw HTML", err, fixture.want)
			}
		})
	}
}

func TestFailedAPIResponseIsNotAnEmptySuccessfulList(t *testing.T) {
	for _, body := range []string{`{"success":0,"message":"Session expired"}`, `{"success":false}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			_, err := New(server.Client()).Browse(t.Context(), source.Account{HomeURL: server.URL, Cookie: "session=fixture"}, model.BrowseOptions{}, 1)
			if err == nil {
				t.Fatal("rejected API request was reported as an empty successful list")
			}
		})
	}
}

func TestPayloadErrorAcceptsBooleanAndNumericSuccess(t *testing.T) {
	for _, value := range []any{true, 1, float64(1), "1"} {
		if err := payloadError(map[string]any{"success": value}); err != "" {
			t.Fatalf("valid success flag %v rejected: %s", value, err)
		}
	}
	for _, value := range []any{false, 0, float64(0), "0"} {
		if err := payloadError(map[string]any{"success": value}); err == "" {
			t.Fatalf("failure flag %v accepted", value)
		}
	}
}

func TestLoginDoesNotForwardCredentialsToRedirectedHost(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(true)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+loginPath, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := New(server.Client()).request(t.Context(), source.Account{Username: "reader", Password: "fixture"}, server.URL,
		http.MethodPost, loginPath, nil, url.Values{"email": {"reader"}, "password": {"fixture"}})
	if err == nil || !strings.Contains(err.Error(), "其他域名") || leaked.Load() {
		t.Fatalf("cross-site login redirect: leaked=%v, error=%v", leaked.Load(), err)
	}
}

func TestFileRedirectDoesNotForwardSourceCookies(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("source cookies leaked to a file host")
		}
		_, _ = w.Write([]byte("epub-bytes"))
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/book.epub", http.StatusFound)
	}))
	defer server.Close()
	account := source.Account{Cookie: "session=fixture"}
	item := New(server.Client())
	for _, address := range []string{server.URL + "/file", target.URL + "/book.epub"} {
		data, err := item.downloadBytes(t.Context(), account, server.URL, address)
		if err != nil || string(data) != "epub-bytes" {
			t.Fatalf("file response = %q, %v", data, err)
		}
	}
}

func TestVerificationPageIsNotDownloadedAsBook(t *testing.T) {
	for _, contentType := range []string{"text/html", "application/octet-stream"} {
		t.Run(contentType, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", contentType)
				_, _ = w.Write([]byte("<html>Verifying your browser</html>"))
			}))
			defer server.Close()
			data, err := New(server.Client()).downloadBytes(t.Context(), source.Account{Cookie: "session=fixture"}, server.URL, server.URL+"/file")
			if err == nil || len(data) != 0 || !strings.Contains(err.Error(), "浏览器验证") {
				t.Fatalf("verification page download = %q, %v", data, err)
			}
		})
	}
}

func TestUpdatedBrowserCredentialsReplaceProtectionSession(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie("session")
		if requests.Add(1) == 1 {
			if cookie == nil || cookie.Value != "old" {
				t.Error("initial cookie missing")
			}
			http.SetCookie(w, &http.Cookie{Name: "old-protection", Value: "accepted", Path: "/"})
		} else {
			if cookie == nil || cookie.Value != "new" {
				t.Error("updated cookie missing")
			}
			if _, err := r.Cookie("old-protection"); err == nil {
				t.Error("old browser session survived credential update")
			}
		}
		_, _ = w.Write([]byte(`{"success":1}`))
	}))
	defer server.Close()
	item := New(server.Client())
	for _, value := range []string{"old", "new"} {
		if _, err := item.request(t.Context(), source.Account{Cookie: "session=" + value}, server.URL,
			http.MethodGet, "/eapi/book/most-popular", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCanceledRequestDoesNotTryMirrors(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := New(server.Client()).Browse(ctx, source.Account{HomeURL: server.URL, Cookie: "session=fixture"}, model.BrowseOptions{}, 1)
	if !errors.Is(err, context.Canceled) || requests.Load() != 0 {
		t.Fatalf("canceled request retried mirrors: requests=%d, err=%v", requests.Load(), err)
	}
}

func TestDefaultSitesDoNotUseRetiredFallbacks(t *testing.T) {
	for _, base := range New(nil).bases(source.Account{}) {
		if strings.Contains(base, "z-lib.io") || strings.Contains(base, "singlelogin.re") {
			t.Fatalf("retired fallback still enabled: %s", base)
		}
	}
}
