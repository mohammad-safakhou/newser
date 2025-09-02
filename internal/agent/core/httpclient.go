package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

type HTTPClient struct {
	client  *http.Client
	retries int
	backoff time.Duration
}

func NewHTTPClient(timeout time.Duration, retries int, backoff time.Duration) *HTTPClient {
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	if retries < 0 {
		retries = 0
	}
	if backoff == 0 {
		backoff = 300 * time.Millisecond
	}
	return &HTTPClient{client: &http.Client{Timeout: timeout}, retries: retries, backoff: backoff}
}

func (c *HTTPClient) DoJSON(ctx context.Context, method, url string, headers map[string]string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewBuffer(b)
	}

	var lastErr error
	tries := c.retries + 1
	for attempt := 0; attempt < tries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if body != nil && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if out == nil {
					return nil
				}
				dec := json.NewDecoder(resp.Body)
				if err := dec.Decode(out); err != nil {
					return err
				}
				return nil
			}
			// read response body (best-effort) to include in error
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			lastErr = errors.New(resp.Status + ": " + string(b))
		}

		if attempt < tries-1 {
			select {
			case <-time.After(c.backoff * time.Duration(1<<attempt)):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return lastErr
}
