// Package mcpapi serves part of the REST API as an MCP server, so agents such as Claude Code can create and run jobs
// Every tool is an existing operation: its schema comes from the OpenAPI spec and a call goes through the same handler as the REST request
package mcpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/stonith404/umpteenth/backend/internal/common"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

// Path is where MCP clients connect, under /api since the SPA owns /mcp
const Path = "/api/mcp"

// ResourceURL is the MCP endpoint's public URL, which is the audience of the access tokens MCP clients sign in with
func ResourceURL(appURL string) string {
	return appURL + Path
}

// metadataPrefix is where MCP clients look up how to sign in to the endpoint, by RFC 9728 its path appended to the well-known prefix
const metadataPrefix = "/.well-known/oauth-protected-resource"

// TokenValidator resolves an API bearer token, like the REST API does
type TokenValidator interface {
	ValidateAPIToken(ctx context.Context, token string) (principal.Principal, error)
	ValidateAPITokenID(ctx context.Context, workspaceID, tokenID string) error
}

// OAuthVerifier resolves access tokens MCP clients got from the identity provider
type OAuthVerifier interface {
	VerifyAccessToken(ctx context.Context, raw, workspaceID string) (principal.Principal, time.Time, error)
}

type Dependencies struct {
	Tokens TokenValidator
	// OAuth verifies access tokens of the identity provider at OAuthIssuer, and is nil when MCP clients can only use API tokens
	OAuth       OAuthVerifier
	OAuthIssuer string
	// AppURL is the instance's public URL, which the resource MCP clients get access tokens for starts with
	AppURL string
}

type Module struct {
	deps Dependencies
}

func New(deps Dependencies) *Module {
	return &Module{deps: deps}
}

// RegisterRoutes mounts the MCP endpoint on mux, with tools built from the operations registered on api and served by handler
// It has to run after every module registered its routes, since the tools are looked up in the finished spec
func (m *Module) RegisterRoutes(mux *http.ServeMux, api huma.API, handler http.Handler) error {
	server, err := newServer(api.OpenAPI(), handler)
	if err != nil {
		return err
	}

	// A stateless server keeps nothing between requests, so any replica can answer any call
	// Localhost protection guards ambient credentials against DNS rebinding, but this endpoint only takes bearer tokens, and the check would refuse a reverse proxy on the same host
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		JSONResponse:               true,
		DisableLocalhostProtection: true,
	})

	// API tokens can be valid forever, which the SDK only accepts when told so
	opts := &auth.RequireBearerTokenOptions{AllowMissingExpiration: true}

	// With OAuth, a client that gets a 401 learns from the metadata which identity provider to sign in with
	if m.deps.OAuth != nil {
		resource := ResourceURL(m.deps.AppURL)
		u, err := url.Parse(resource)
		if err != nil {
			return fmt.Errorf("invalid MCP resource URL: %w", err)
		}
		opts.ResourceMetadataURL = u.Scheme + "://" + u.Host + metadataPrefix + u.Path
		metadata := auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
			Resource:               resource,
			AuthorizationServers:   []string{m.deps.OAuthIssuer},
			BearerMethodsSupported: []string{"header"},
			ResourceName:           "Umpteenth",
		})
		// Clients that don't append the endpoint's path look the metadata up at the bare prefix
		mux.Handle(metadataPrefix+u.Path, metadata)
		mux.Handle(metadataPrefix, metadata)
	}

	// The pattern has no method, so the SDK answers anything but POST itself
	mux.Handle(Path, auth.RequireBearerToken(m.verifyToken, opts)(mcpHandler))
	return nil
}

func newServer(spec *huma.OpenAPI, handler http.Handler) (*mcp.Server, error) {
	server := mcp.NewServer(&mcp.Implementation{Name: "umpteenth", Title: "Umpteenth", Version: common.Version}, &mcp.ServerOptions{
		Instructions: instructions,
	})
	tools, err := buildTools(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to build MCP tools: %w", err)
	}
	for _, t := range tools {
		server.AddTool(t.tool, t.handler(handler))
	}

	// The docs of this release are searchable next to the API, so an agent can look up how a setting works without leaving the server
	sections, err := loadDocs(docsFS)
	if err != nil {
		return nil, fmt.Errorf("failed to load the docs: %w", err)
	}
	docs, err := newDocsIndex(context.Background(), sections)
	if err != nil {
		return nil, err
	}
	server.AddTool(searchDocsTool, docs.handler)
	return server, nil
}

// instructions tell the agent how Umpteenth fits together, which no single tool description can
const instructions = `Umpteenth runs agentic jobs: each run of a job gets a disposable sandbox where an LLM agent works with shell tools and MCP servers.

A job has a name, an instruction (the prompt the agent reads on every run) and a spec compiled from that instruction: goal, success criteria, inputs, outputs, schedule, network access and the MCP servers it needs.
A run is one execution of a job. Its status moves from queued through provisioning, running and verifying to succeeded, failed, cancelled, timed_out or skipped.

To create a job:
1. Find out what the workspace offers with list_mcp_servers, list_secrets and list_skills.
2. Call compile_job with the instruction. If it returns questions, ask the user, add the answers to the instruction and compile again with askQuestions false.
3. Call create_job with a name (the spec's title works), the instruction, the compiled spec and network from spec.network. For a recurring job, also pass cron and timezone from spec.schedule.
4. Give the job what it needs: set_job_mcp_servers with the servers in spec.mcp, set_job_secrets with the secrets compile_job matched and the environment variables it named, and set_job_skills with the skills it suggested. These take IDs, which the lists from step 1 map from names.

To run a job, call run_job, then poll get_run every few seconds until its status is final. The run's summary, outputs and error say how it went, and list_run_events shows what the agent did step by step.

search_docs searches the Umpteenth documentation of this release. Read its guide on writing instructions before you write one, and search it whenever a setting, error or behavior is unclear.`
