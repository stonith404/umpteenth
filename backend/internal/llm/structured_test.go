//go:build unit

package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
)

var verdictSchema = json.RawMessage(`{"type":"object","properties":{"passed":{"type":"boolean"},"reason":{"type":"string"}},"required":["passed","reason"]}`)

type verdict struct {
	Passed bool   `json:"passed"`
	Reason string `json:"reason"`
}

func verifyRequest() llm.Request {
	return llm.Request{
		Model:        "fake-model",
		System:       []llm.Block{{Text: "You verify runs.", CacheBreakpoint: true}},
		Messages:     []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart("Did the run pass?")}}},
		OutputSchema: verdictSchema,
		OutputName:   "verdict",
	}
}

func TestStructuredNative(t *testing.T) {
	p := fake.New()
	p.Enqueue(fake.ScriptedResponse{Text: "```json\n{\"passed\":true,\"reason\":\"all green\"}\n```"})

	var out verdict
	resp, err := llm.Structured(context.Background(), p, verifyRequest(), &out)
	require.NoError(t, err)
	assert.Equal(t, verdict{Passed: true, Reason: "all green"}, out)
	assert.Equal(t, llm.StopEndTurn, resp.Stop)

	// The native path passes the schema through untouched and adds no tools
	sent := p.Requests()[0]
	assert.JSONEq(t, string(verdictSchema), string(sent.OutputSchema))
	assert.Empty(t, sent.Tools)
	assert.Empty(t, sent.ForceTool)
}

func TestStructuredNativeRetriesOnce(t *testing.T) {
	p := fake.New()
	p.Enqueue(
		fake.ScriptedResponse{Text: "The run passed.", Usage: llm.Usage{Input: 10, Output: 5}},
		fake.ScriptedResponse{Text: `{"passed":true,"reason":"retried"}`, Usage: llm.Usage{Input: 20, Output: 7}},
	)

	var out verdict
	resp, err := llm.Structured(context.Background(), p, verifyRequest(), &out)
	require.NoError(t, err)
	assert.Equal(t, "retried", out.Reason)

	// Usage covers both attempts so the retry is accounted for
	assert.Equal(t, llm.Usage{Input: 30, Output: 12}, resp.Usage)

	// The retry replays the failed answer and explains the error in a user turn
	reqs := p.Requests()
	require.Len(t, reqs, 2)
	retry := reqs[1].Messages
	require.Len(t, retry, 3)
	assert.Equal(t, "The run passed.", retry[1].Text())
	assert.Equal(t, llm.RoleUser, retry[2].Role)
	assert.Contains(t, retry[2].Text(), "not valid JSON")
}

func TestStructuredGivesUpAfterRetry(t *testing.T) {
	p := fake.New()
	p.Enqueue(fake.ScriptedResponse{Text: "nope"}, fake.ScriptedResponse{Text: "still nope"})

	var out verdict
	resp, err := llm.Structured(context.Background(), p, verifyRequest(), &out)
	require.ErrorContains(t, err, "after retry")
	require.NotNil(t, resp)
	assert.Len(t, p.Requests(), 2)
}

func TestStructuredKeepsTheUsageOfAFailedRetry(t *testing.T) {
	p := fake.New()
	p.Enqueue(fake.ScriptedResponse{Text: "The run passed.", Usage: llm.Usage{Input: 10, Output: 5}}, fake.ScriptedResponse{Error: "stream ended"})

	var out verdict
	resp, err := llm.Structured(context.Background(), p, verifyRequest(), &out)
	require.ErrorContains(t, err, "stream ended")

	// The first attempt was paid for, so its usage comes back with the error
	require.NotNil(t, resp)
	assert.Equal(t, llm.Usage{Input: 10, Output: 5}, resp.Usage)
	assert.Len(t, p.Requests(), 2)
}

func TestStructuredFallback(t *testing.T) {
	p := fake.New()
	p.SetCaps(llm.Caps{Tools: true})
	p.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "submit_verdict", Args: map[string]any{"passed": false, "reason": "tests failed"}}}})

	var out verdict
	_, err := llm.Structured(context.Background(), p, verifyRequest(), &out)
	require.NoError(t, err)
	assert.Equal(t, verdict{Passed: false, Reason: "tests failed"}, out)

	// The schema moves into a forced submit tool and the instruction is appended to the last user turn
	sent := p.Requests()[0]
	require.Len(t, sent.Tools, 1)
	assert.Equal(t, "submit_verdict", sent.Tools[0].Name)
	assert.JSONEq(t, string(verdictSchema), string(sent.Tools[0].Schema))
	assert.Equal(t, "submit_verdict", sent.ForceTool)
	assert.Empty(t, sent.OutputSchema)
	require.Len(t, sent.Messages, 1)
	assert.Len(t, sent.Messages[0].Parts, 2)
	assert.Contains(t, sent.Messages[0].Parts[1].Text, "submit_verdict")
}

