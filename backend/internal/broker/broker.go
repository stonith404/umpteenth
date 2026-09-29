// Package broker is the sandbox-facing API the ump CLI talks to; credentials stay on the host
package broker

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/mcp"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// RunLookup resolves a broker token to its live run
type RunLookup interface {
	RunIDByBrokerToken(ctx context.Context, tokenHash string) (string, error)
}

type Dependencies struct {
	Runs  RunLookup
	Live  *runner.Registry
	State runner.StateStores
	// Blocked are the ranges the operator blocks, which the egress proxy never connects to
	Blocked []netip.Prefix
	// ContainerAddress reports whether an address belongs to a container of the sandbox backend, such as another run's sandbox, which the egress proxy never connects to either
	ContainerAddress func(netip.Addr) bool
}

// Broker serves the per-run API for the ump CLI
type Broker struct {
	deps Dependencies
	// dialer connects the egress proxy to allowed hosts; tests replace it to reach local servers
	dialer func(allowPrivate bool) dialFunc
	// sniffTimeout bounds how long a new connection may take to send its first byte, which tells SOCKS5 from HTTP
	sniffTimeout time.Duration
	// socksHandshakeTimeout bounds the SOCKS5 negotiation, so a client that goes silent can't hold its connection open
	socksHandshakeTimeout time.Duration
}

func New(deps Dependencies) *Broker {
	// Beyond what the job's network settings allow, the proxy stays off the operator's blocked ranges and off containers, which this process reaches but a sandbox must not
	offLimits := func(addr netip.Addr) bool {
		return egress.InRanges(addr, deps.Blocked) || (deps.ContainerAddress != nil && deps.ContainerAddress(egress.Reached(addr)))
	}
	return &Broker{deps: deps, dialer: guardedDialer(offLimits), sniffTimeout: defaultSniffTimeout, socksHandshakeTimeout: defaultSOCKSHandshakeTimeout}
}

// Handler returns the broker's HTTP handler
func (b *Broker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/mcp/tools", b.auth(b.mcpTools))
	mux.HandleFunc("POST /v1/mcp/call", b.auth(b.mcpCall))
	mux.HandleFunc("POST /v1/llm", b.auth(b.llmCall))
	mux.HandleFunc("GET /v1/state", b.auth(b.stateList))
	mux.HandleFunc("GET /v1/state/{key}", b.auth(b.stateGet))
	mux.HandleFunc("PUT /v1/state/{key}", b.auth(b.stateSet))
	mux.HandleFunc("POST /v1/output", b.auth(b.output))
	mux.HandleFunc("POST /v1/step", b.auth(b.step))
	mux.HandleFunc("POST /v1/fail", b.auth(b.fail))
	mux.HandleFunc("POST /v1/summary", b.auth(b.setSummary))
	api := httpserver.RequestIDMiddleware(mux)

	// Proxy requests name their target, a CONNECT with host:port or a request with an absolute URL, and go to the egress proxy instead of the API
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect || r.URL.IsAbs() {
			b.proxy(w, r)
			return
		}
		api.ServeHTTP(w, r)
	})
}

