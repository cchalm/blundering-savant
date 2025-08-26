package transport

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

type RateLimitedTransport struct {
	base http.RoundTripper
}

func WithRateLimiting(base http.RoundTripper) *RateLimitedTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &RateLimitedTransport{base: base}
}

func (t *RateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Preserve the original request body for retries
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
		err = req.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to close request body: %w", err)
		}
	}

	for {
		// Restore the request body for each attempt
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, err := t.base.RoundTrip(req)
		if err != nil {
			return resp, err
		}

		// Check for 429 status
		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfterStr := resp.Header.Get("retry-after")
			if retryAfterStr != "" {
				// Parse retry-after header
				var waitDuration time.Duration

				// Try parsing as seconds
				if seconds, err := strconv.Atoi(retryAfterStr); err == nil {
					waitDuration = time.Duration(seconds) * time.Second
				} else if retryTime, err := time.Parse(time.RFC1123, retryAfterStr); err == nil {
					waitDuration = time.Until(retryTime)
				}

				if waitDuration > 0 {
					// Close the response body to free resources
					err = resp.Body.Close()
					if err != nil {
						return nil, fmt.Errorf("failed to close request body: %w", err)
					}

					// Wait for the specified duration
					log.Printf("Rate limited, waiting %s", waitDuration)
					select {
					case <-req.Context().Done():
						return nil, req.Context().Err()
					case <-time.After(waitDuration):
						// Continue the loop to retry
						continue
					}
				}
			}
		}

		// Return response for all other cases
		return resp, err
	}
}
