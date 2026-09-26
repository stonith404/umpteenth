package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// LiveRun is the host-local state of a run executing on this replica, which the broker serves
// It only holds what dies with the run anyway; everything durable is written to the database as it happens (PLAN.md §3.4)
type LiveRun struct {
	Run      Run
	Job      JobConfig
	Recorder *events.Recorder

	Provider  llm.Provider
	Model     Model
	Utility   llm.Provider
	UtilModel Model

	// Shadow marks a trial of a proposed main script, where the broker refuses calls that may have side effects
	Shadow bool
	// State replaces the job's persistent state for the broker, such as a shadow run's copy that never writes to the job's own
	State agent.StateStore

	mu      sync.Mutex
	tools   map[string]agent.Tool
	outputs map[string]json.RawMessage
	// outputBytes is the combined size of the output keys and values
	outputBytes int
	steps       []string
	// actions are the calls with possible side effects a script made, which a fallback agent must not repeat
	actions []string
	// refused are the calls with possible side effects a shadow run turned away
	refused []string
	summary string
	failure string
	// brokerEvents counts the broker calls the run put on its timeline
	brokerEvents int
	// wroteState is set once the run wrote to the job's state through the broker
	wroteState bool
	// requests holds a slot for each broker request the run has in flight, and is made with the first one
	requests chan struct{}
	// cost accumulates LLM spend from ump llm calls, in micro-USD
	cost  int64
	usage llm.Usage
	// reserved is the worst-case cost of the ump llm calls still in flight, in micro-USD
	reserved int64
	// closed is set once the run's cost has been taken for its record, after which nothing would count a new ump llm call
	closed bool
	// agentCost is the spend of the agent loop's own model calls, in micro-USD
	agentCost int64
}

// SetTools makes tools callable through the broker, keyed by tool name
func (l *LiveRun) SetTools(tools []agent.Tool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tools = make(map[string]agent.Tool, len(tools))
	for _, t := range tools {
		l.tools[t.Def().Name] = t
	}
}

// Tool returns a tool by name
func (l *LiveRun) Tool(name string) (agent.Tool, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.tools[name]
	return t, ok
}

// Tools returns all tools callable through the broker
func (l *LiveRun) Tools() []agent.Tool {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]agent.Tool, 0, len(l.tools))
	for _, t := range l.tools {
		out = append(out, t)
	}
	return out
}

// Outputs and step markers live in the control plane's memory until the run ends, so a run can only record a bounded amount of them
const (
	maxOutputs     = 100
	maxOutputBytes = 1 << 20
	maxSteps       = 1000
	maxStepName    = 200
	maxActions     = 200
	maxSummary     = 16 << 10
	maxFailure     = 2000
	// maxBrokerRequests bounds the broker requests a run has in flight at once, since each one can hold a request body and a state value in the control plane's memory
	maxBrokerRequests = 8
)

// MaxBrokerEvents bounds the broker calls a run puts on its timeline, since each one is a database row and a sandbox can send requests without end
const MaxBrokerEvents = 1000

// ErrRecordLimit reports that a run tried to record more outputs or steps than it may
var ErrRecordLimit = errors.New("record limit exceeded")

// SetOutput records a structured output from ump output set
func (l *LiveRun) SetOutput(key string, value json.RawMessage) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.outputs == nil {
		l.outputs = map[string]json.RawMessage{}
	}

	// Replacing an output frees the size of its old value, so only the growth counts against the limit
	old, exists := l.outputs[key]
	size := l.outputBytes + len(key) + len(value)
	if exists {
		size -= len(key) + len(old)
	}
	if !exists && len(l.outputs) >= maxOutputs {
		return fmt.Errorf("%w: a run records at most %d outputs", ErrRecordLimit, maxOutputs)
	}
	if size > maxOutputBytes {
		return fmt.Errorf("%w: a run's outputs may total at most %d bytes", ErrRecordLimit, maxOutputBytes)
	}
	l.outputs[key] = value
	l.outputBytes = size
	return nil
}

// Outputs returns the outputs recorded through the broker
func (l *LiveRun) Outputs() map[string]json.RawMessage {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]json.RawMessage, len(l.outputs))
	maps.Copy(out, l.outputs)
	return out
}

// AddStep records a ump step marker
func (l *LiveRun) AddStep(name string) error {
	if len(name) > maxStepName {
		return fmt.Errorf("%w: step names are at most %d bytes", ErrRecordLimit, maxStepName)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.steps) >= maxSteps {
		return fmt.Errorf("%w: a run records at most %d steps", ErrRecordLimit, maxSteps)
	}
	l.steps = append(l.steps, name)
	return nil
}

// Steps returns the recorded step markers
func (l *LiveRun) Steps() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.steps...)
}

// RecordAction notes a call that may have changed something outside the sandbox, such as an MCP tool that isn't read-only
// It reports whether the call was noted, since a run notes a bounded number of them
func (l *LiveRun) RecordAction(action string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.actions) >= maxActions {
		return false
	}
	l.actions = append(l.actions, action)
	return true
}

// Actions returns the recorded calls with possible side effects
func (l *LiveRun) Actions() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.actions...)
}

