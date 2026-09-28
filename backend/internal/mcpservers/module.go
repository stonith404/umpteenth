// Package mcpservers owns the MCP server registry of a workspace and attaches servers to jobs
package mcpservers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/listquery"
	"github.com/stonith404/umpteenth/backend/internal/mcp"
	"github.com/stonith404/umpteenth/backend/internal/mcpservers/mcpserversdb"
	"github.com/stonith404/umpteenth/backend/internal/middleware"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// SecretExpander replaces {{secret:NAME}} references with secret values
type SecretExpander interface {
	Expand(ctx context.Context, workspaceID, value string) (string, error)
}

// JobChecker verifies a job belongs to the workspace
type JobChecker interface {
	JobExists(ctx context.Context, workspaceID, jobID string) error
}

// RateLimiter decides whether a test may start a sandbox
type RateLimiter interface {
	Allow(ctx context.Context, key string) (bool, time.Duration, error)
}

type Dependencies struct {
	DB            *database.DB
	Egress        *egress.Guard
	Secrets       SecretExpander
	Jobs          JobChecker
	EncryptionKey []byte
	// AppURL is where authorization servers send the browser back to after an OAuth login
	AppURL string
	// Adapter launches a temporary sandbox to test stdio servers
	Adapter      sandbox.Adapter
	DefaultImage func(ctx context.Context, workspaceID string) string
	// GrantProxy lets the test sandbox reach the internet through the egress proxy until revoke is called
	GrantProxy func(token string, network sandbox.NetworkPolicy) (revoke func())
	// TestLimiter bounds the test sandboxes per person and workspace, which runs.max_concurrent doesn't count; nil admits every test
	TestLimiter RateLimiter
}

type Module struct {
	deps     Dependencies
	db       *database.DB
	queries  *mcpserversdb.Queries
	manager  *mcp.Manager
	oauthKey []byte

	// tokens caches each server's OAuth token refresher by server ID
	tokensMu sync.Mutex
	tokens   map[string]cachedTokens
}

func New(deps Dependencies) (*Module, error) {
	key, err := crypto.DeriveKey(deps.EncryptionKey, "mcp-oauth")
	if err != nil {
		return nil, fmt.Errorf("failed to derive MCP OAuth key: %w", err)
	}
	return &Module{
		deps: deps, db: deps.DB, queries: mcpserversdb.New(deps.DB), manager: mcp.NewManager(deps.Egress),
		oauthKey: key, tokens: map[string]cachedTokens{},
	}, nil
}

// SetJobs wires the job checker, which is built after this module
func (m *Module) SetJobs(j JobChecker) { m.deps.Jobs = j }

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("list-mcp-servers", http.MethodGet, "/api/mcp-servers", "MCP"), auth, m.list)
	httpserver.Register(api, httpserver.Operation("create-mcp-server", http.MethodPost, "/api/mcp-servers", "MCP"), auth, m.create)
	httpserver.Register(api, httpserver.Operation("get-mcp-server", http.MethodGet, "/api/mcp-servers/{id}", "MCP"), auth, m.get)
	httpserver.Register(api, httpserver.Operation("update-mcp-server", http.MethodPut, "/api/mcp-servers/{id}", "MCP"), auth, m.update)
	httpserver.Register(api, httpserver.Operation("delete-mcp-server", http.MethodDelete, "/api/mcp-servers/{id}", "MCP"), auth, m.delete)
	httpserver.Register(api, httpserver.Operation("test-mcp-server", http.MethodPost, "/api/mcp-servers/{id}/test", "MCP"), auth, m.test)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("login-mcp-server", http.MethodPost, "/api/mcp-servers/{id}/oauth/login", "MCP"), httpserver.Access{SessionOnly: true}), auth, m.login)
	httpserver.Register(api, httpserver.Operation("mcp-server-oauth-callback", http.MethodGet, "/api/mcp-servers/{id}/oauth/callback", "MCP"), auth, m.callback)
	httpserver.Register(api, httpserver.Operation("logout-mcp-server", http.MethodDelete, "/api/mcp-servers/{id}/oauth", "MCP"), auth, m.logout)
	httpserver.Register(api, httpserver.Operation("get-job-mcp-servers", http.MethodGet, "/api/jobs/{id}/mcp-servers", "MCP"), auth, m.getJobServers)
	httpserver.Register(api, httpserver.Operation("set-job-mcp-servers", http.MethodPut, "/api/jobs/{id}/mcp-servers", "MCP"), auth, m.setJobServers)
}

