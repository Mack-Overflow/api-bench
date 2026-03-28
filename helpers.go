package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func bodySummary(body any) string {
	bytes, err := json.Marshal(body)
	if err != nil {
		return "<invalid body>"
	}

	const max = 200
	if len(bytes) > max {
		return fmt.Sprintf("%s... (%d bytes)", bytes[:max], len(bytes))
	}

	return string(bytes)
}

func percentile(latencies []time.Duration, p float64) time.Duration {
	if len(latencies) == 0 {
		return 0
	}

	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	index := int(math.Ceil((p/100)*float64(len(latencies)))) - 1
	if index < 0 {
		index = 0
	}

	return latencies[index]
}

func redactHeader(key, value string) string {
	switch strings.ToLower(key) {
	case "authorization", "cookie", "x-api-key":
		return "[REDACTED]"
	default:
		return value
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func normalizeJSON(b []byte) []byte {
	if len(b) == 0 {
		return []byte(`{}`)
	}
	return b
}

func toValidJSON(data []byte) string {
	if len(data) == 0 {
		return "{}"
	}
	return string(data)
}

// sanitizeError returns an error message safe for streaming to clients,
// stripping query strings and other potentially sensitive details.
func sanitizeError(err error) string {
	msg := err.Error()
	// Strip query parameters from URLs in error messages
	if idx := strings.Index(msg, "?"); idx != -1 {
		// Find the end of the URL portion
		end := strings.IndexAny(msg[idx:], " \"')")
		if end == -1 {
			msg = msg[:idx]
		} else {
			msg = msg[:idx] + msg[idx+end:]
		}
	}
	return msg
}

func parseRetryAfter(resp *http.Response) time.Duration {
	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		return 2 * time.Second
	}

	// Retry-After can be seconds or HTTP date
	if secs, err := strconv.Atoi(retryAfter); err == nil {
		if secs > 0 {
			return time.Duration(secs) * time.Second
		}
		return 2 * time.Second
	}

	if t, err := http.ParseTime(retryAfter); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}

	return 2 * time.Second
}
