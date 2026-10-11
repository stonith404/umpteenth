package mcpservers

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/mcp"
	"github.com/stonith404/umpteenth/backend/internal/mcpservers/mcpserversdb"
	"github.com/stonith404/umpteenth/backend/internal/runner"
)

// connectTimeout bounds connecting one server and listing its tools, which includes npx or uvx installing a stdio server
const connectTimeout = 2 * time.Minute

// rootStdioReason explains why a root sandbox skips a stdio server with environment variables
const rootStdioReason = "a stdio server with environment variables can't run in a root sandbox, because root could read them"

// runServers are a run's MCP servers, each connected the first time the run needs it
// A main script that never calls ump mcp then never waits for a server, and stdio servers don't take up the sandbox's memory
type runServers struct {
	m  *Module
	rc runner.RunContext
	// ctx is the run's, so a broker request that ends early never leaves a server failed for an agent that takes over later
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	pending  []mcpserversdb.ListJobServersRow
	tools    []agent.Tool
	sessions []*mcp.Session
	// unavailable holds why each server that failed to connect or was skipped has no tools, by server name
	unavailable map[string]string
	budget      mcp.ToolBudget
	closed      bool
}

// Connect implements runner.RunTools
func (s *runServers) Connect(server string) []agent.Tool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Each server is tried once, in the order the job lists them, whether it connects or not
	if !s.closed {
		var rest []mcpserversdb.ListJobServersRow
		for _, row := range s.pending {
			if server != "" && row.Name != server {
				rest = append(rest, row)
				continue
			}
			s.connect(row)
		}
		s.pending = rest
	}
	return slices.Clone(s.tools)
}

// Unavailable implements runner.RunTools
func (s *runServers) Unavailable(server string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reason, ok := s.unavailable[server]
	return reason, ok
}

// Close implements runner.RunTools
func (s *runServers) Close() {
	// Cancelling first ends a connection still in progress, so closing never waits for a slow server
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, session := range s.sessions {
		session.Close()
	}
}

// connect connects one server and adds its tools, or tells the run why it can't
func (s *runServers) connect(row mcpserversdb.ListJobServersRow) {
	// The server's secrets and OAuth login are resolved when it connects rather than when the run starts
	server := mcpserversdb.McpServer{
		ID: row.ID, WorkspaceID: row.WorkspaceID, Name: row.Name, Transport: row.Transport, Command: row.Command, Args: row.Args, Env: row.Env, Url: row.Url, Headers: row.Headers,
		OauthCredentials: row.OauthCredentials, OauthLoggedInAt: row.OauthLoggedInAt,
	}
	cfg, err := s.m.serverConfig(s.ctx, s.rc.Run.WorkspaceID, server, row.AllowedTools)
	if err != nil {
		s.skip(row.Name, err.Error(), fmt.Sprintf("MCP server %s: %v", row.Name, err))
		return
	}

	// Root in the sandbox can take on the mcp user and read a stdio server's environment, which often holds secrets
	if s.rc.Job.RunAsRoot && cfg.Transport == mcp.TransportStdio && len(cfg.Env) > 0 {
		s.skip(row.Name, rootStdioReason, fmt.Sprintf("MCP server %s was skipped: %s. Use an HTTP server, or install packages with a job image instead of running as root.", row.Name, rootStdioReason))
		return
	}

	// A server that fails to connect is reported and skipped, so the agent can still finish and explain
	start := time.Now()
	connectCtx, cancel := context.WithTimeout(s.ctx, connectTimeout)
	session, err := s.m.manager.Connect(connectCtx, cfg, s.rc.Sandbox)
	var infos []mcp.ToolInfo
	if err == nil {
		infos, err = session.Tools(connectCtx)
	}
	cancel()
	if err != nil {
		if session != nil {
			session.Close()
		}
		s.skip(row.Name, connectError(err), fmt.Sprintf("MCP server %s is unavailable: %s", row.Name, connectError(err)))
		return
	}

	// The same bound applies across every server of the run, and only the server that overflows it is left out, since the order servers connect in follows main's calls
	nextBudget := s.budget
	var limitErr error
	for _, info := range infos {
		if limitErr = nextBudget.Add(info); limitErr != nil {
			break
		}
	}
	if limitErr != nil {
		session.Close()
		s.skip(row.Name, limitErr.Error(), fmt.Sprintf("MCP server %s was skipped: %s", row.Name, limitErr))
		return
	}

	// The server's tools become callable, and the timeline shows how long it took to start
	s.budget = nextBudget
	s.sessions = append(s.sessions, session)
	ms := time.Since(start).Milliseconds()
	s.rc.Emit(events.Event{Type: events.TypeMCPCall, Ms: &ms, Payload: map[string]any{"server": row.Name, "transport": cfg.Transport, "tools": len(infos)}})
	for _, info := range infos {
		s.tools = append(s.tools, mcp.NewAgentTool(session, row.Name, info))
	}
}

// skip notes why a server has no tools and tells the run, unless the run stopped meanwhile, which says nothing about the server
func (s *runServers) skip(name, reason, message string) {
	s.unavailable[name] = reason
	if s.ctx.Err() != nil {
		return
	}
	s.rc.Emit(events.Event{Type: events.TypeError, Payload: map[string]any{"message": message}})
}
