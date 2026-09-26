// Package llm defines the provider-agnostic LLM interface; adapters live in subpackages (PLAN.md §5)
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/egress"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type PartType string

const (
	PartText       PartType = "text"
	PartToolCall   PartType = "tool_call"
	PartToolResult PartType = "tool_result"
	PartReasoning  PartType = "reasoning"
)

// ToolCall is a model's request to run a tool
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// ToolResult answers one tool call
type ToolResult struct {
	CallID  string `json:"callId"`
	Content string `json:"content"`
	IsError bool   `json:"isError,omitempty"`
}

// Reasoning is provider-specific thinking that must be echoed back unchanged on the next turn
type Reasoning struct {
	Provider string          `json:"provider"`
	Opaque   json.RawMessage `json:"opaque,omitempty"`
	// Text is a human-readable summary for the UI, when the provider offers one
	Text string `json:"text,omitempty"`
}

// Part is one piece of a message; exactly one of the content fields is set, matching Type
type Part struct {
	Type       PartType    `json:"type"`
	Text       string      `json:"text,omitempty"`
	ToolCall   *ToolCall   `json:"toolCall,omitempty"`
	ToolResult *ToolResult `json:"toolResult,omitempty"`
	Reasoning  *Reasoning  `json:"reasoning,omitempty"`
}

// Message is one conversation turn
type Message struct {
	Role  Role   `json:"role"`
	Parts []Part `json:"parts"`
}

// Text returns the concatenated text parts of the message
func (m Message) Text() string {
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Type == PartText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// ToolCalls returns the tool calls in the message
func (m Message) ToolCalls() []ToolCall {
	var calls []ToolCall
	for _, p := range m.Parts {
		if p.Type == PartToolCall && p.ToolCall != nil {
			calls = append(calls, *p.ToolCall)
		}
	}
	return calls
}

// TextPart builds a text part
func TextPart(text string) Part { return Part{Type: PartText, Text: text} }

// Block is one system prompt block, ordered from stable to volatile
type Block struct {
	Text string `json:"text"`
	// CacheBreakpoint marks the end of a stable prefix that providers with explicit caching should cache
	CacheBreakpoint bool `json:"cacheBreakpoint,omitempty"`
}

// ToolDef describes a function tool with a JSON Schema for its arguments
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type Effort string

const (
	EffortLow    Effort = "low"
	EffortMedium Effort = "medium"
	EffortHigh   Effort = "high"
)

// Request is one model call
type Request struct {
	Model    string
	System   []Block
	Messages []Message
	Tools    []ToolDef
	// MaxTokens caps the output; zero means the adapter default
	MaxTokens int
	Effort    Effort
	// OutputSchema requests structured JSON output matching the schema, used by Structured
	// Adapters always send it as the provider's native JSON-schema output, so Structured only sets it when Caps.JSONSchema is true and uses a submit tool otherwise
	OutputSchema json.RawMessage
	// OutputName names the structured output, used for the submit-tool fallback
	OutputName string
	// ForceTool makes the model call this tool, used by the submit-tool fallback of Structured
	ForceTool string
}

type DeltaType string

const (
	DeltaText      DeltaType = "text"
	DeltaReasoning DeltaType = "reasoning"
	DeltaToolCall  DeltaType = "tool_call"
)

// Delta is a streamed fragment, used for live UI updates only and never persisted
type Delta struct {
	Type DeltaType `json:"type"`
	Text string    `json:"text,omitempty"`
	// ToolName is set when a tool call starts streaming
	ToolName string `json:"toolName,omitempty"`
}

// Usage is normalized across providers: Input excludes cached tokens
// Reasoning is the part of Output the model spent thinking, so it is informational and never billed on top of Output
type Usage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Reasoning  int64 `json:"reasoning"`
}

// Add returns the sum of two usages
func (u Usage) Add(o Usage) Usage {
	return Usage{Input: u.Input + o.Input, Output: u.Output + o.Output, CacheRead: u.CacheRead + o.CacheRead, CacheWrite: u.CacheWrite + o.CacheWrite, Reasoning: u.Reasoning + o.Reasoning}
}

// Tokens is the token count a workspace that shows usage in tokens sees, input plus output like the runs' tok_in and tok_out, with cache reads and writes left out
func (u Usage) Tokens() int64 {
	return u.Input + u.Output
}

type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopToolUse   StopReason = "tool_use"
	StopMaxTokens StopReason = "max_tokens"
	StopRefusal   StopReason = "refusal"
	StopOther     StopReason = "other"
)

// Response is the complete result of one model call
type Response struct {
	Message Message
	Stop    StopReason
	Usage   Usage
	Latency time.Duration
	// Model is the model that actually answered, as reported by the provider
	Model string
}

// Caps describes what a model supports
type Caps struct {
	Tools         bool `json:"tools"`
	ParallelTools bool `json:"parallelTools"`
	Reasoning     bool `json:"reasoning"`
	JSONSchema    bool `json:"jsonSchema"`
	PromptCache   bool `json:"promptCache"`
	Vision        bool `json:"vision"`
	Context       int  `json:"context"`
}

// Provider is implemented by every LLM adapter
type Provider interface {
	// Stream performs one model call, reporting fragments to onDelta as they arrive; onDelta may be nil
	Stream(ctx context.Context, req Request, onDelta func(Delta)) (*Response, error)
	// Caps returns the capabilities of a model
	Caps(model string) Caps
}

// ListedModel is a model a server lists, with whatever metadata it reports; zero and nil fields are unknown
type ListedModel struct {
	ID    string
	Label string
	// Context is the context window in tokens
	Context    int
	Price      *Price
	Tools      *bool
	Reasoning  *bool
	JSONSchema *bool
	Vision     *bool
}

