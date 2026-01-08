package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
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
