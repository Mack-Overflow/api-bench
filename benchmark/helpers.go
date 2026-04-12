package benchmark

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func Percentile(latencies []time.Duration, p float64) time.Duration {
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

func FormatBytes(b int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
	)
	switch {
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func redactHeader(key, value string) string {
	switch strings.ToLower(key) {
	case "authorization", "cookie", "x-api-key":
		return "[REDACTED]"
	default:
		return value
	}
}

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

func sanitizeError(err error) string {
	msg := err.Error()
	if idx := strings.Index(msg, "?"); idx != -1 {
		end := strings.IndexAny(msg[idx:], " \"')")
		if end == -1 {
			msg = msg[:idx]
		} else {
			msg = msg[:idx] + msg[idx+end:]
		}
	}
	return msg
}

func headLines(body []byte, maxLines int, maxBytes int) string {
	if maxLines <= 0 || maxBytes <= 0 {
		return ""
	}

	if len(body) > maxBytes {
		body = body[:maxBytes]
	}

	scanner := bufio.NewScanner(bytes.NewReader(body))

	const maxScanTokenSize = 256 * 1024
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxScanTokenSize)

	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) >= maxLines {
			break
		}
	}

	return strings.Join(lines, "\n")
}

func parseRetryAfter(resp *http.Response) time.Duration {
	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		return 2 * time.Second
	}

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