// auth resolves the run from the bearer token and puts the call on the run's timeline, within the run's limits
func (b *Broker) auth(next func(w http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			httpserver.WriteHTTPError(w, r, apperror.InvalidToken())
			return
		}
		live, err := b.liveRun(r.Context(), strings.TrimSpace(token))
		if err != nil {
			httpserver.WriteHTTPError(w, r, err)
			return
		}

		// Each request can hold its body and what it reads in the control plane's memory, so a sandbox's requests beyond a few wait for their turn
		// Past a bounded queue they are turned away, and a client that gave up while waiting has nobody left to answer
		release, err := live.AcquireRequest(r.Context())
		if errors.Is(err, runner.ErrTooManyRequests) {
			httpserver.WriteHTTPError(w, r, apperror.RateLimited(time.Second))
			return
		} else if err != nil {
			return
		}
		defer release()

		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		start := time.Now()
		res, err := next(w, r, live)
		ms := time.Since(start).Milliseconds()

		payload := map[string]any{"endpoint": clip(r.Method + " " + r.URL.Path), "ok": err == nil}
		if err != nil {
			payload["error"] = clip(err.Error())
		}
		if detail, ok := res.(interface{ timelineDetail() map[string]any }); ok {
			maps.Copy(payload, detail.timelineDetail())
		}
		keep := false
		if kept, ok := res.(keptCall); ok {
			keep = kept.keptOnTimeline()
		}
		emitCall(r.Context(), live, events.Event{Type: events.TypeBrokerCall, Ms: &ms, Payload: payload}, keep)

		if err != nil {
			httpserver.WriteHTTPError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if streamed, ok := res.(streamedResponse); ok {
			_ = streamed.writeJSON(w)
			return
		}
		_ = newEncoder(w).Encode(res)
	}
}

// keptCall is a response to a call that stays on the timeline past the run's limit of broker calls
type keptCall interface{ keptOnTimeline() bool }

// streamedResponse is a response written out piece by piece, since encoding all of it at once would hold several copies of it in memory
type streamedResponse interface{ writeJSON(w io.Writer) error }

// newEncoder returns the JSON encoder of broker responses
// HTML escaping would turn every <, > and & a sandbox stored into six bytes, multiplying the size of a response
func newEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}

// emitCall puts a broker call on the run's timeline
// Every request a sandbox sends would otherwise add a row to the database, so a run keeps its first calls and notes once that later ones are left out
// A kept call is recorded past the limit too, so a flood of cheap calls can't push the calls that matter off the timeline
func emitCall(ctx context.Context, live *runner.LiveRun, e events.Event, kept bool) {
	record, marker := live.BrokerEvent()
	if marker {
		message := fmt.Sprintf("The run made more than %d broker calls, so the timeline leaves out later ones apart from MCP calls that may change something, paid ump llm calls and the first state write", runner.MaxBrokerEvents)
		live.Recorder.Emit(ctx, events.Event{Type: events.TypeLog, Payload: map[string]any{"message": message}})
	}
	if record || kept {
		live.Recorder.Emit(ctx, e)
	}
}

// liveRun resolves a broker token to the live run on this replica it belongs to
func (b *Broker) liveRun(ctx context.Context, token string) (*runner.LiveRun, error) {
	hash := crypto.HashToken(token)

	// A shadow run of a proposed main script has no run row, so only the registry of the replica running it knows its token
	if live, ok := b.deps.Live.ShadowByToken(hash); ok {
		return live, nil
	}
	runID, err := b.deps.Runs.RunIDByBrokerToken(ctx, hash)
	if err != nil {
		return nil, apperror.InvalidToken()
	}

	// The adapter routes the sandbox to the replica executing the run, which is where its live state is
	live, ok := b.deps.Live.Get(runID)
	if !ok {
		return nil, apperror.Conflict("The run is not executing on this replica")
	}
	return live, nil
}

// state returns the job state a run's ump state calls read and write
func (b *Broker) state(live *runner.LiveRun) runner.JobState {
	if live.State != nil {
		return live.State
	}
	return b.deps.State.ForJob(live.Run.JobID)
}

// clip shortens text a sandbox controls before it is recorded, so requests with megabytes of path or tool name can't fill the timeline storage
func clip(s string) string {
	out, _ := agent.Truncate(s, 500, 0)
	return out
}

func decode(r *http.Request, into any) error {
	err := json.NewDecoder(r.Body).Decode(into)
	if err != nil {
		return apperror.InvalidRequestBody(err)
	}
	return nil
}

