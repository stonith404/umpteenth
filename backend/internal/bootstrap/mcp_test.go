//go:build unit

package bootstrap

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/italypaleale/francis/host/local"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/config"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/mcpapi"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// mcpTools are the tools the MCP endpoint offers, so adding or renaming one is a visible decision
var mcpTools = []string{
	"cancel_run", "compile_job", "create_job", "get_job", "get_job_mcp_servers", "get_job_secrets", "get_job_skills", "get_run",
	"list_jobs", "list_mcp_servers", "list_models", "list_run_events", "list_runs", "list_secrets", "list_skills",
	"run_job", "search_docs", "set_job_mcp_servers", "set_job_secrets", "set_job_skills", "update_job",
}

// mcpTestServer is a full instance behind an HTTP test server, with a workspace and an API token for it
type mcpTestServer struct {
	url         string
	db          *database.DB
	workspaceID string
	userID      string
	token       string
}

// newMCPTestServer starts the instance, with configure changing the configuration first when given
func newMCPTestServer(t *testing.T, configure ...func(cfg *config.Config)) *mcpTestServer {
	t.Helper()
	ctx := t.Context()

	cfg := config.Default()
	cfg.App.Env = config.AppEnvDevelopment
	cfg.App.EncryptionKey = "mcp-test-encryption-key"
	cfg.Sandbox.Adapter = config.SandboxAdapterNone
	cfg.FileStorage.Backend = config.FileBackendDatabase
	cfg.Workspaces.Enabled = true
	for _, c := range configure {
		c(cfg)
	}
	require.NoError(t, cfg.Validate())
	db := testutil.NewDatabaseForTest(t)
	fileStorage, err := initStorage(ctx, cfg, db)
	require.NoError(t, err)

	// The actor host has to run, since creating a job arms its schedule through the job's actor, and the modules register their actors before it starts
	// It keeps its state in memory, since sharing the in-memory SQLite database with it locks tables under concurrent writes
	var svc *services
	testutil.NewActorHostForTest(t, func(t *testing.T, h *local.Host) {
		svc, err = initServices(ctx, cfg, db, h, fileStorage, "mcp-test")
		require.NoError(t, err)
	})
	t.Cleanup(svc.close)
	_, handler, err := initRouter(cfg, db, svc)
	require.NoError(t, err)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	s := &mcpTestServer{url: srv.URL, db: db, workspaceID: testutil.SeedWorkspace(t, db), userID: testutil.SeedUser(t, db)}
	testutil.SeedMember(t, db, s.workspaceID, s.userID, "member")
	s.token = s.seedToken(t)
	return s
}

// seedToken stores an API token created by the test user, the way the tokens page would
func (s *mcpTestServer) seedToken(t *testing.T) string {
	t.Helper()
	token := "ump_" + crypto.RandomToken(32)
	testutil.Exec(t, s.db, "INSERT INTO api_tokens (id, workspace_id, name, token_hash, created_by, created_at) VALUES ($1, $2, $3, $4, $5, $6)",
		database.NewID(), s.workspaceID, "MCP", crypto.HashToken(token), s.userID, database.Now())
	return token
}

// bearerTransport adds an Authorization header to every request
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// connect opens an MCP client session with the SDK's own client, which speaks its newest protocol version
func (s *mcpTestServer) connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	return s.connectWith(t, s.token)
}

// connectWith opens an MCP client session that authenticates with the given bearer token
func (s *mcpTestServer) connectWith(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             s.url + mcpapi.Path,
		HTTPClient:           &http.Client{Transport: bearerTransport{token: token}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(t.Context(), transport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// call calls a tool and returns its text, failing the test on a protocol error
func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.Len(t, res.Content, 1)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return text.Text, res.IsError
}

func TestMCPToolsMatchTheirOperations(t *testing.T) {
	s := newMCPTestServer(t)
	session := s.connect(t)

	res, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	names := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		names = append(names, tool.Name)

		// Every schema stands on its own, since clients don't resolve references into the OpenAPI spec
		schema, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		assert.NotContains(t, string(schema), "$ref", tool.Name)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(schema, &parsed))
		assert.Equal(t, "object", parsed["type"], tool.Name)

		require.NotNil(t, tool.Annotations, tool.Name)
		readOnly := strings.HasPrefix(tool.Name, "get_") || strings.HasPrefix(tool.Name, "list_") || strings.HasPrefix(tool.Name, "search_")
		assert.Equal(t, readOnly, tool.Annotations.ReadOnlyHint, tool.Name)
	}
	slices.Sort(names)
	assert.Equal(t, mcpTools, names)

	// An object body is flattened next to the path parameters, and a list body is passed as body
	byName := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}
	update, _ := json.Marshal(byName["update_job"].InputSchema)
	assert.Contains(t, string(update), `"id"`)
	assert.Contains(t, string(update), `"instruction"`)
	set, _ := json.Marshal(byName["set_job_mcp_servers"].InputSchema)
	assert.Contains(t, string(set), `"body"`)
	assert.NotContains(t, string(set), `"serverName"`, "read-only fields are left out of input schemas")
}

