package checks

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	httpTimeout   = 10 * time.Second
	logBodyLimit  = 2048
	readBodyLimit = 8 << 20
)

// NewHTTPClient 는 타임아웃 10초이고 모든 요청·응답을 로그에 남기는 클라이언트를 만든다.
func NewHTTPClient(log *slog.Logger) *http.Client {
	return &http.Client{
		Timeout:   httpTimeout,
		Transport: &loggingTransport{base: http.DefaultTransport, log: log},
	}
}

type loggingTransport struct {
	base http.RoundTripper
	log  *slog.Logger
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	elapsed := time.Since(start)
	if err != nil {
		t.log.Debug("HTTP 요청 실패", "method", req.Method, "url", req.URL.String(), "elapsed", elapsed, "err", err)
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, readBodyLimit))
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	shown := string(body)
	truncated := false
	if len(shown) > logBodyLimit {
		shown = shown[:logBodyLimit]
		truncated = true
	}
	t.log.Debug("HTTP 응답", "method", req.Method, "url", req.URL.String(), "status", resp.StatusCode,
		"elapsed", elapsed, "body", shown, "truncated", truncated, "read_err", readErr)
	return resp, nil
}