type toolDto struct {
	Server      string          `json:"server"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	ReadOnly    bool            `json:"readOnly"`
}

func (b *Broker) mcpTools(_ http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error) {
	server := r.URL.Query().Get("server")
	out := []toolDto{}
	for _, t := range live.Tools() {
		mt, ok := t.(*mcp.AgentTool)
		if !ok || (server != "" && mt.Server != server) {
			continue
		}
		out = append(out, toolDto{Server: mt.Server, Name: mt.Info.Name, Description: mt.Info.Description, InputSchema: mt.Info.InputSchema, ReadOnly: mt.Info.ReadOnly})
	}
	slices.SortFunc(out, func(a, b toolDto) int {
		return cmp.Or(strings.Compare(a.Server, b.Server), strings.Compare(a.Name, b.Name))
	})
	return out, nil
}

type mcpCallRequest struct {
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

type mcpCallResponse struct {
	Content string `json:"content"`
	IsError bool   `json:"isError"`
	server  string
	tool    string
	// action is set when the run noted the call as one that may have changed something
	action bool
}

func (r mcpCallResponse) timelineDetail() map[string]any {
	content, _ := agent.Truncate(r.Content, 2000, 1000)
	return map[string]any{"server": r.server, "tool": r.tool, "isError": r.IsError, "result": content}
}

func (r mcpCallResponse) keptOnTimeline() bool { return r.action }

// mcpCall runs an MCP tool for a script through the same host-side session the agent uses, so the host sees every call and records the ones that may change something
func (b *Broker) mcpCall(_ http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error) {
	var req mcpCallRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	tool, ok := live.Tool(mcp.ToolName(req.Server, req.Tool))
	if !ok {
		return nil, apperror.NotFound(fmt.Sprintf("MCP tool %s on server %s", req.Tool, req.Server))
	}
	args, _ := agent.Truncate(string(req.Arguments), 300, 0)

	// A shadow run tries out a main script, so it must never change anything outside the sandbox
	if live.Shadow && !tool.ReadOnly() {
		live.Refuse(fmt.Sprintf("MCP tool %s.%s with %s", req.Server, req.Tool, args))
		return nil, apperror.Forbidden("A shadow run of the main script can't call MCP tools that may have side effects")
	}
	res := tool.Run(r.Context(), llm.ToolCall{ID: "broker", Name: tool.Def().Name, Args: req.Arguments})

	// Calls that may change something elsewhere are remembered, so an agent taking over a failed script doesn't repeat them
	// The remembered ones also stay on the timeline, however many other calls the run makes
	action := false
	if !tool.ReadOnly() {
		outcome := "succeeded"
		if res.IsError {
			outcome = "failed"
		}
		action = live.RecordAction(fmt.Sprintf("MCP tool %s.%s with %s (%s)", req.Server, req.Tool, args, outcome))
	}
	return mcpCallResponse{Content: res.Content, IsError: res.IsError, server: req.Server, tool: req.Tool, action: action}, nil
}

// Output token limits of one ump llm call
const (
	defaultLLMTokens = 4096
	maxLLMTokens     = 16000
)

type llmRequest struct {
	// Model is "utility" (default) or "agent"
	Model     string          `json:"model"`
	System    string          `json:"system"`
	Prompt    string          `json:"prompt"`
	Schema    json.RawMessage `json:"schema"`
	MaxTokens int             `json:"maxTokens"`
}

type llmResponse struct {
	Text  string          `json:"text,omitempty"`
	JSON  json.RawMessage `json:"json,omitempty"`
	Usage llm.Usage       `json:"usage"`
	Cost  int64           `json:"cost"`
	model string
}

func (r llmResponse) timelineDetail() map[string]any {
	return map[string]any{"model": r.model, "usage": r.Usage, "cost": r.Cost}
}

// keptOnTimeline keeps paid calls on the timeline, since the live run page adds up the run's cost from them and the run's cost limit already bounds how many there are
func (r llmResponse) keptOnTimeline() bool { return r.Cost > 0 }

// llmCall makes one bounded LLM call without tools, the building block of scripted runs' judgment steps
func (b *Broker) llmCall(_ http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error) {
	var req llmRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, apperror.InvalidField("prompt", "required", "is required")
	}

	// A schema that isn't an object would only fail inside the adapter, after the call was charged its reservation
	if len(req.Schema) > 0 && !bytes.HasPrefix(bytes.TrimSpace(req.Schema), []byte("{")) {
		return nil, apperror.InvalidField("schema", "invalid", "must be a JSON Schema object")
	}

	provider, model := live.Utility, live.UtilModel
	if req.Model == "agent" {
		provider, model = live.Provider, live.Model
	}
	maxTokens := min(req.MaxTokens, maxLLMTokens)
	if maxTokens <= 0 {
		maxTokens = defaultLLMTokens
	}
	llmReq := llm.Request{Model: model.Name, MaxTokens: maxTokens, Effort: llm.EffortLow,
		Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart(req.Prompt)}}}}
	if req.System != "" {
		llmReq.System = []llm.Block{{Text: req.System}}
	}

	// Scripts calling ump llm in a loop or in parallel must not get around the run's cost limit
	// Tokens are estimated from bytes on the generous side, since most text needs several bytes per token
	est := int64(len(req.Prompt)+len(req.System)+len(req.Schema)) / 2
	worst := llm.Cost(llm.Usage{Input: est, Output: int64(maxTokens)}, model.Price)

	// A structured answer may take a second attempt that sends the first answer back and generates another, so the reservation covers both
	if len(req.Schema) > 0 {
		worst = llm.Cost(llm.Usage{Input: 2*est + int64(maxTokens), Output: 2 * int64(maxTokens)}, model.Price)
	}
	settle, ok := live.ReserveSpend(worst)
	if !ok {
		return nil, apperror.Forbidden("the run reached its cost limit, so ump llm is no longer available")
	}

	// The provider bills a call whether or not the sandbox waits for the answer, so a sandbox hanging up must not cut the call short before its cost is known
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()
	meter := &meteredProvider{Provider: provider}
	var (
		resp *llm.Response
		err  error
		out  = llmResponse{model: model.Name}
	)
	if len(req.Schema) > 0 {
		llmReq.OutputSchema, llmReq.OutputName = req.Schema, "result"
		var parsed json.RawMessage
		_, err = llm.Structured(ctx, meter, llmReq, &parsed)
		out.JSON = parsed
	} else {
		resp, err = meter.Stream(ctx, llmReq, nil)
		if resp != nil {
			out.Text = resp.Message.Text()
		}
	}
	out.Usage = meter.usage
	out.Cost = llm.Cost(meter.usage, model.Price)

	// A model call that failed without reporting its usage may still have been billed, so it is charged the whole reservation
	charged := out.Cost
	if meter.unmetered {
		charged = max(charged, worst)
	}
	settle(out.Usage, charged)
	if err != nil {
		if errors.Is(err, llm.ErrRefusal) {
			return nil, apperror.New(apperror.CodeProviderError, http.StatusUnprocessableEntity, "The model refused to answer")
		}
		return nil, apperror.ProviderError(err, "The LLM call failed")
	}
	return out, nil
}

// meteredProvider adds up the usage of every model call one ump llm call makes, including the retry llm.Structured may make on its own
type meteredProvider struct {
	llm.Provider
	usage llm.Usage
	// unmetered is set when a model call failed after the provider may have started billing it, so its usage is unknown
	unmetered bool
}

func (p *meteredProvider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (*llm.Response, error) {
	resp, err := p.Provider.Stream(ctx, req, onDelta)
	if resp != nil {
		p.usage = p.usage.Add(resp.Usage)
	}
	if err != nil && !llm.Unbilled(err) {
		p.unmetered = true
	}
	return resp, err
}

type stateValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Found bool   `json:"found"`
	// firstWrite is set on the run's first write to the job's state
	firstWrite bool
}

// keptOnTimeline keeps the run's first state write on the timeline, where reflection looks for whether the run changed the job's state
func (v stateValue) keptOnTimeline() bool { return v.firstWrite }

func (b *Broker) stateGet(_ http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error) {
	key := r.PathValue("key")
	v, ok, err := b.state(live).Get(r.Context(), key)
	if err != nil {
		return nil, err
	}
	return stateValue{Key: key, Value: v, Found: ok}, nil
}

func (b *Broker) stateSet(_ http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error) {
	var req struct {
		Value string `json:"value"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	key := r.PathValue("key")
	err := b.state(live).Set(r.Context(), key, req.Value)
	if errors.Is(err, runner.ErrRecordLimit) {
		return nil, apperror.InvalidField("value", "too_large", err.Error())
	} else if err != nil {
		return nil, err
	}
	return stateValue{Key: key, Value: req.Value, Found: true, firstWrite: live.FirstStateWrite()}, nil
}

