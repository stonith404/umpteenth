//go:build unit

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/anthropic"
)

// boundThinkingAPI is a Messages API that binds every thinking block to the conversation that produced it, as Claude Opus 5.5 and Claude Fable 5.1 do for accounts created on or after 2026-08-31
// A thinking block's signature records the system prompt, the tools and every earlier message without their thinking blocks, and a replayed block whose prefix changed is rejected with a 400
type boundThinkingAPI struct {
	t       *testing.T
	mu      sync.Mutex
	replies []boundReply
	calls   int
	// rejected holds the error message of every request the check refused
	rejected []string
	// shapes records the role and block types of every request's messages, for failure output
	shapes []string
}

// boundReply is one scripted answer: an optional leading thinking block, then the given text or tool call, with the given prompt size
type boundReply struct {
	thinking    bool
	text        string
	tool        string
	toolArgs    string
	inputTokens int
	overflow    bool
}

func (a *boundThinkingAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	require.NoError(a.t, err)
	var req map[string]any
	require.NoError(a.t, json.Unmarshal(body, &req))
	messages, _ := req["messages"].([]any)

	a.mu.Lock()
	defer a.mu.Unlock()
	a.shapes = append(a.shapes, messageShape(messages))

	// Every replayed thinking block must carry the signature of the prefix it now follows
	for i, m := range messages {
		msg := m.(map[string]any)
		blocks, _ := msg["content"].([]any)
		for j, b := range blocks {
			block := b.(map[string]any)
			if block["type"] != "thinking" {
				continue
			}
			if block["signature"] != prefixSignature(req, messages[:i], blocks[:j]) {
				text := fmt.Sprintf("messages.%d.content.%d: Invalid `signature` in `thinking` block. The block is bound to a different conversation. Remove the block, or set `thinking.block_binding.prefix_mismatch_behavior` to \"drop_block\". That setting requires the `thinking-binding-controls-2026-08-01` value in the `anthropic-beta` header.", i, j)
				a.rejected = append(a.rejected, text)
				writeAPIError(w, text)
				return
			}
		}
	}

	// Answer with the next scripted reply, signing its thinking block with the prefix it was produced after
	require.Less(a.t, a.calls, len(a.replies), "unexpected model call %d", a.calls+1)
	reply := a.replies[a.calls]
	a.calls++
	if reply.overflow {
		writeAPIError(w, "prompt is too long: 1000500 tokens > 1000000 maximum")
		return
	}
	var content []string
	if reply.thinking {
		content = append(content, fmt.Sprintf(`{"type":"thinking","thinking":"planning","signature":%q}`, prefixSignature(req, messages, nil)))
	}
	stop := "end_turn"
	if reply.text != "" {
		content = append(content, fmt.Sprintf(`{"type":"text","text":%q}`, reply.text))
	}
	if reply.tool != "" {
		args := reply.toolArgs
		if args == "" {
			args = "{}"
		}
		content = append(content, fmt.Sprintf(`{"type":"tool_use","id":"toolu_%d","name":%q,"input":%s}`, a.calls, reply.tool, args))
		stop = "tool_use"
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, sseMessage(fmt.Sprint(req["model"]), reply.inputTokens, stop, content))
}