// ServerNames lists enabled servers, used by the compile step
func (m *Module) ServerNames(ctx context.Context, workspaceID string) ([]string, error) {
	return m.queries.ListServerNames(ctx, workspaceID)
}

// serverConfig turns a row into a connectable config with secrets expanded
func (m *Module) serverConfig(ctx context.Context, workspaceID string, s mcpserversdb.McpServer, allowed *string) (mcp.ServerConfig, error) {
	cfg := mcp.ServerConfig{Name: s.Name, Transport: s.Transport, Command: deref(s.Command), URL: deref(s.Url)}
	_ = json.Unmarshal([]byte(s.Args), &cfg.Args)

	var env map[string]string
	_ = json.Unmarshal([]byte(s.Env), &env)
	var err error
	cfg.Env, err = m.expandAll(ctx, workspaceID, env)
	if err != nil {
		return cfg, err
	}
	cfg.Headers, err = m.expandedHeaders(ctx, workspaceID, s)
	if err != nil {
		return cfg, err
	}
	if allowed != nil {
		_ = json.Unmarshal([]byte(*allowed), &cfg.AllowedTools)
	}

	// A configured Authorization header wins over an OAuth login, like Codex's bearer token setting does
	if cfg.Transport == mcp.TransportHTTP && s.OauthLoggedInAt != nil && s.OauthCredentials != nil && !mcp.HasAuthorizationHeader(cfg.Headers) {
		cfg.OAuth, err = m.oauthTokens(workspaceID, s)
		if err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

func (m *Module) expandedHeaders(ctx context.Context, workspaceID string, s mcpserversdb.McpServer) (map[string]string, error) {
	var headers map[string]string
	_ = json.Unmarshal([]byte(s.Headers), &headers)
	return m.expandAll(ctx, workspaceID, headers)
}

func (m *Module) expandAll(ctx context.Context, workspaceID string, in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for k, v := range in {
		expanded, err := m.deps.Secrets.Expand(ctx, workspaceID, v)
		if err != nil {
			return nil, err
		}
		out[k] = expanded
	}
	return out, nil
}

// ToolsForRun implements runner.ToolProvider: it connects the job's MCP servers and exposes their tools
func (m *Module) ToolsForRun(ctx context.Context, rc runner.RunContext) ([]agent.Tool, func(), error) {
	rows, err := m.queries.ListJobServers(ctx, mcpserversdb.ListJobServersParams{WorkspaceID: rc.Run.WorkspaceID, JobID: rc.Run.JobID})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load MCP servers: %w", err)
	}

	var (
		tools    []agent.Tool
		sessions []*mcp.Session
		budget   mcp.ToolBudget
	)
	cleanup := func() {
		for _, s := range sessions {
			s.Close()
		}
	}
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		server := mcpserversdb.McpServer{
			ID: row.ID, WorkspaceID: row.WorkspaceID, Name: row.Name, Transport: row.Transport, Command: row.Command, Args: row.Args, Env: row.Env, Url: row.Url, Headers: row.Headers,
			OauthCredentials: row.OauthCredentials, OauthLoggedInAt: row.OauthLoggedInAt,
		}
		cfg, err := m.serverConfig(ctx, rc.Run.WorkspaceID, server, row.AllowedTools)
		if err != nil {
			rc.Emit(events.Event{Type: events.TypeError, Payload: map[string]any{"message": fmt.Sprintf("MCP server %s: %v", row.Name, err)}})
			continue
		}

		// Root in the sandbox can take on the mcp user and read a stdio server's environment, which often holds secrets
		if rc.Job.RunAsRoot && cfg.Transport == mcp.TransportStdio && len(cfg.Env) > 0 {
			rc.Emit(events.Event{Type: events.TypeError, Payload: map[string]any{"message": fmt.Sprintf("MCP server %s was skipped: a stdio server with environment variables can't run in a root sandbox, because root could read them. Use an HTTP server, or install packages with a job image instead of running as root.", row.Name)}})
			continue
		}

		// A server that fails to connect is reported and skipped, so the agent can still finish and explain
		start := time.Now()
		connectCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		session, err := m.manager.Connect(connectCtx, cfg, rc.Sandbox)
		var infos []mcp.ToolInfo
		if err == nil {
			infos, err = session.Tools(connectCtx)
		}
		cancel()
		if err != nil {
			if session != nil {
				session.Close()
			}
			rc.Emit(events.Event{Type: events.TypeError, Payload: map[string]any{"message": fmt.Sprintf("MCP server %s is unavailable: %s", row.Name, connectError(err))}})
			continue
		}
		// Apply the same aggregate bound across every server attached to the run
		nextBudget := budget
		var limitErr error
		for _, info := range infos {
			if limitErr = nextBudget.Add(info); limitErr != nil {
				break
			}
		}
		if limitErr != nil {
			session.Close()
			rc.Emit(events.Event{Type: events.TypeError, Payload: map[string]any{"message": fmt.Sprintf("MCP server %s was skipped: %s", row.Name, limitErr)}})
			break
		}
		budget = nextBudget
		sessions = append(sessions, session)
		ms := time.Since(start).Milliseconds()
		rc.Emit(events.Event{Type: events.TypeMCPCall, Ms: &ms, Payload: map[string]any{"server": row.Name, "transport": cfg.Transport, "tools": len(infos)}})

		for _, info := range infos {
			t := mcp.NewAgentTool(session, row.Name, info)
			tools = append(tools, t)
		}
	}
	return tools, cleanup, nil
}