func TestMCPCreatesAndReadsAJob(t *testing.T) {
	s := newMCPTestServer(t)
	session := s.connect(t)

	text, isError := call(t, session, "create_job", map[string]any{
		"name":        "Nightly report",
		"instruction": "Summarize yesterday's errors",
		"network":     "none",
	})
	require.False(t, isError, text)
	var job struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Network string `json:"network"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &job))
	assert.Equal(t, "Nightly report", job.Name)
	assert.Equal(t, "none", job.Network)

	text, isError = call(t, session, "get_job", map[string]any{"id": job.ID})
	require.False(t, isError, text)
	assert.Contains(t, text, `"Nightly report"`)

	text, isError = call(t, session, "list_jobs", map[string]any{"search": "nightly", "pageSize": 5})
	require.False(t, isError, text)
	assert.Contains(t, text, job.ID)

	// A list body replaces what the job is given, and reads back the same way
	text, isError = call(t, session, "set_job_secrets", map[string]any{"id": job.ID, "body": []any{}})
	require.False(t, isError, text)
	text, isError = call(t, session, "get_job_secrets", map[string]any{"id": job.ID})
	require.False(t, isError, text)
	assert.Equal(t, "[]", text)

	// The job was created as the token's creator in the token's workspace
	var workspaceID string
	var createdBy *string
	require.NoError(t, s.db.QueryRowContext(t.Context(), "SELECT workspace_id, created_by FROM jobs WHERE id = $1", job.ID).Scan(&workspaceID, &createdBy))
	assert.Equal(t, s.workspaceID, workspaceID)
	assert.Nil(t, createdBy, "tokens create jobs without a user, like the REST API does")
}

func TestMCPSearchesTheDocsOfThisRelease(t *testing.T) {
	// Only the image build copies the docs pages in, so a test build answers with the website, and either way the agent learns where to read
	s := newMCPTestServer(t)
	text, isError := call(t, s.connect(t), "search_docs", map[string]any{"query": "cron schedule", "limit": 3})
	require.False(t, isError, text)
	assert.Contains(t, text, "https://umpteenth.dev/")
}

func TestMCPReportsProblemsToTheAgent(t *testing.T) {
	s := newMCPTestServer(t)
	session := s.connect(t)

	// Validation errors name the flattened argument without Huma's location prefix
	text, isError := call(t, session, "create_job", map[string]any{"name": "", "instruction": "x"})
	assert.True(t, isError)
	assert.Contains(t, text, "name:")
	assert.NotContains(t, text, "body.name")

	text, isError = call(t, session, "create_job", map[string]any{"name": "Job", "instruction": "x", "colour": "blue"})
	assert.True(t, isError)
	assert.Contains(t, text, `Unknown argument "colour"`)

	text, isError = call(t, session, "get_job", map[string]any{"id": "../tokens"})
	assert.True(t, isError)
	assert.Contains(t, text, "not_found")

	text, isError = call(t, session, "get_job", map[string]any{})
	assert.True(t, isError)
	assert.Contains(t, text, `Missing required argument "id"`)

	// Compiling needs a model, which this workspace doesn't have
	text, isError = call(t, session, "compile_job", map[string]any{"instruction": "Check the website every hour"})
	assert.True(t, isError)
	assert.Contains(t, text, "utility model")
}

func TestMCPRequiresAValidToken(t *testing.T) {
	s := newMCPTestServer(t)
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`

	status, _ := s.post(t, "", initialize)
	assert.Equal(t, http.StatusUnauthorized, status)

	status, _ = s.post(t, "ump_not-a-real-token", initialize)
	assert.Equal(t, http.StatusUnauthorized, status)

	// A token stops working once its creator leaves the workspace
	testutil.Exec(t, s.db, "DELETE FROM workspace_members WHERE user_id = $1", s.userID)
	status, body := s.post(t, s.token, initialize)
	assert.Equal(t, http.StatusUnauthorized, status)
	assert.Contains(t, body, "lost access")
}

func TestMCPServesTheOlderProtocol(t *testing.T) {
	s := newMCPTestServer(t)

	// Clients on the 2025-11-25 protocol initialize first and then call tools without a session, which a stateless server accepts
	status, body := s.post(t, s.token, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"protocolVersion":"2025-11-25"`)
	assert.Contains(t, body, "compile_job", "the instructions describe the flow")

	status, body = s.post(t, s.token, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_jobs","arguments":{}}}`, "Mcp-Protocol-Version", "2025-11-25")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `\"items\":[]`)

	// Only POST carries messages
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.url+mcpapi.Path, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+s.token)
	getResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = getResp.Body.Close()
	assert.Equal(t, http.StatusMethodNotAllowed, getResp.StatusCode)
}

// post sends one JSON-RPC message the way a Streamable HTTP client does, with an optional token and extra header pairs, and returns the status and body
func (s *mcpTestServer) post(t *testing.T, token, message string, header ...string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.url+mcpapi.Path, bytes.NewBufferString(message))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}
