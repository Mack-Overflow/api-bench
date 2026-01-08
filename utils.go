package main

import (
	"bufio"
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"
)

func (r StartBenchmarkRequest) String() string {
	var b strings.Builder

	b.WriteString("BenchmarkRequest{\n")
	b.WriteString(fmt.Sprintf("  URL: %s\n", r.URL))
	b.WriteString(fmt.Sprintf("  Method: %s\n", r.Method))
	b.WriteString(fmt.Sprintf("  Concurrency: %d\n", r.Concurrency))
	b.WriteString(fmt.Sprintf("  DurationSec: %d\n", r.DurationSec))

	if len(r.Headers) > 0 {
		b.WriteString("  Headers:\n")
		keys := sortedKeys(r.Headers)
		for _, k := range keys {
			b.WriteString(fmt.Sprintf("    %s: %s\n", k, redactHeader(k, r.Headers[k])))
		}
	}

	if len(r.Params) > 0 {
		b.WriteString("  Params:\n")
		keys := sortedKeys(r.Params)
		for _, k := range keys {
			b.WriteString(fmt.Sprintf("    %s=%s\n", k, r.Params[k]))
		}
	}

	if r.Body != nil {
		b.WriteString("  Body: ")
		b.WriteString(bodySummary(r.Body))
		b.WriteString("\n")
	}

	b.WriteString("}")

	return b.String()
}

func headLines(body []byte, maxLines int, maxBytes int) string {
	if maxLines <= 0 || maxBytes <= 0 {
		return ""
	}

	// Cap total bytes first
	if len(body) > maxBytes {
		body = body[:maxBytes]
	}

	scanner := bufio.NewScanner(bytes.NewReader(body))

	// Increase default token size (64K) just in case
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

var (
	titleRe = regexp.MustCompile(`(?i)<title>(.*?)</title>`)
	h1Re    = regexp.MustCompile(`(?i)<h1[^>]*>(.*?)</h1>`)
)

func extractHTMLError(body []byte) string {
	html := string(body)

	if m := titleRe.FindStringSubmatch(html); len(m) > 1 {
		return cleanHTML(m[1])
	}
	if m := h1Re.FindStringSubmatch(html); len(m) > 1 {
		return cleanHTML(m[1])
	}

	// fallback: strip all tags
	return cleanHTML(stripTags(html))
}

func cleanHTML(s string) string {
	s = html.UnescapeString(s)
	s = strings.TrimSpace(s)
	return truncate(s, 256)
}

func stripTags(s string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	return re.ReplaceAllString(s, "")
}

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}

	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
