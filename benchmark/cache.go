package benchmark

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
)

func WarmCache(req StartBenchmarkRequest) {
	wctx, err := setupWorker(req)
	if err != nil {
		return
	}

	for i := 0; i < 5; i++ {
		r := wctx.req.Clone(context.Background())
		if len(wctx.body) > 0 {
			r.Body = io.NopCloser(bytes.NewReader(wctx.body))
		}
		resp, err := wctx.client.Do(r)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
}

func DetectCacheHit(h http.Header) *bool {
	if v := h.Get("X-Cache"); v != "" {
		hit := strings.Contains(strings.ToUpper(v), "HIT")
		return &hit
	}
	if v := h.Get("CF-Cache-Status"); v != "" {
		hit := v == "HIT"
		return &hit
	}
	if v := h.Get("Age"); v != "" {
		hit := v != "0"
		return &hit
	}
	return nil
}
