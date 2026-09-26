// Package mcp connects to MCP servers: HTTP servers from the host, stdio servers inside the run's sandbox (PLAN.md §8)
package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/common"
	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// Transports
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

// ServerConfig describes one MCP server with its secrets already expanded
type ServerConfig struct {
	Name      string
	Transport string
	Command   string
	Args      []string
	Env       map[string]string
	URL       string
	Headers   map[string]string
	// OAuth adds the server's OAuth login to every request when set
	OAuth *OAuthTokens
	// AllowedTools limits the exposed tools; nil exposes all of them
	AllowedTools []string
}

// ToolInfo describes one tool of a server
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	ReadOnly    bool            `json:"readOnly"`
	Destructive bool            `json:"destructive"`
}

// Session is a connection to one MCP server
type Session struct {
	cfg    ServerConfig
	cs     *sdk.ClientSession
	proc   sandbox.Process
	stderr *ringBuffer
	// cancel ends the context a legacy SSE stream was opened with, which outlives the caller's context
	cancel context.CancelFunc
}

// Manager opens MCP sessions with the host-side egress guard applied
type Manager struct {
	guard *egress.Guard
}

func NewManager(guard *egress.Guard) *Manager {
	return &Manager{guard: guard}
}

func newClient() *sdk.Client {
	return sdk.NewClient(&sdk.Implementation{Name: "umpteenth", Version: common.Version}, nil)
}

// Connect opens a session; stdio servers need the run's sandbox to launch in
func (m *Manager) Connect(ctx context.Context, cfg ServerConfig, sb sandbox.Sandbox) (*Session, error) {
	switch cfg.Transport {
	case TransportHTTP:
		return m.connectHTTP(ctx, cfg)
	case TransportStdio:
		if sb == nil {
			return nil, errors.New("stdio MCP servers need a sandbox")
		}
		return connectStdio(ctx, cfg, sb)
	default:
		return nil, fmt.Errorf("unknown MCP transport %q", cfg.Transport)
	}
}

// connectHTTP connects from the host, so credentials in headers and OAuth tokens never enter the sandbox
// They only go to the server's own origin, so a redirect or a legacy SSE endpoint elsewhere never receives them
func (m *Manager) connectHTTP(ctx context.Context, cfg ServerConfig) (*Session, error) {
	endpoint, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL of %s: %w", cfg.Name, err)
	}
	serverOrigin := origin(endpoint)
	httpClient := m.guard.HTTPClient(0)
	recorder := &unauthorizedRecorder{base: &responseLimitTransport{base: httpClient.Transport}}
	var transport http.RoundTripper = recorder
	if cfg.OAuth != nil {
		transport = &oauthTransport{base: transport, origin: serverOrigin, tokens: cfg.OAuth}
	}
	httpClient.Transport = &originHeaderTransport{base: transport, origin: serverOrigin, headers: cfg.Headers}

	// Streamable HTTP is the current transport; the legacy SSE transport is tried for older servers
	cs, err := newClient().Connect(ctx, &sdk.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: httpClient, DisableStandaloneSSE: true, MaxRetries: 2}, nil)
	var cancel context.CancelFunc
	if err != nil {
		// The legacy SSE stream stays bound to the context it was opened with, and callers end theirs once the tools are listed
		// The stream therefore gets a context that only ends with the session, while the caller's context still bounds the handshake
		sseCtx, sseCancel := context.WithCancel(context.WithoutCancel(ctx))
		stop := context.AfterFunc(ctx, sseCancel)
		legacy, legacyErr := newClient().Connect(sseCtx, &sdk.SSEClientTransport{Endpoint: cfg.URL, HTTPClient: httpClient}, nil)
		stop()
		if legacyErr != nil {
			sseCancel()
			// A 401 is reported as such, so callers can tell a missing login from an unreachable server
			if recorder.sawUnauthorized() && !errors.Is(err, ErrLoginExpired) {
				return nil, fmt.Errorf("failed to connect to %s: %w", cfg.Name, ErrUnauthorized)
			}
			return nil, fmt.Errorf("failed to connect to %s: %w", cfg.Name, err)
		}
		cs = legacy
		cancel = sseCancel
	}
	return &Session{cfg: cfg, cs: cs, cancel: cancel}, nil
}