// Refuse notes a call with possible side effects that a shadow run turned away, which makes the shadow run inconclusive
func (l *LiveRun) Refuse(call string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.refused) < maxActions {
		l.refused = append(l.refused, call)
	}
}

// Refused returns the calls a shadow run turned away
func (l *LiveRun) Refused() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.refused...)
}

// BrokerEvent counts a broker call against the run's timeline limit
// It returns record for the calls within the limit, and marker once for the first call past it, which notes on the timeline that later calls are left out
func (l *LiveRun) BrokerEvent() (record, marker bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.brokerEvents > MaxBrokerEvents {
		return false, false
	}
	l.brokerEvents++
	return l.brokerEvents <= MaxBrokerEvents, l.brokerEvents > MaxBrokerEvents
}

// FirstStateWrite reports whether a write to the job's state through the broker is the run's first
func (l *LiveRun) FirstStateWrite() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	first := !l.wroteState
	l.wroteState = true
	return first
}

// AcquireRequest reserves one of the run's concurrent broker requests, waiting while the run has too many in flight
func (l *LiveRun) AcquireRequest(ctx context.Context) (release func(), err error) {
	l.mu.Lock()
	if l.requests == nil {
		l.requests = make(chan struct{}, maxBrokerRequests)
	}
	slots := l.requests
	l.mu.Unlock()

	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SetSummary records the markdown summary a scripted run reports with ump summary
func (l *LiveRun) SetSummary(summary string) error {
	if len(summary) > maxSummary {
		return fmt.Errorf("%w: a summary is at most %d bytes", ErrRecordLimit, maxSummary)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.summary = summary
	return nil
}

// Summary returns the summary a script reported
func (l *LiveRun) Summary() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.summary
}

// Fail records an explicit failure from ump fail, keeping the start of a long reason since it becomes the run's error
func (l *LiveRun) Fail(reason string) {
	reason, _ = agent.Truncate(reason, maxFailure, 0)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failure = reason
}

// ClearFailure forgets a failure the main script reported, once an agent has taken over the run
func (l *LiveRun) ClearFailure() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failure = ""
}

// Failure returns the explicit failure reason, if any
func (l *LiveRun) Failure() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.failure
}

// AddAgentCost records the cost of one model call of the agent loop
func (l *LiveRun) AddAgentCost(cost int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.agentCost += cost
}

// ReserveSpend holds back the worst-case cost of a ump llm call until the call settles with its actual usage and cost
// Parallel calls see each other's reservations, so a script can't start more calls than the remaining budget pays for
// A call that doesn't fit is still allowed while nothing else is in flight, which bounds the overspend to one call like the agent loop
// No call may start once FinalSpend has taken the run's cost for its record
func (l *LiveRun) ReserveSpend(worst int64) (settle func(u llm.Usage, cost int64), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	limit := l.Job.Limits.MaxCost
	spent := l.cost + l.agentCost
	if l.closed || (limit > 0 && (spent >= limit || (l.reserved > 0 && spent+l.reserved+worst > limit))) {
		return nil, false
	}
	l.reserved += worst
	return func(u llm.Usage, cost int64) {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.reserved -= worst
		l.usage = l.usage.Add(u)
		l.cost += cost
	}, true
}

// Spend returns the broker LLM usage and cost
func (l *LiveRun) Spend() (llm.Usage, int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.usage, l.cost
}

// FinalSpend returns the broker LLM usage and cost to record for the finished run, and refuses ump llm calls from then on
// A call still in flight completes and is billed after the record is written, so it counts with the worst-case cost reserved for it
func (l *LiveRun) FinalSpend() (llm.Usage, int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	return l.usage, l.cost + l.reserved
}

// Registry tracks the live runs of this replica for the broker
type Registry struct {
	mu   sync.RWMutex
	runs map[string]*LiveRun
	// shadows are keyed by the hash of their broker token, since a shadow run has no run row the broker could resolve the token with
	shadows map[string]*LiveRun
	// grants are keyed by the hash of the broker token that holds them
	grants map[string]*ProxyGrant
}

func NewRegistry() *Registry {
	return &Registry{runs: map[string]*LiveRun{}, shadows: map[string]*LiveRun{}, grants: map[string]*ProxyGrant{}}
}

func (r *Registry) RegisterShadow(tokenHash string, lr *LiveRun) {
	r.mu.Lock()
	r.shadows[tokenHash] = lr
	r.mu.Unlock()
}

func (r *Registry) UnregisterShadow(tokenHash string) {
	r.mu.Lock()
	delete(r.shadows, tokenHash)
	r.mu.Unlock()
}

// ShadowByToken returns the shadow run a broker token belongs to
func (r *Registry) ShadowByToken(tokenHash string) (*LiveRun, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	lr, ok := r.shadows[tokenHash]
	return lr, ok
}

func (r *Registry) Register(runID string, lr *LiveRun) {
	r.mu.Lock()
	r.runs[runID] = lr
	r.mu.Unlock()
}

func (r *Registry) Unregister(runID string) {
	r.mu.Lock()
	delete(r.runs, runID)
	r.mu.Unlock()
}

// Get returns a live run on this replica
func (r *Registry) Get(runID string) (*LiveRun, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	lr, ok := r.runs[runID]
	return lr, ok
}