// prefixSignature hashes what a thinking block is bound to: the system prompt, the tools, the earlier messages and the blocks before it in its own message
// Thinking blocks and cache_control markers are left out, since earlier thinking isn't part of the prefix and cache markers move every turn
func prefixSignature(req map[string]any, messages []any, before []any) string {
	var prefix []any
	for _, m := range messages {
		msg := m.(map[string]any)
		blocks, _ := msg["content"].([]any)
		prefix = append(prefix, map[string]any{"role": msg["role"], "content": withoutThinking(blocks)})
	}
	prefix = append(prefix, map[string]any{"role": "assistant", "content": withoutThinking(before)})
	raw, _ := json.Marshal(withoutCacheControl([]any{req["system"], req["tools"], prefix}))
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func withoutThinking(blocks []any) []any {
	out := []any{}
	for _, b := range blocks {
		if t := b.(map[string]any)["type"]; t != "thinking" && t != "redacted_thinking" {
			out = append(out, b)
		}
	}
	return out
}

func withoutCacheControl(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if k != "cache_control" {
				out[k] = withoutCacheControl(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = withoutCacheControl(val)
		}
		return out
	}
	return v
}

func messageShape(messages []any) string {
	var parts []string
	for _, m := range messages {
		msg := m.(map[string]any)
		blocks, _ := msg["content"].([]any)
		var types []string
		for _, b := range blocks {
			types = append(types, fmt.Sprint(b.(map[string]any)["type"]))
		}
		parts = append(parts, fmt.Sprintf("%s[%s]", msg["role"], strings.Join(types, ",")))
	}
	return strings.Join(parts, " ")
}

func writeAPIError(w http.ResponseWriter, message string) {
	body, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]any{"type": "invalid_request_error", "message": message}})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write(body)
}

// sseMessage renders one streamed Messages API answer with the given complete content blocks
func sseMessage(model string, inputTokens int, stop string, blocks []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":%q,\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":%d,\"cache_creation_input_tokens\":0,\"cache_read_input_tokens\":0,\"output_tokens\":1}}}\n\n", model, inputTokens)
	for i, block := range blocks {
		fmt.Fprintf(&b, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":%s}\n\n", i, block)
		fmt.Fprintf(&b, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", i)
	}
	fmt.Fprintf(&b, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q,\"stop_sequence\":null},\"usage\":{\"output_tokens\":20}}\n\n", stop)
	b.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return b.String()
}

func TestCompactionKeepsThinkingBoundToTheConversationValid(t *testing.T) {
	read := boundReply{thinking: true, tool: "reader", inputTokens: 1_000}
	finish := boundReply{thinking: true, tool: "finish", toolArgs: `{"status":"success","summary":"done"}`, inputTokens: 1_000}
	summary := boundReply{thinking: true, text: "## Goal\nRead the files\n## Next steps\nFinish", inputTokens: 3_000}

	tests := []struct {
		name          string
		contextWindow int
		replies       []boundReply
	}{
		{
			// The third turn's prompt passes 70% of the window, so the conversation is summarized before the fourth turn
			name:          "compaction near the window",
			contextWindow: 10_000,
			replies:       []boundReply{read, read, {thinking: true, tool: "reader", inputTokens: 9_000}, summary, finish},
		},
		{
			// The window is unknown, so only the provider's refusal of the third turn triggers the compaction and the retry
			name:    "compaction after the provider rejects a long prompt",
			replies: []boundReply{read, read, {overflow: true}, summary, finish},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api := &boundThinkingAPI{t: t, replies: tc.replies}
			srv := httptest.NewServer(api)
			defer srv.Close()
			p, err := anthropic.New(llm.Config{APIKey: "test", BaseURL: srv.URL})
			require.NoError(t, err)
			obs := &compactionRecorder{}

			out := Run(context.Background(), Config{
				Provider: p, Model: "claude-opus-5-5", Effort: llm.EffortMedium,
				Tools: []Tool{&slowTool{name: "reader", readOnly: true}, FinishTool()}, Observer: obs,
				ContextWindow: tc.contextWindow, RetryBackoff: []time.Duration{},
			}, userMsg("read the files"))

			api.mu.Lock()
			defer api.mu.Unlock()
			for i, shape := range api.shapes {
				t.Logf("request %d: %s", i+1, shape)
			}
			t.Logf("run status %s: %s", out.Status, out.Reason)
			require.Len(t, obs.seen, 1, "the conversation should have been compacted once")
			require.NoError(t, obs.seen[0].Err)
			require.Empty(t, api.rejected, "a request after the compaction replayed a thinking block bound to the conversation before it")
			require.Equal(t, StatusSucceeded, out.Status, out.Reason)
		})
	}
}
