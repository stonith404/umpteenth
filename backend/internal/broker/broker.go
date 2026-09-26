// Package broker is the sandbox-facing API the ump CLI talks to; credentials stay on the host (PLAN.md §4.10)
package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/apperror"
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

// StateStores gives access to a job's persistent state
type StateStores interface {
	ForJob(workspaceID, jobID string) agent.StateStore
}

// StateLister lists all entries of a job's state
type StateLister interface {
	List(ctx context.Context) (map[string]string, error)
}

type Dependencies struct {
	Runs  RunLookup
	Live  *runner.Registry
	State StateStores
}

// Broker serves the per-run API for the ump CLI
type Broker struct {
	deps Dependencies
	// dialer connects the egress proxy to allowed hosts; tests replace it to reach local servers
	dialer func(allowPrivate bool) func(ctx context.Context, network, addr string) (net.Conn, error)
}

func New(deps Dependencies) *Broker {
	return &Broker{deps: deps, dialer: guardedDialer}
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
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
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

// auth resolves the run from the bearer token and records every call on the run's timeline
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

		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		start := time.Now()
		res, err := next(w, r, live)
		ms := time.Since(start).Milliseconds()

		payload := map[string]any{"endpoint": clip(r.Method + " " + r.URL.Path), "ok": err == nil}
		if err != nil {
			payload["error"] = clip(err.Error())
		}
		if detail, ok := res.(interface{ timelineDetail() map[string]any }); ok {
			for k, v := range detail.timelineDetail() {
				payload[k] = v
			}
		}
		live.Recorder.Emit(r.Context(), events.Event{Type: events.TypeBrokerCall, Ms: &ms, Payload: payload})

		if err != nil {
			httpserver.WriteHTTPError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
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
func (b *Broker) state(live *runner.LiveRun) agent.StateStore {
	if live.State != nil {
		return live.State
	}
	return b.deps.State.ForJob(live.Run.WorkspaceID, live.Run.JobID)
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
	sort.Slice(out, func(i, j int) bool { return out[i].Server+out[i].Name < out[j].Server+out[j].Name })
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
}

func (r mcpCallResponse) timelineDetail() map[string]any {
	content, _ := agent.Truncate(r.Content, 2000, 1000)
	return map[string]any{"server": r.server, "tool": r.tool, "isError": r.IsError, "result": content}
}

// mcpCall runs an MCP tool for a script through the same host-side session the agent uses, so every call is recorded
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
	if !tool.ReadOnly() {
		outcome := "succeeded"
		if res.IsError {
			outcome = "failed"
		}
		live.RecordAction(fmt.Sprintf("MCP tool %s.%s with %s (%s)", req.Server, req.Tool, args, outcome))
	}
	return mcpCallResponse{Content: res.Content, IsError: res.IsError, server: req.Server, tool: req.Tool}, nil
}

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

// llmCall makes one bounded LLM call without tools, the building block of scripted runs' judgment steps
func (b *Broker) llmCall(_ http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error) {
	var req llmRequest
	if err := decode(r, &req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, apperror.InvalidField("prompt", "required", "is required")
	}

	provider, model := live.Utility, live.UtilModel
	if req.Model == "agent" {
		provider, model = live.Provider, live.Model
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 || maxTokens > 16000 {
		maxTokens = 4096
	}
	llmReq := llm.Request{Model: model.Name, MaxTokens: maxTokens, Effort: llm.EffortLow,
		Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart(req.Prompt)}}}}
	if req.System != "" {
		llmReq.System = []llm.Block{{Text: req.System}}
	}

	// Scripts calling ump llm in a loop or in parallel must not get around the run's cost limit
	// Tokens are estimated from bytes on the generous side, since most text needs several bytes per token
	worst := llm.Cost(llm.Usage{Input: int64(len(req.Prompt)+len(req.System)+len(req.Schema)) / 2, Output: int64(maxTokens)}, model.Price)
	settle, ok := live.ReserveSpend(worst)
	if !ok {
		return nil, apperror.Forbidden("the run reached its cost limit, so ump llm is no longer available")
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	var (
		resp *llm.Response
		err  error
		out  = llmResponse{model: model.Name}
	)
	if len(req.Schema) > 0 {
		llmReq.OutputSchema, llmReq.OutputName = req.Schema, "result"
		var parsed json.RawMessage
		resp, err = llm.Structured(ctx, provider, llmReq, &parsed)
		out.JSON = parsed
	} else {
		resp, err = provider.Stream(ctx, llmReq, nil)
		if resp != nil {
			out.Text = resp.Message.Text()
		}
	}
	if resp != nil {
		out.Usage = resp.Usage
		out.Cost = llm.Cost(resp.Usage, model.Price)
	}
	settle(out.Usage, out.Cost)
	if err != nil {
		if errors.Is(err, llm.ErrRefusal) {
			return nil, apperror.New(apperror.CodeProviderError, http.StatusUnprocessableEntity, "The model refused to answer")
		}
		return nil, apperror.ProviderError(err, "The LLM call failed")
	}
	return out, nil
}

type stateValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Found bool   `json:"found"`
}

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
	if err != nil {
		return nil, err
	}
	return stateValue{Key: key, Value: req.Value, Found: true}, nil
}

func (b *Broker) stateList(_ http.ResponseWriter, r *http.Request, live *runner.LiveRun) (any, error) {
	lister, ok := b.state(live).(StateLister)
	if !ok {
		return map[string]string{}, nil
	}
	return lister.List(r.Context())
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
	if len(req.Value) == 0 || !json.Valid(req.Value) {
		req.Value, _ = json.Marshal(string(req.Value))
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
	slog.InfoContext(r.Context(), "Run reported failure through the broker", slog.String("run", live.Run.ID), slog.String("reason", req.Reason))
	return okResponse{OK: true}, nil
}
