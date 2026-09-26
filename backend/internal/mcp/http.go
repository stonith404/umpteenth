package mcp

import (
	"mime"
	"net/http"
)

// maxHTTPResponseBytes matches the SDK's default limit for a single SSE event or stdio message
const maxHTTPResponseBytes = 16 << 20

// responseLimitTransport bounds bodies the SDK reads into memory in one piece
type responseLimitTransport struct {
	base http.RoundTripper
}

func (t *responseLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Successful SSE streams are long-lived and already have an SDK limit per event, while even SSE error bodies are read in full
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || mediaType != "text/event-stream" {
		resp.Body = http.MaxBytesReader(nil, resp.Body, maxHTTPResponseBytes)
	}
	return resp, nil
}
