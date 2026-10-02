package mcpapi

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/stonith404/umpteenth/backend/internal/apitokens"
	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/middleware"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

// callerKey is where the token verifier leaves the caller for the tool handlers
const callerKey = "umpteenth.caller"

// caller is who sent an MCP request, and what the REST requests made for its tool calls carry over
type caller struct {
	principal  principal.Principal
	revalidate func(context.Context) error
	remoteAddr string
	requestID  string
}

// verifyToken resolves the bearer token of an MCP request, which the SDK then hands to every tool call of that request
// An ump_ token is an API token, and anything else is an access token from the identity provider when OAuth is set up
func (m *Module) verifyToken(ctx context.Context, token string, r *http.Request) (*auth.TokenInfo, error) {
	var c caller
	var expiry time.Time
	var err error
	if strings.HasPrefix(token, apitokens.TokenPrefix) || m.deps.OAuth == nil {
		c, err = m.apiTokenCaller(ctx, token)
	} else {
		c, expiry, err = m.oauthCaller(ctx, token, r.Header.Get(middleware.WorkspaceHeader))
	}
	if err != nil {
		// The SDK writes an error's text into the response, so only client-safe messages get through and anything else is logged
		if appErr, ok := apperror.As(err); ok && appErr.GetStatus() < http.StatusInternalServerError {
			return nil, fmt.Errorf("%w: %s", auth.ErrInvalidToken, appErr.Message())
		}
		slog.ErrorContext(ctx, "Failed to verify an MCP bearer token", slog.Any("error", err))
		return nil, errors.New("failed to verify the token")
	}

	c.remoteAddr = r.RemoteAddr
	c.requestID = httpserver.RequestID(r.Context())
	// The SDK compares the user across a session's requests, which is the token's creator for an API token
	userID := cmp.Or(c.principal.UserID, c.principal.TokenCreatorID)
	return &auth.TokenInfo{UserID: userID, Expiration: expiry, Extra: map[string]any{callerKey: c}}, nil
}

func (m *Module) apiTokenCaller(ctx context.Context, token string) (caller, error) {
	p, err := m.deps.Tokens.ValidateAPIToken(ctx, token)
	if appErr, ok := apperror.As(err); ok && appErr.GetStatus() < http.StatusInternalServerError {
		// The REST API's message only says the token is invalid, while an agent's user also needs to know a removed creator counts
		return caller{}, apperror.New(appErr.Code(), appErr.GetStatus(), "The token is invalid, expired, or its creator lost access to the workspace")
	} else if err != nil {
		return caller{}, err
	}
	workspaceID, tokenID := p.WorkspaceID, p.TokenID
	return caller{
		principal: p,
		revalidate: func(checkCtx context.Context) error {
			return m.deps.Tokens.ValidateAPITokenID(checkCtx, workspaceID, tokenID)
		},
	}, nil
}

// oauthCaller resolves an access token in the workspace the client names, or else where the user last worked
// A tool call ends with its request and the token was checked for it, so the caller needs no revalidation
func (m *Module) oauthCaller(ctx context.Context, token, workspaceID string) (caller, time.Time, error) {
	p, expiry, err := m.deps.OAuth.VerifyAccessToken(ctx, token, workspaceID)
	if err != nil {
		return caller{}, time.Time{}, err
	}
	return caller{principal: p}, expiry, nil
}

// callerOf returns the caller the token verifier resolved for a tool call
func callerOf(req *mcp.CallToolRequest) (caller, bool) {
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		return caller{}, false
	}
	c, ok := req.Extra.TokenInfo.Extra[callerKey].(caller)
	return c, ok
}
