package picacg

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hyaeve/manco/internal/source"
)

func TestSignMatchesReferenceClient(t *testing.T) {
	const timestamp = "1700000000"
	const want = "4227e8b760083c4f0bfd01e276fbb02e46a465e4650c3324f4495b2daffb8785"

	got := sign("auth/sign-in", timestamp, http.MethodPost)
	if got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
	if slashSignature := sign("/auth/sign-in", timestamp, http.MethodPost); slashSignature == want {
		t.Fatal("signature with a leading slash unexpectedly matched the reference path")
	}
}

func TestEncodePicaQueryPreservesReferenceOrder(t *testing.T) {
	values := url.Values{
		"page": {"1"},
		"c":    {"doujin"},
		"s":    {"dd"},
	}
	if got, want := encodePicaQuery(values), "page=1&c=doujin&s=dd"; got != want {
		t.Fatalf("encoded query = %q, want %q", got, want)
	}
}

func TestLoginUsesSignedPathAndParsesToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/sign-in" {
			t.Errorf("path = %q, want /auth/sign-in", r.URL.Path)
		}
		if got, want := r.Header.Get("signature"), sign("auth/sign-in", r.Header.Get("time"), http.MethodPost); got != want {
			t.Errorf("signature = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":200,"message":"success","data":{"token":"token-123"}}`)
	}))
	defer server.Close()

	item := New(server.Client())
	result, err := item.Login(context.Background(), source.Account{
		Username: "user@example.com",
		Password: "secret",
		HomeURL:  server.URL,
	})
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if result.Token != "token-123" {
		t.Fatalf("token = %q, want token-123", result.Token)
	}
}