func TestStructuredFallbackAnswersEveryToolCallOnRetry(t *testing.T) {
	p := fake.New()
	p.SetCaps(llm.Caps{Tools: true})
	p.Enqueue(
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "bash", Args: map[string]any{"command": "ls"}}, {Name: "submit_verdict", Args: map[string]any{"passed": "yes"}}}},
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "submit_verdict", Args: map[string]any{"passed": true, "reason": "ok"}}}},
	)

	req := verifyRequest()
	var out verdict
	_, err := llm.Structured(context.Background(), p, req, &out)
	require.NoError(t, err)
	assert.True(t, out.Passed)

	// Every call of the failed attempt gets an error result, as providers require
	retry := p.Requests()[1].Messages
	last := retry[len(retry)-1]
	var results []*llm.ToolResult
	for _, part := range last.Parts {
		if part.Type == llm.PartToolResult {
			results = append(results, part.ToolResult)
		}
	}
	require.Len(t, results, 2)
	assert.Equal(t, "fake_call_1", results[0].CallID)
	assert.True(t, results[0].IsError)
	assert.Equal(t, "fake_call_2", results[1].CallID)
	assert.Contains(t, results[1].Content, "submit_verdict")

	// The caller's request is not mutated
	assert.Len(t, req.Messages, 1)
	assert.Len(t, req.Messages[0].Parts, 1)
	assert.Empty(t, req.Tools)
}

func TestStructuredRefusal(t *testing.T) {
	p := fake.New()
	p.Enqueue(fake.ScriptedResponse{Stop: llm.StopRefusal})

	var out verdict
	resp, err := llm.Structured(context.Background(), p, verifyRequest(), &out)
	require.ErrorIs(t, err, llm.ErrRefusal)
	assert.Equal(t, llm.StopRefusal, resp.Stop)
	assert.Len(t, p.Requests(), 1, "a refusal is not retried")
}

func TestStructuredRequiresSchema(t *testing.T) {
	req := verifyRequest()
	req.OutputSchema = nil
	_, err := llm.Structured(context.Background(), fake.New(), req, nil)
	require.Error(t, err)
}

func TestSubmitToolName(t *testing.T) {
	assert.Equal(t, "submit_result", llm.SubmitToolName(""))
	assert.Equal(t, "submit_success_criteria", llm.SubmitToolName("success criteria"))
}

func TestIsTransientCoversNetworkFailures(t *testing.T) {
	assert.True(t, llm.IsTransient(fmt.Errorf("post: %w", io.EOF)))
	assert.True(t, llm.IsTransient(fmt.Errorf("post: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")})))
	assert.True(t, llm.IsTransient(fmt.Errorf("read: %w", syscall.ECONNRESET)))
	assert.True(t, llm.IsTransient(&llm.APIError{StatusCode: 529}))
	assert.True(t, llm.IsTransient(&llm.APIError{Type: "server_error"}), "an OpenAI error event has a type but no status")
	assert.False(t, llm.IsTransient(&llm.APIError{StatusCode: 400}))
	assert.False(t, llm.IsTransient(errors.New("the answer is not valid JSON")))
	assert.False(t, llm.IsTransient(fmt.Errorf("post: %w", &net.OpError{Op: "dial", Net: "tcp", Err: fmt.Errorf("egress to 10.0.0.1: %w", egress.ErrBlocked)})), "a refused egress fails the same way every time")
	assert.False(t, llm.IsTransient(fmt.Errorf("post: %w", &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true}})))
}

func TestUnbilledOnlyForCallsThatNeverStartedAnAnswer(t *testing.T) {
	// An error status comes before the provider generates anything
	assert.True(t, llm.Unbilled(&llm.APIError{StatusCode: 400}))
	assert.True(t, llm.Unbilled(fmt.Errorf("structured: %w", &llm.APIError{StatusCode: 429})))
	assert.True(t, llm.Unbilled(&llm.APIError{StatusCode: 529}))

	// A request that never left the host can't have been billed
	assert.True(t, llm.Unbilled(fmt.Errorf("post: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")})))
	assert.True(t, llm.Unbilled(fmt.Errorf("post: %w", &net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true})))
	assert.True(t, llm.Unbilled(fmt.Errorf("egress to 10.0.0.1: %w", egress.ErrBlocked)))

	// Anything after the answer may have started could have been billed
	assert.False(t, llm.Unbilled(&llm.APIError{StatusCode: 400, InStream: true}), "an error event inside the stream may follow billed output")
	assert.False(t, llm.Unbilled(&llm.APIError{StatusCode: 200, Type: "overloaded_error"}), "Anthropic reports stream errors with the stream's own status")
	assert.False(t, llm.Unbilled(&net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}))
	assert.False(t, llm.Unbilled(context.Canceled))
	assert.False(t, llm.Unbilled(fmt.Errorf("anthropic: %w", llm.ErrIncompleteStream)))
}