type serverDto struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   *string           `json:"description"`
	Transport     string            `json:"transport"`
	Command       *string           `json:"command"`
	Args          []string          `json:"args"`
	Env           map[string]string `json:"env" doc:"Values may reference secrets as {{secret:NAME}}"`
	URL           *string           `json:"url"`
	Headers       map[string]string `json:"headers" doc:"Values may reference secrets as {{secret:NAME}}"`
	OAuth         oauthConfig       `json:"oauth"`
	Auth          authDto           `json:"auth"`
	Enabled       bool              `json:"enabled"`
	Tools         []mcp.ToolInfo    `json:"tools"`
	ToolsCachedAt *int64            `json:"toolsCachedAt"`
	CreatedAt     int64             `json:"createdAt"`
	UpdatedAt     int64             `json:"updatedAt"`
}

func (m *Module) toDto(s mcpserversdb.McpServer) serverDto {
	d := serverDto{ID: s.ID, Name: s.Name, Description: s.Description, Transport: s.Transport, Command: s.Command, URL: s.Url, Enabled: s.Enabled,
		ToolsCachedAt: s.ToolsCachedAt, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Args: []string{}, Env: map[string]string{}, Headers: map[string]string{}, Tools: []mcp.ToolInfo{}}
	_ = json.Unmarshal([]byte(s.Args), &d.Args)
	_ = json.Unmarshal([]byte(s.Env), &d.Env)
	_ = json.Unmarshal([]byte(s.Headers), &d.Headers)
	_ = json.Unmarshal([]byte(s.OauthConfig), &d.OAuth)
	if s.ToolsCache != nil {
		_ = json.Unmarshal([]byte(*s.ToolsCache), &d.Tools)
	}
	d.Auth = authOf(s, d.Headers)
	if s.Transport == mcp.TransportHTTP {
		d.Auth.CallbackURL = m.callbackURL(s.ID)
	}
	return d
}

