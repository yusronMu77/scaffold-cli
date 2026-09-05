package learn

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// Retry budget shared by every provider client. A `learn` run has already paid for a folder scan by
// the time it calls out, so one 429 or transient 5xx shouldn't end it - but retries are capped,
// since a request that fails four times with backoff isn't going to succeed on a fifth.
const (
	maxAttempts    = 4
	baseRetryDelay = time.Second
	maxRetryDelay  = 30 * time.Second
)

// waitFn is a var so tests can exercise the retry path without real sleeps.
var waitFn = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// postJSON POSTs body and returns the response bytes and status, retrying only transient failures
// (connection errors, 408, 429, 5xx) with exponential backoff plus full jitter. A 4xx other than
// those is deterministic - a bad key or a malformed request - so it comes back on the first try.
func postJSON(ctx context.Context, hc *http.Client, url string, headers map[string]string, body []byte) ([]byte, int, error) {
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, 0, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := hc.Do(req)
		if err != nil {
			lastErr = err
			if attempt == maxAttempts-1 {
				break
			}
			if waitErr := waitFn(ctx, backoffDelay(attempt, "")); waitErr != nil {
				return nil, 0, waitErr
			}
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, resp.StatusCode, fmt.Errorf("reading response body: %w", readErr)
		}
		if !retryableStatus(resp.StatusCode) || attempt == maxAttempts-1 {
			return respBody, resp.StatusCode, nil
		}

		lastErr = fmt.Errorf("provider returned status %d", resp.StatusCode)
		if waitErr := waitFn(ctx, backoffDelay(attempt, resp.Header.Get("Retry-After"))); waitErr != nil {
			return nil, 0, waitErr
		}
	}

	return nil, 0, lastErr
}

// retryableStatus reports whether a status is worth a second attempt: rate limiting, a request
// timeout, or a server-side error. Everything else (401, 400, 404, ...) fails the same way twice.
func retryableStatus(code int) bool {
	return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500
}

// backoffDelay honors a Retry-After header when the provider sends one - it knows better than any
// schedule we invent - and otherwise waits a random duration up to an exponentially growing cap
// (full jitter), so concurrent callers don't retry in lockstep.
func backoffDelay(attempt int, retryAfter string) time.Duration {
	if secs, err := strconv.ParseFloat(retryAfter, 64); err == nil && secs > 0 {
		d := time.Duration(secs * float64(time.Second))
		if d > maxRetryDelay {
			return maxRetryDelay
		}
		return d
	}

	window := baseRetryDelay << attempt
	if window > maxRetryDelay {
		window = maxRetryDelay
	}
	return time.Duration(rand.Int63n(int64(window) + 1))
}
