package learn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// noWait replaces the retry sleep for the duration of a test, so the retry path runs instantly.
func noWait(t *testing.T) {
	t.Helper()
	orig := waitFn
	waitFn = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { waitFn = orig })
}

func TestPostJSON_RetriesRateLimitThenSucceeds(t *testing.T) {
	noWait(t)

	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"type":"rate_limit_error"}}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, status, err := postJSON(context.Background(), srv.Client(), srv.URL, nil, []byte(`{}`))
	if err != nil {
		t.Fatalf("postJSON returned error: %v", err)
	}
	if status != http.StatusOK || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("expected the successful third attempt, got status %d body %s", status, body)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestPostJSON_DoesNotRetryAuthFailure(t *testing.T) {
	noWait(t)

	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"type":"authentication_error"}}`))
	}))
	defer srv.Close()

	_, status, err := postJSON(context.Background(), srv.Client(), srv.URL, nil, []byte(`{}`))
	if err != nil {
		t.Fatalf("postJSON returned error: %v", err)
	}
	if status != http.StatusUnauthorized {
		t.Errorf("expected status 401 returned to the caller, got %d", status)
	}
	// A bad key fails the same way every time, so retrying it only wastes the user's wall clock.
	if attempts != 1 {
		t.Errorf("expected exactly 1 attempt for a 401, got %d", attempts)
	}
}

func TestPostJSON_GivesUpAfterMaxAttempts(t *testing.T) {
	noWait(t)

	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, status, err := postJSON(context.Background(), srv.Client(), srv.URL, nil, []byte(`{}`))
	if err != nil {
		t.Fatalf("postJSON returned error: %v", err)
	}
	if status != http.StatusServiceUnavailable {
		t.Errorf("expected the last 503 handed back, got %d", status)
	}
	if attempts != maxAttempts {
		t.Errorf("expected %d attempts, got %d", maxAttempts, attempts)
	}
}

func TestBackoffDelay_HonorsRetryAfterAndCaps(t *testing.T) {
	if got := backoffDelay(0, "2"); got != 2*time.Second {
		t.Errorf("expected Retry-After to win, got %v", got)
	}
	if got := backoffDelay(0, "9999"); got != maxRetryDelay {
		t.Errorf("expected an oversized Retry-After capped at %v, got %v", maxRetryDelay, got)
	}
	// With no header, the delay is jittered - only its ceiling is guaranteed.
	if got := backoffDelay(1, ""); got < 0 || got > 2*baseRetryDelay {
		t.Errorf("expected a jittered delay within the attempt-1 window, got %v", got)
	}
}
