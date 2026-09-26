//go:build unit

package backup

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactKeepsSecretReferencesAndHarmlessValues(t *testing.T) {
	// #nosec G101 -- fake credentials the redaction must catch
	kept, removed := redact(map[string]string{
		"Authorization": "Bearer {{secret:GITHUB_TOKEN}}",
		"X-Api-Key":     "literal-key",
		"Accept":        "application/json",
		"LOG_LEVEL":     "debug",
		"UPSTREAM":      "https://api.example.com",
		"OPENAI":        "sk-abcdefghijklmnopqrstuvwx",
		"GH":            "ghp_abcdefghijklmnopqrstuvwxyz0123",
		"PASSWORD":      "{{secret:DB_PASSWORD}}",
	})
	// #nosec G101 -- secret references, not credentials
	assert.Equal(t, map[string]string{
		"Authorization": "Bearer {{secret:GITHUB_TOKEN}}",
		"Accept":        "application/json",
		"LOG_LEVEL":     "debug",
		"UPSTREAM":      "https://api.example.com",
		"PASSWORD":      "{{secret:DB_PASSWORD}}",
	}, kept)
	assert.ElementsMatch(t, []string{"X-Api-Key", "OPENAI", "GH"}, removed)

	kept, removed = redact(nil)
	assert.Nil(t, kept)
	assert.Nil(t, removed)
}

func TestExportedServersCarryNoCredentialsInURLsOrArgs(t *testing.T) {
	env, removed := redact(map[string]string{"DATABASE_URL": "postgres://app:hunter2@db:5432/app"}) // #nosec G101 -- a fake credential the redaction must catch
	assert.NotContains(t, env, "DATABASE_URL")
	assert.Equal(t, []string{"DATABASE_URL"}, removed)

	args, removed := redactArgs([]string{"-y", "@modelcontextprotocol/server-postgres", "postgresql://app:hunter2@db/app", "--token=abc123", "--api-key", "abc456", "--port", "5432"})
	assert.Equal(t, []string{"-y", "@modelcontextprotocol/server-postgres", redactedValue, "--token=" + redactedValue, "--api-key", redactedValue, "--port", "5432"}, args)
	assert.Equal(t, []string{"args.2", "args.3", "args.5"}, removed)

	url, changed := redactURL("https://user:hunter2@mcp.example.com/sse?token=abc123&region=eu")
	assert.True(t, changed)
	assert.NotContains(t, url, "hunter2")
	assert.NotContains(t, url, "abc123")
	assert.Contains(t, url, "region=eu")

	url, changed = redactURL("https://mcp.example.com/sse?region=eu")
	assert.False(t, changed)
	assert.Equal(t, "https://mcp.example.com/sse?region=eu", url)
}

func TestImportKeepsAttachmentsTheJobAlreadyHas(t *testing.T) {
	var put []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/secrets":
			_, _ = io.WriteString(w, `{"items":[{"id":"s-old","name":"OLD"},{"id":"s-new","name":"NEW"}],"total":2}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/jobs":
			_, _ = io.WriteString(w, `{"items":[{"id":"j1","name":"Digest"}],"total":1}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/jobs/j1/secrets":
			_, _ = io.WriteString(w, `[{"envName":"OLD_TOKEN","secretId":"s-old","secretName":"OLD"}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/jobs/j1/mcp-servers":
			_, _ = io.WriteString(w, `[]`)
		case r.Method == http.MethodPut && r.URL.Path == "/api/jobs/j1/secrets":
			_ = json.NewDecoder(r.Body).Decode(&put)
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"items":[],"total":0}`)
		}
	}))
	defer srv.Close()

	doc := &Document{Format: FormatName, Version: FormatVersion, Secrets: []string{"NEW"}, Jobs: []Job{{
		Fields:  map[string]json.RawMessage{"name": json.RawMessage(`"Digest"`)},
		Secrets: []JobSecret{{Env: "NEW_TOKEN", Secret: "NEW"}},
	}}}
	_, err := Import(context.Background(), NewClient(srv.URL, "tok"), doc, ImportOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []map[string]string{{"envName": "OLD_TOKEN", "secretId": "s-old"}, {"envName": "NEW_TOKEN", "secretId": "s-new"}}, put)
}

func TestOAuthSettingsAreExportedWithoutALiteralSecret(t *testing.T) {
	oauth, removed := exportOAuth(apiServerOAuth{ClientID: "app", ClientSecret: "literal-secret", Scopes: []string{"read"}}) // #nosec G101 -- a fake credential the redaction must catch
	assert.Equal(t, &MCPServerOAuth{ClientID: "app", Scopes: []string{"read"}}, oauth)
	assert.Equal(t, []string{"oauth.clientSecret"}, removed)

	oauth, removed = exportOAuth(apiServerOAuth{ClientID: "app", ClientSecret: "{{secret:CLIENT_SECRET}}"})
	assert.Equal(t, "{{secret:CLIENT_SECRET}}", oauth.ClientSecret)
	assert.Empty(t, removed)

	oauth, removed = exportOAuth(apiServerOAuth{})
	assert.Nil(t, oauth)
	assert.Empty(t, removed)
}

func TestImportRestoresOAuthSettingsAndAsksForALogin(t *testing.T) {
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/mcp-servers":
			_ = json.NewDecoder(r.Body).Decode(&created)
			_, _ = io.WriteString(w, `{"id":"srv1"}`)
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"items":[],"total":0}`)
		}
	}))
	defer srv.Close()

	doc := &Document{Format: FormatName, Version: FormatVersion, MCPServers: []MCPServer{{
		Name: "linear", Transport: "http", URL: "https://mcp.linear.app/mcp", Enabled: true, LoggedIn: true,
		OAuth: &MCPServerOAuth{ClientID: "app", Scopes: []string{"read"}},
	}}}
	report, err := Import(context.Background(), NewClient(srv.URL, "tok"), doc, ImportOptions{})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"clientId": "app", "scopes": []any{"read"}}, created["oauth"])
	assert.Contains(t, report.Todo, "Log in to MCP server linear under MCP Servers, since exports leave out OAuth logins")
}