func (b *Broker) stateList(_ http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error) {
	entries, err := b.state(live).List(r.Context())
	if err != nil {
		return nil, err
	}
	return stateListing(entries), nil
}

// stateListing is a job's whole state, written one entry at a time so a listing holds at most one encoded value in memory besides the entries
type stateListing map[string]string

// writeJSON writes the object encoding the whole map would, with the keys in order
func (l stateListing) writeJSON(w io.Writer) error {
	bw := bufio.NewWriter(w)
	enc := newEncoder(valueWriter{bw})
	_ = bw.WriteByte('{')
	for i, key := range slices.Sorted(maps.Keys(l)) {
		if i > 0 {
			_ = bw.WriteByte(',')
		}
		_ = enc.Encode(key)
		_ = bw.WriteByte(':')

		// A failed write sticks to the buffered writer, so the listing stops here once the client went away
		err := enc.Encode(l[key])
		if err != nil {
			return err
		}
	}
	_, _ = bw.WriteString("}\n")
	return bw.Flush()
}

// valueWriter drops the newline json.Encoder ends each value with, so values can go inside an object
// An encoded string never contains a newline of its own, since JSON escapes it
type valueWriter struct{ w io.Writer }

func (v valueWriter) Write(p []byte) (int, error) {
	_, err := v.w.Write(bytes.TrimSuffix(p, []byte("\n")))
	return len(p), err
}