var listSpec = &listquery.Spec{
	Select: "SELECT id, workspace_id, name, description, transport, command, args, env, url, headers, tools_cache, tools_cached_at, enabled, created_at, updated_at, " +
		"oauth_config, oauth_supported, oauth_expires_at, oauth_refreshable, oauth_logged_in_at FROM mcp_servers",
	From:          "FROM mcp_servers",
	Sorts:         map[string]string{"name": "name", "transport": "transport", "createdAt": "created_at", "toolsCachedAt": "tools_cached_at"},
	NullableSorts: []string{"toolsCachedAt"},
	DefaultSort:   "name",
	Search:        []string{"name", "description", "command", "url"},
	TieBreaker:    "id",
}

type listInput struct {
	httpserver.ListParams
	Transport string `query:"transport"`
}

func (m *Module) list(ctx context.Context, in *listInput) (*httpserver.PaginatedOutput[serverDto], error) {
	q := listquery.New(listSpec).WhereEq("workspace_id", principal.WorkspaceID(ctx))
	q.WhereIn("transport", listquery.SplitCSV(in.Transport))
	items, total, err := listquery.Run(ctx, m.db, q, in.ToQuery(), func(rows *sql.Rows) (serverDto, error) {
		var s mcpserversdb.McpServer
		err := rows.Scan(&s.ID, &s.WorkspaceID, &s.Name, &s.Description, &s.Transport, &s.Command, &s.Args, &s.Env, &s.Url, &s.Headers, &s.ToolsCache, &s.ToolsCachedAt, &s.Enabled, &s.CreatedAt, &s.UpdatedAt,
			&s.OauthConfig, &s.OauthSupported, &s.OauthExpiresAt, &s.OauthRefreshable, &s.OauthLoggedInAt)
		return m.toDto(s), err
	})
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

type serverBody struct {
	Name        string            `json:"name" minLength:"1" maxLength:"64" pattern:"^[A-Za-z0-9_-]+$"`
	Description string            `json:"description,omitempty" maxLength:"500"`
	Transport   string            `json:"transport" enum:"stdio,http"`
	Command     string            `json:"command,omitempty" maxLength:"500"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty" maxLength:"2000"`
	Headers     map[string]string `json:"headers,omitempty"`
	OAuth       *oauthConfig      `json:"oauth,omitempty" doc:"OAuth client settings of HTTP servers, only needed when the server's authorization server can't register clients itself"`
	Enabled     *bool             `json:"enabled,omitempty"`
}

func (m *Module) validate(ctx context.Context, b *serverBody) error {
	switch b.Transport {
	case mcp.TransportStdio:
		if strings.TrimSpace(b.Command) == "" {
			return apperror.InvalidField("command", "required", "is required for stdio servers")
		}
	case mcp.TransportHTTP:
		if err := m.deps.Egress.CheckURL(ctx, "url", b.URL); err != nil {
			return err
		}
	}
	if b.Args == nil {
		b.Args = []string{}
	}
	return validateOAuth(b)
}

type idOutput struct {
	Body serverDto
}

type createInput struct {
	Body serverBody
}

func (m *Module) create(ctx context.Context, in *createInput) (*idOutput, error) {
	if err := m.validate(ctx, &in.Body); err != nil {
		return nil, err
	}
	b := in.Body
	args, _ := json.Marshal(b.Args)
	env, _ := json.Marshal(orEmpty(b.Env))
	headers, _ := json.Marshal(orEmpty(b.Headers))
	oauth, _ := json.Marshal(orEmptyOAuth(b.OAuth)) // #nosec G117 -- the client secret is kept like header values, which should reference secrets as {{secret:NAME}}
	id := database.NewID()
	wid := principal.WorkspaceID(ctx)
	err := m.queries.CreateServer(ctx, mcpserversdb.CreateServerParams{
		ID: id, WorkspaceID: wid, Name: b.Name, Description: nonEmpty(b.Description), Transport: b.Transport,
		Command: nonEmpty(b.Command), Args: string(args), Env: string(env), Url: nonEmpty(b.URL), Headers: string(headers),
		OauthConfig: string(oauth), Enabled: b.Enabled == nil || *b.Enabled, Now: database.Now(),
	})
	if database.IsUniqueViolation(err) {
		return nil, apperror.AlreadyInUse("MCP server name")
	} else if err != nil {
		return nil, fmt.Errorf("failed to create MCP server: %w", err)
	}

	// Detect an OAuth login right away, so the UI can start it like codex mcp add does
	s, err := m.queries.GetServer(ctx, mcpserversdb.GetServerParams{WorkspaceID: wid, ID: id})
	if err != nil {
		return nil, err
	}
	m.detectOAuth(ctx, wid, s)
	return m.get(ctx, &idInput{ID: id})
}

type idInput struct {
	ID string `path:"id"`
}

func (m *Module) get(ctx context.Context, in *idInput) (*idOutput, error) {
	s, err := m.queries.GetServer(ctx, mcpserversdb.GetServerParams{WorkspaceID: principal.WorkspaceID(ctx), ID: in.ID})
	if database.IsNotFound(err) {
		return nil, apperror.NotFound("MCP server")
	} else if err != nil {
		return nil, err
	}
	return &idOutput{Body: m.toDto(s)}, nil
}

type updateInput struct {
	ID   string `path:"id"`
	Body serverBody
}

func (m *Module) update(ctx context.Context, in *updateInput) (*idOutput, error) {
	if err := m.validate(ctx, &in.Body); err != nil {
		return nil, err
	}
	b := in.Body
	wid := principal.WorkspaceID(ctx)
	before, err := m.queries.GetServer(ctx, mcpserversdb.GetServerParams{WorkspaceID: wid, ID: in.ID})
	if database.IsNotFound(err) {
		return nil, apperror.NotFound("MCP server")
	} else if err != nil {
		return nil, err
	}

	args, _ := json.Marshal(b.Args)
	env, _ := json.Marshal(orEmpty(b.Env))
	headers, _ := json.Marshal(orEmpty(b.Headers))
	oauth, _ := json.Marshal(orEmptyOAuth(b.OAuth)) // #nosec G117 -- the client secret is kept like header values, which should reference secrets as {{secret:NAME}}

	// A login belongs to one server URL and client, so changing either needs a new login, like Codex keys its logins by URL
	// The login is dropped in the same transaction as the change, so no connection ever sends its token to the new URL
	var beforeOAuth oauthConfig
	_ = json.Unmarshal([]byte(before.OauthConfig), &beforeOAuth)
	urlChanged := before.Transport != b.Transport || deref(before.Url) != strings.TrimSpace(b.URL)
	loginChanged := urlChanged || beforeOAuth.ClientID != orEmptyOAuth(b.OAuth).ClientID
	err = m.db.InTx(ctx, func(tx *database.Tx) error {
		q := mcpserversdb.New(tx)
		n, err := q.UpdateServer(ctx, mcpserversdb.UpdateServerParams{
			Name: b.Name, Description: nonEmpty(b.Description), Transport: b.Transport, Command: nonEmpty(b.Command), Args: string(args),
			Env: string(env), Url: nonEmpty(b.URL), Headers: string(headers), OauthConfig: string(oauth), Enabled: b.Enabled == nil || *b.Enabled,
			UpdatedAt: database.Now(), WorkspaceID: wid, ID: in.ID,
		})
		if database.IsUniqueViolation(err) {
			return apperror.AlreadyInUse("MCP server name")
		} else if err != nil {
			return err
		}
		if n == 0 {
			return apperror.NotFound("MCP server")
		}
		if loginChanged {
			if _, err := q.ClearOAuthLogin(ctx, mcpserversdb.ClearOAuthLoginParams{WorkspaceID: wid, ID: in.ID}); err != nil {
				return err
			}
		}
		if urlChanged {
			return q.SetOAuthSupported(ctx, mcpserversdb.SetOAuthSupportedParams{WorkspaceID: wid, ID: in.ID})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if loginChanged {
		m.forgetTokens(in.ID)
	}

	// Detect again when the endpoint or its headers changed, or when no detection has succeeded yet
	var beforeHeaders map[string]string
	_ = json.Unmarshal([]byte(before.Headers), &beforeHeaders)
	if urlChanged || before.OauthSupported == nil || !maps.Equal(beforeHeaders, orEmpty(b.Headers)) {
		s, err := m.queries.GetServer(ctx, mcpserversdb.GetServerParams{WorkspaceID: wid, ID: in.ID})
		if err != nil {
			return nil, err
		}
		m.detectOAuth(ctx, wid, s)
	}
	return m.get(ctx, &idInput{ID: in.ID})
}

func (m *Module) delete(ctx context.Context, in *idInput) (*struct{}, error) {
	n, err := m.queries.DeleteServer(ctx, mcpserversdb.DeleteServerParams{WorkspaceID: principal.WorkspaceID(ctx), ID: in.ID})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, apperror.NotFound("MCP server")
	}

	// The cached refresher holds the decrypted login, which must not outlive the server
	m.forgetTokens(in.ID)
	return nil, nil
}

type testOutput struct {
	Body struct {
		OK        bool           `json:"ok"`
		Error     string         `json:"error,omitempty"`
		Tools     []mcp.ToolInfo `json:"tools"`
		LatencyMs int64          `json:"latencyMs"`
		Auth      authDto        `json:"auth" doc:"The server's authentication status after the test"`
	}
}

// test connects to the server, lists its tools and caches them; stdio servers are started in a temporary sandbox
func (m *Module) test(ctx context.Context, in *idInput) (*testOutput, error) {
	wid := principal.WorkspaceID(ctx)
	s, err := m.queries.GetServer(ctx, mcpserversdb.GetServerParams{WorkspaceID: wid, ID: in.ID})
	if database.IsNotFound(err) {
		return nil, apperror.NotFound("MCP server")
	} else if err != nil {
		return nil, err
	}
	cfg, err := m.serverConfig(ctx, wid, s, nil)
	if err != nil {
		return nil, err
	}

	out := &testOutput{}
	out.Body.Tools = []mcp.ToolInfo{}
	start := time.Now()
	testCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	var sb sandbox.Sandbox
	if cfg.Transport == mcp.TransportStdio {
		if m.deps.Adapter == nil {
			return nil, apperror.Unsupported("No sandbox adapter is available to test stdio servers")
		}

		// Test sandboxes skip the run queue and its concurrency limit, so each person may start only a few of them a minute in a workspace
		err = middleware.CheckRateLimit(ctx, m.deps.TestLimiter, "mcp-test:"+wid+":"+principal.CallerID(ctx))
		if err != nil {
			return nil, err
		}

		// The test sandbox reaches the internet through the egress proxy, so a server started with npx or uvx can install itself
		token := crypto.RandomToken(32)
		if m.deps.GrantProxy != nil {
			revoke := m.deps.GrantProxy(token, sandbox.NetworkInternet)
			defer revoke()
		}
		sb, err = m.deps.Adapter.Create(testCtx, sandbox.Spec{
			RunID: "mcptest-" + database.NewID(), WorkspaceID: wid, Image: m.deps.DefaultImage(ctx, wid),
			Network: sandbox.NetworkInternet, AgentUser: sandbox.UserAgent, TTL: 10 * time.Minute,
			Resources: sandbox.Resources{CPUs: 1, MemoryMB: 1024},
			Broker:    sandbox.BrokerAccess{Token: token},
		})
		if err != nil {
			out.Body.Error = "failed to start a test sandbox: " + err.Error()
			return out, nil
		}
		defer func() {
			if err := m.deps.Adapter.Destroy(context.WithoutCancel(ctx), sb.ID()); err != nil {
				slog.WarnContext(ctx, "Failed to destroy MCP test sandbox", slog.Any("error", err))
			}
		}()
	}

	session, err := m.manager.Connect(testCtx, cfg, sb)
	if err == nil {
		defer session.Close()
		var tools []mcp.ToolInfo
		tools, err = session.Tools(testCtx)
		if tools != nil {
			out.Body.Tools = tools
		}
	}
	out.Body.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		out.Body.Error = connectError(err)
	} else {
		out.Body.OK = true
		cache, _ := json.Marshal(out.Body.Tools)
		_ = m.queries.SetToolsCache(ctx, mcpserversdb.SetToolsCacheParams{WorkspaceID: wid, ID: in.ID, ToolsCache: new(string(cache)), ToolsCachedAt: new(database.Now())})
	}

	// A server without a login is checked for OAuth again, so a test tells whether it needs one
	if cfg.OAuth == nil {
		m.detectOAuth(ctx, wid, s)
	}
	if s, err = m.queries.GetServer(ctx, mcpserversdb.GetServerParams{WorkspaceID: wid, ID: in.ID}); err == nil {
		out.Body.Auth = m.toDto(s).Auth
	}
	return out, nil
}

// JobServer attaches a server to a job, optionally limited to some tools
type JobServer struct {
	ServerID     string   `json:"serverId"`
	ServerName   string   `json:"serverName,omitempty" readOnly:"true"`
	AllowedTools []string `json:"allowedTools" doc:"null exposes all tools"`
}

type jobServersOutput struct {
	Body []JobServer
}

func (m *Module) getJobServers(ctx context.Context, in *idInput) (*jobServersOutput, error) {
	wid := principal.WorkspaceID(ctx)
	if err := m.deps.Jobs.JobExists(ctx, wid, in.ID); err != nil {
		return nil, err
	}
	rows, err := m.queries.ListJobServers(ctx, mcpserversdb.ListJobServersParams{WorkspaceID: wid, JobID: in.ID})
	if err != nil {
		return nil, err
	}
	out := &jobServersOutput{Body: []JobServer{}}
	for _, r := range rows {
		js := JobServer{ServerID: r.ID, ServerName: r.Name}
		if r.AllowedTools != nil {
			_ = json.Unmarshal([]byte(*r.AllowedTools), &js.AllowedTools)
		}
		out.Body = append(out.Body, js)
	}
	return out, nil
}

type setJobServersInput struct {
	ID   string `path:"id"`
	Body []JobServer
}

func (m *Module) setJobServers(ctx context.Context, in *setJobServersInput) (*jobServersOutput, error) {
	wid := principal.WorkspaceID(ctx)
	if err := m.deps.Jobs.JobExists(ctx, wid, in.ID); err != nil {
		return nil, err
	}
	err := m.db.InTx(ctx, func(tx *database.Tx) error {
		q := mcpserversdb.New(tx)
		err := q.ClearJobServers(ctx, in.ID)
		if err != nil {
			return err
		}
		for _, js := range in.Body {
			// Servers from other workspaces must never be attachable
			_, err := q.GetServer(ctx, mcpserversdb.GetServerParams{WorkspaceID: wid, ID: js.ServerID})
			if database.IsNotFound(err) {
				return apperror.NotFound("MCP server")
			} else if err != nil {
				return err
			}
			var allowed *string
			if js.AllowedTools != nil {
				raw, _ := json.Marshal(js.AllowedTools)
				allowed = new(string(raw))
			}
			err = q.AddJobServer(ctx, mcpserversdb.AddJobServerParams{JobID: in.ID, McpServerID: js.ServerID, AllowedTools: allowed})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return m.getJobServers(ctx, &idInput{ID: in.ID})
}

func orEmptyOAuth(c *oauthConfig) oauthConfig {
	if c == nil {
		return oauthConfig{}
	}
	return *c
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func nonEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