// connectStdio launches the server inside the sandbox as the mcp user, so the agent cannot read its environment
func connectStdio(ctx context.Context, cfg ServerConfig, sb sandbox.Sandbox) (*Session, error) {
	stderr := &ringBuffer{max: 8192}
	proc, err := sb.Attach(context.WithoutCancel(ctx), sandbox.ExecRequest{
		Cmd:     append([]string{cfg.Command}, cfg.Args...),
		WorkDir: "/tmp",
		Env:     cfg.Env,
		User:    sandbox.UserMCP,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", cfg.Name, err)
	}
	go func() { _, _ = io.Copy(stderr, proc.Stderr()) }()

	cs, err := newClient().Connect(ctx, &sdk.IOTransport{Reader: io.NopCloser(proc.Stdout()), Writer: proc.Stdin()}, nil)
	if err != nil {
		_ = proc.Kill()
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("failed to initialize %s: %w (stderr: %s)", cfg.Name, err, msg)
		}
		return nil, fmt.Errorf("failed to initialize %s: %w", cfg.Name, err)
	}
	return &Session{cfg: cfg, cs: cs, proc: proc, stderr: stderr}, nil
}

// Tool discovery limits bound the memory retained from an untrusted MCP server
const (
	maxToolPages = 100
	maxTools     = 512
	maxToolBytes = 8 << 20
)

// ToolBudget bounds a tool catalog across one server or a whole run
type ToolBudget struct {
	count int
	bytes int
}

// Add reserves space for one tool definition
func (b *ToolBudget) Add(info ToolInfo) error {
	size := len(info.Name) + len(info.Description) + len(info.InputSchema)
	if b.count >= maxTools {
		return fmt.Errorf("a run exposes at most %d MCP tools", maxTools)
	}
	if size > maxToolBytes-b.bytes {
		return fmt.Errorf("MCP tool definitions may total at most %d bytes", maxToolBytes)
	}
	b.count++
	b.bytes += size
	return nil
}

// Tools lists the server's tools, filtered by the allow-list
// A name the server lists twice is kept once, since model APIs reject duplicate tool names
func (s *Session) Tools(ctx context.Context) ([]ToolInfo, error) {
	var out []ToolInfo
	var budget ToolBudget
	var cursor string
	seen := map[string]bool{}
	cursors := map[string]bool{}
	for page := 0; ; page++ {
		res, err := s.cs.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("failed to list tools of %s: %w", s.cfg.Name, err)
		}
		for _, t := range res.Tools {
			if seen[t.Name] || (s.cfg.AllowedTools != nil && !slices.Contains(s.cfg.AllowedTools, t.Name)) {
				continue
			}
			seen[t.Name] = true
			schema, _ := json.Marshal(t.InputSchema)
			if len(schema) == 0 || string(schema) == "null" {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			info := ToolInfo{Name: t.Name, Description: t.Description, InputSchema: schema}
			if t.Annotations != nil {
				info.ReadOnly = t.Annotations.ReadOnlyHint

				// MCP defaults the destructive hint to true but only applies it to tools that are not read-only
				info.Destructive = !info.ReadOnly && (t.Annotations.DestructiveHint == nil || *t.Annotations.DestructiveHint)
			}
			if err := budget.Add(info); err != nil {
				return nil, fmt.Errorf("failed to list tools of %s: %w", s.cfg.Name, err)
			}
			out = append(out, info)
		}
		if res.NextCursor == "" {
			return out, nil
		}
		if cursors[res.NextCursor] || page+1 >= maxToolPages {
			return nil, fmt.Errorf("failed to list tools of %s: the server keeps returning more pages", s.cfg.Name)
		}
		cursors[res.NextCursor] = true
		cursor = res.NextCursor
	}
}

