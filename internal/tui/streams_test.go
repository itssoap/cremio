package tui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/itssoap/cremio/internal/stremio"
)

// TestFetchStreamsWithRetry covers the rate-limit recovery path: an aggregator
// answers HTTP 200 with a single non-content "Rate Limit Exceeded" placeholder,
// then real content once it recovers. The helper must retry past the
// placeholders and return only content streams.
func TestFetchStreamsWithRetry_RecoversAfterPlaceholders(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			io.WriteString(w, `{"streams":[{"name":"[x] Addon","description":"Rate Limit Exceeded","externalUrl":"https://e"}]}`)
			return
		}
		io.WriteString(w, `{"streams":[{"name":"real","url":"https://cdn/v.mkv"}]}`)
	}))
	defer srv.Close()

	got := fetchStreamsWithRetry(context.Background(), stremio.NewClient(), srv.URL, "series", "tt1:1:1")
	if len(got) != 1 || got[0].URL == "" {
		t.Fatalf("expected 1 content stream after retries, got %+v", got)
	}
	if atomic.LoadInt32(&calls) < 3 {
		t.Fatalf("expected the helper to retry past placeholders, calls=%d", calls)
	}
}

// A genuinely empty result must return immediately without retrying.
func TestFetchStreamsWithRetry_EmptyIsNotRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"streams":[]}`)
	}))
	defer srv.Close()

	got := fetchStreamsWithRetry(context.Background(), stremio.NewClient(), srv.URL, "series", "tt1:1:1")
	if len(got) != 0 {
		t.Fatalf("expected no streams, got %d", len(got))
	}
	if c := atomic.LoadInt32(&calls); c != 1 {
		t.Fatalf("empty result should not be retried, calls=%d", c)
	}
}

// Persistent placeholders must never be returned as content, even after all
// retries are exhausted.
func TestFetchStreamsWithRetry_PlaceholderNeverReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"streams":[{"name":"[x]","description":"Rate Limit Exceeded","externalUrl":"https://e"}]}`)
	}))
	defer srv.Close()

	got := fetchStreamsWithRetry(context.Background(), stremio.NewClient(), srv.URL, "series", "tt1:1:1")
	if len(got) != 0 {
		t.Fatalf("placeholder streams must not be returned, got %+v", got)
	}
}