// ModelLister is implemented by adapters whose server can list the models it serves
type ModelLister interface {
	ListModels(ctx context.Context) ([]ListedModel, error)
}

// Config configures one provider instance
type Config struct {
	BaseURL string
	APIKey  string
	// HTTPClient makes the provider's requests, so the egress guard can check every address it connects to; nil uses the SDK default
	HTTPClient *http.Client
}

// Limits of what one response from a provider may hold, counting decompressed bytes, so a malicious or broken endpoint can't make the server buffer without bound
// A long streamed answer takes a few dozen megabytes of events at most, and an error body is only read for its message
const (
	maxResponseBytes = 64 << 20
	maxErrorBytes    = 1 << 20
)

// MaxAnswerBytes bounds the text, reasoning and tool arguments of one answer, several times what the largest output limit of any current model allows
// Adapters stop reading a stream that passes it, since small events well within the response limit would otherwise pile up and get copied several times
const MaxAnswerBytes = 4 << 20

// ErrAnswerTooLarge is returned for an answer that passes MaxAnswerBytes
var ErrAnswerTooLarge = errors.New("the answer is larger than any model writes in one call")

// MaxListedModels bounds the models one server's list may hold, over ten times what the largest routers serve
// Adapters refuse a longer list before decoding its entries, since tiny entries well within the response limit would otherwise take a gigabyte of memory
const MaxListedModels = 5_000

// LimitedHTTPClient returns a copy of c, or of a default client when c is nil, whose responses fail once they grow past the limits
// Adapters make every request through it, since the SDKs read whole bodies and accumulate streams without a limit of their own
func LimitedHTTPClient(c *http.Client) *http.Client {
	if c == nil {
		c = &http.Client{}
	}
	limited := *c
	limited.Transport = cutOffTransport{base: egress.LimitResponses(c.Transport, maxResponseBytes, maxErrorBytes)}
	return &limited
}

// cutOffTransport marks every response body that breaks off partway with ErrIncompleteStream, so IsTransient retries it
// How a dropped response surfaces depends on the protocol, and an HTTP/2 stream reset or GOAWAY matches none of the network errors IsTransient knows
type cutOffTransport struct {
	base http.RoundTripper
}

func (t cutOffTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = cutOffBody{ReadCloser: resp.Body}
	return resp, nil
}

type cutOffBody struct {
	io.ReadCloser
}

func (b cutOffBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)

	// The end of the body, a body past its limit and a canceled call keep their own errors, since none of them is a dropped response
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, egress.ErrResponseTooLarge) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return n, err
	}
	return n, fmt.Errorf("%w: %w", ErrIncompleteStream, err)
}

// Provider kinds stored in the providers table
const (
	KindAnthropic = "anthropic"
	KindOpenAI    = "openai"
	KindFake      = "fake"
)

// ErrRefusal is returned by Structured when the model refused to answer
var ErrRefusal = errors.New("model refused to answer")

// ErrIncompleteStream means the provider closed the stream before the message ended, which usually succeeds on a second try
var ErrIncompleteStream = errors.New("stream ended before the message was complete")

// IsTransient reports whether repeating the same call may succeed, such as after a dropped stream, an overloaded server or a network failure
func IsTransient(err error) bool {
	if errors.Is(err, ErrIncompleteStream) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	// A refused egress or a host that doesn't exist fails the same way on every attempt
	if errors.Is(err, egress.ErrBlocked) {
		return false
	}
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok && dnsErr.IsNotFound {
		return false
	}

	// A connection that failed, timed out or closed before the response is a network problem, not a problem with the request
	if errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return true
	}
	if _, ok := errors.AsType[*net.OpError](err); ok {
		return true
	}
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		return apiErr.Retryable()
	}
	return false
}

// APIError is an error response from a provider API, normalized across adapters
type APIError struct {
	Provider   string
	StatusCode int
	// Type is the provider's error type, such as rate_limit_error, when it reports one
	Type    string
	Message string
	// InStream marks an error event sent inside a response stream, after the provider may already have generated and billed part of the answer
	InStream bool
	Err      error
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("%s API error (status %d", e.Provider, e.StatusCode)
	if e.Type != "" {
		msg += ", " + e.Type
	}
	msg += ")"
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

func (e *APIError) Unwrap() error { return e.Err }

// Retryable reports whether the same call may succeed later, such as after a rate limit or an overloaded server
func (e *APIError) Retryable() bool {
	switch {
	case e.StatusCode == 408, e.StatusCode == 409, e.StatusCode == 429, e.StatusCode >= 500:
		return true
	// Errors sent inside an event stream carry no status, so Anthropic's and OpenAI's error types decide
	case e.Type == "overloaded_error", e.Type == "rate_limit_error", e.Type == "api_error", e.Type == "server_error":
		return true
	}
	return false
}

// Unbilled reports whether a failed call is known to cost nothing, because the request never reached the provider or the provider answered with an error status before generating anything
// Any other failure, such as a dropped stream, a timeout or an error event inside the stream, may come after the provider started billing the call
func Unbilled(err error) bool {
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		return !apiErr.InStream && apiErr.StatusCode >= 400
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return true
	}
	if opErr, ok := errors.AsType[*net.OpError](err); ok && opErr.Op == "dial" {
		return true
	}
	return errors.Is(err, egress.ErrBlocked)
}

// Price is in micro-USD per 1M tokens
type Price struct {
	In         int64 `json:"in"`
	Out        int64 `json:"out"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

// Cost returns the cost of the usage in micro-USD
func Cost(u Usage, p Price) int64 {
	total := u.Input*p.In + u.Output*p.Out + u.CacheRead*p.CacheRead + u.CacheWrite*p.CacheWrite
	return total / 1_000_000
}
