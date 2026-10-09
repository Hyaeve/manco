package source

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchTextFallbackUsesNextMirror(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer secondary.Close()

	client := NewHTTPClient()
	text, base, err := FetchTextFallback(context.Background(), client, []string{primary.URL, secondary.URL}, "/page", nil, nil)
	if err != nil {
		t.Fatalf("FetchTextFallback returned error: %v", err)
	}
	if text != "ok" {
		t.Fatalf("text = %q, want %q", text, "ok")
	}
	if base != secondary.URL {
		t.Fatalf("base = %q, want %q", base, secondary.URL)
	}
}

func TestFetchTextFallbackReportsAllFailures(t *testing.T) {
	client := NewHTTPClient()
	_, _, err := FetchTextFallback(context.Background(), client, []string{"http://127.0.0.1:1", "http://127.0.0.1:2"}, "/", nil, nil)
	if err == nil {
		t.Fatal("FetchTextFallback should fail when every mirror fails")
	}
}