// Call invokes a tool and renders its result as text
func (s *Session) Call(ctx context.Context, tool string, args json.RawMessage) (string, bool, error) {
	var arguments any = map[string]any{}
	if len(args) > 0 && string(args) != "null" {
		arguments = args
	}
	res, err := s.cs.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: arguments})
	if err != nil {
		return "", true, err
	}
	return renderResult(res), res.IsError, nil
}

// Close ends the session and stops a stdio server
func (s *Session) Close() {
	_ = s.cs.Close()
	if s.cancel != nil {
		s.cancel()
	}
	if s.proc != nil {
		_ = s.proc.Kill()
	}
}

func renderResult(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		switch v := c.(type) {
		case *sdk.TextContent:
			b.WriteString(v.Text)
		case *sdk.ImageContent:
			fmt.Fprintf(&b, "[image %s, %d bytes]", v.MIMEType, len(v.Data))
		case *sdk.AudioContent:
			fmt.Fprintf(&b, "[audio %s, %d bytes]", v.MIMEType, len(v.Data))
		case *sdk.ResourceLink:
			fmt.Fprintf(&b, "[resource %s]", v.URI)
		case *sdk.EmbeddedResource:
			if v.Resource != nil && v.Resource.Text != "" {
				b.WriteString(v.Resource.Text)
			} else if v.Resource != nil {
				fmt.Fprintf(&b, "[resource %s]", v.Resource.URI)
			}
		default:
			raw, _ := json.Marshal(c)
			b.Write(raw)
		}
		b.WriteString("\n")
	}
	// Structured results without text content are shown as JSON
	if strings.TrimSpace(b.String()) == "" && res.StructuredContent != nil {
		raw, _ := json.Marshal(res.StructuredContent)
		return string(raw)
	}
	return strings.TrimRight(b.String(), "\n")
}

var toolNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// ToolName gives an MCP tool a stable provider-safe name whose identity suffix prevents sanitized or truncated names from colliding
func ToolName(server, tool string) string {
	readable := toolNameSanitizer.ReplaceAllString(server, "_") + "__" + toolNameSanitizer.ReplaceAllString(tool, "_")
	identity, _ := json.Marshal([2]string{server, tool})
	sum := sha256.Sum256(identity)
	suffix := fmt.Sprintf("__%x", sum[:10])
	if len(readable) > 64-len(suffix) {
		readable = readable[:64-len(suffix)]
	}
	return readable + suffix
}

// AgentTool exposes one MCP tool to the agent loop and the broker
type AgentTool struct {
	Session *Session
	Server  string
	Info    ToolInfo
	name    string
	// OnCall records the call on the run timeline when set
	OnCall func(server, tool string, took time.Duration, isError bool)
}

// NewAgentTool wraps a server tool
func NewAgentTool(s *Session, server string, info ToolInfo) *AgentTool {
	return &AgentTool{Session: s, Server: server, Info: info, name: ToolName(server, info.Name)}
}

func (t *AgentTool) ReadOnly() bool { return t.Info.ReadOnly }

func (t *AgentTool) Def() llm.ToolDef {
	desc := t.Info.Description
	if desc == "" {
		desc = "Tool " + t.Info.Name + " of the " + t.Server + " MCP server"
	}
	return llm.ToolDef{Name: t.name, Description: desc, Schema: t.Info.InputSchema}
}

func (t *AgentTool) Run(ctx context.Context, call llm.ToolCall) agent.Result {
	start := time.Now()
	content, isError, err := t.Session.Call(ctx, t.Info.Name, call.Args)
	if t.OnCall != nil {
		t.OnCall(t.Server, t.Info.Name, time.Since(start), isError || err != nil)
	}
	if err != nil {
		return agent.Errorf("MCP call failed: %v", err)
	}
	return agent.Result{Content: content, IsError: isError, Meta: map[string]any{"server": t.Server, "tool": t.Info.Name}}
}

// ringBuffer keeps the last bytes of a stream, for error messages from stdio servers
type ringBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf.Write(p)
	if r.buf.Len() > r.max {
		b := r.buf.Bytes()[r.buf.Len()-r.max:]
		r.buf.Reset()
		r.buf.Write(b)
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}