type okResponse struct {
	OK bool `json:"ok"`
}

func (b *Broker) output(_ http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error) {
	var req struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if req.Key == "" {
		return nil, apperror.InvalidField("key", "required", "is required")
	}
	// A request without a value stores an empty string, since the decoder already rejects values that are not JSON
	if len(req.Value) == 0 {
		req.Value = json.RawMessage(`""`)
	}
	if err := live.SetOutput(req.Key, req.Value); err != nil {
		return nil, apperror.InvalidField("value", "too_large", err.Error())
	}
	return okResponse{OK: true}, nil
}

func (b *Broker) step(_ http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if err := live.AddStep(req.Name); err != nil {
		return nil, apperror.InvalidField("name", "too_large", err.Error())
	}
	live.Recorder.Emit(r.Context(), events.Event{Type: events.TypeScriptStep, Payload: map[string]any{"name": req.Name}})
	return okResponse{OK: true}, nil
}

// setSummary records the markdown summary of a scripted run, which has no finish call to carry one
func (b *Broker) setSummary(_ http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error) {
	var req struct {
		Summary string `json:"summary"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if err := live.SetSummary(req.Summary); err != nil {
		return nil, apperror.InvalidField("summary", "too_large", err.Error())
	}
	return okResponse{OK: true}, nil
}

func (b *Broker) fail(_ http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error) {
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	live.Fail(req.Reason)

	// The reason can be megabytes and a sandbox may report failures without end, so the log only gets its start
	slog.InfoContext(r.Context(), "Run reported failure through the broker", slog.String("run", live.Run.ID), slog.String("reason", clip(req.Reason)))
	return okResponse{OK: true}, nil
}
