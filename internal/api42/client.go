package api42

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/EvAvKein/Fortytwode/internal/config"
	"golang.org/x/time/rate"
)

const (
	perSecondLimit = 2    // 42 per-application cap: 2 requests/second
	hourlyLimit    = 1200 // 42 per-application cap: 1200 requests/hour
	pageSize       = 100  // 42 API maximum
	maxRetries     = 5
)

// Limiter enforces both 42 per-application caps at once: the per-second request
// rate and the hourly quota. Wait is the single gate every API call passes
// through. It is safe for concurrent use, so the web server shares one across
// all syncs to keep their *combined* traffic under the caps; the CLI uses its own.
type Limiter struct {
	perSecond *rate.Limiter
	perHour   *rate.Limiter
}

// NewLimiter returns a Limiter tuned to the 42 per-application caps.
//
// The hourly limiter's burst is the quota itself, so it holds one hour's budget:
// requests run at the full 2 req/s until that budget is spent, then throttle to
// the refill rate. In normal operation (the per-user sync cooldown keeps volume
// low) it never bites — it is purely a backstop against overrunning the quota.
func NewLimiter() *Limiter {
	return &Limiter{
		perSecond: rate.NewLimiter(perSecondLimit, 1),
		perHour:   rate.NewLimiter(rate.Every(time.Hour/hourlyLimit), hourlyLimit),
	}
}

// Wait blocks until both caps permit another request, or ctx is cancelled. The
// hourly cap is checked first so a long hourly wait doesn't consume (and waste)
// a per-second token before the request is actually cleared to go.
func (l *Limiter) Wait(ctx context.Context) error {
	if err := l.perHour.Wait(ctx); err != nil {
		return err
	}
	return l.perSecond.Wait(ctx)
}

// Client talks to the 42 v2 API on behalf of one access token, pacing requests
// through a shared limiter.
type Client struct {
	token   string
	http    *http.Client
	limiter *Limiter
}

// New returns a Client authenticated with the given bearer token and paced by
// limiter (a solo limiter is created when nil).
func New(token string, limiter *Limiter) *Client {
	if limiter == nil {
		limiter = NewLimiter()
	}
	return &Client{token: token, http: &http.Client{Timeout: 30 * time.Second}, limiter: limiter}
}

// request fetches one path (an object, or one page of a collection). It returns
// the raw JSON body and the X-Total header (-1 when the header is absent),
// retrying on 429 and 5xx with exponential backoff.
func (client *Client) request(ctx context.Context, path string, params url.Values) (json.RawMessage, int, error) {
	u := config.APIv2 + "/" + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	for attempt := 0; ; attempt++ {
		if err := client.limiter.Wait(ctx); err != nil {
			return nil, 0, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Authorization", "Bearer "+client.token)

		res, err := client.http.Do(req)
		if err != nil {
			return nil, 0, err
		}

		if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
			res.Body.Close()
			if attempt == maxRetries {
				return nil, 0, fmt.Errorf("%d for %s after %d retries", res.StatusCode, path, maxRetries)
			}
			wait := backoff(res.Header.Get("Retry-After"), attempt)
			fmt.Printf("  %d on %s; backing off %s (attempt %d)\n", res.StatusCode, path, wait, attempt+1)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, 0, ctx.Err()
			}
			continue
		}

		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			return nil, 0, err
		}
		if res.StatusCode != http.StatusOK {
			return nil, 0, fmt.Errorf("%d for %s: %s", res.StatusCode, path, body)
		}

		total := -1
		if t, err := strconv.Atoi(res.Header.Get("X-Total")); err == nil {
			total = t
		}
		return body, total, nil
	}
}

// backoff picks a wait duration: the Retry-After header if present and valid,
// otherwise 2^attempt seconds.
func backoff(retryAfter string, attempt int) time.Duration {
	if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return time.Duration(1<<attempt) * time.Second
}

// GetOne fetches a single resource (e.g. "me") and returns its raw JSON.
func (client *Client) GetOne(ctx context.Context, path string) (json.RawMessage, error) {
	body, _, err := client.request(ctx, path, nil)
	return body, err
}

// GetAll fetches every page of a paginated collection and returns the records
// concatenated into one slice, each still as raw JSON.
func (client *Client) GetAll(ctx context.Context, path string) ([]json.RawMessage, error) {
	var all []json.RawMessage

	for page := 1; ; page++ {
		body, total, err := client.request(ctx, path, url.Values{
			"page[size]":   {strconv.Itoa(pageSize)},
			"page[number]": {strconv.Itoa(page)},
		})
		if err != nil {
			return nil, err
		}

		var records []json.RawMessage
		if err := json.Unmarshal(body, &records); err != nil {
			return nil, fmt.Errorf("expected an array from %s: %w", path, err)
		}
		all = append(all, records...)

		// Stop on a short page, or once we've gathered the advertised total.
		if len(records) < pageSize || (total >= 0 && len(all) >= total) {
			return all, nil
		}
	}
}
