package mcpservers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/mcp"
	"github.com/stonith404/umpteenth/backend/internal/mcpservers/mcpserversdb"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// OAuth logins of HTTP servers, detected and performed the way Codex does it (PLAN.md §8)
// The browser leaves for the authorization server and comes back to a per-server callback, and the started login waits in the database so any replica can finish it

// pendingLoginTTL bounds how long the browser may take at the authorization server
const pendingLoginTTL = 10 * time.Minute

const (
	// refreshLeaseTTL outlasts a token request, so a replica's lease on a refresh only runs out when that replica died
	refreshLeaseTTL = time.Minute
	// refreshLeasePoll is how often a replica checks whether another one finished refreshing the login
	refreshLeasePoll = 200 * time.Millisecond
)

// Authentication states, the same ones Codex shows for its MCP servers
const (
	authUnsupported = "unsupported"
	authUnknown     = "unknown"
	authBearerToken = "bearer_token"
	authNotLoggedIn = "not_logged_in"
	authOAuth       = "oauth"
)

// oauthConfig holds the optional OAuth client settings of an HTTP server, like Codex's per-server OAuth settings
type oauthConfig struct {
	ClientID     string   `json:"clientId,omitempty" maxLength:"500" doc:"A client registered with the authorization server beforehand, for servers without dynamic client registration"`
	ClientSecret string   `json:"clientSecret,omitempty" maxLength:"2000" doc:"The client's secret, may reference a secret as {{secret:NAME}}"`
	Scopes       []string `json:"scopes,omitempty" maxItems:"50" doc:"Scopes to request instead of the ones the server advertises"`
}

// authDto is a server's authentication status
type authDto struct {
	Status      string `json:"status" enum:"unsupported,unknown,bearer_token,not_logged_in,oauth" doc:"unsupported: no login needed or possible, unknown: OAuth detection hasn't succeeded yet, bearer_token: an Authorization header is configured, not_logged_in: the server advertises OAuth but has no usable login, oauth: logged in"`
	LoggedInAt  *int64 `json:"loggedInAt" doc:"When the OAuth login was made, also set once it expired"`
	ExpiresAt   *int64 `json:"expiresAt" doc:"When the current access token expires"`
	Refreshable bool   `json:"refreshable" doc:"The login has a refresh token, so it outlives its access token"`
	CallbackURL string `json:"callbackUrl,omitempty" doc:"Where the authorization server sends the browser back to, which an OAuth client configured by hand must allow"`
}

// authOf derives the status from what is stored, so listing servers never waits on the network
func authOf(s mcpserversdb.McpServer, headers map[string]string) authDto {
	a := authDto{LoggedInAt: s.OauthLoggedInAt, ExpiresAt: s.OauthExpiresAt, Refreshable: s.OauthRefreshable}
	switch {
	case s.Transport != mcp.TransportHTTP:
		a.Status = authUnsupported
	case mcp.HasAuthorizationHeader(headers):
		a.Status = authBearerToken
	case s.OauthLoggedInAt != nil && mcp.LoginUsable(deref64(s.OauthExpiresAt), s.OauthRefreshable, time.Now()):
		a.Status = authOAuth
	case s.OauthLoggedInAt != nil || (s.OauthSupported != nil && *s.OauthSupported):
		a.Status = authNotLoggedIn
	case s.OauthSupported == nil:
		a.Status = authUnknown
	default:
		a.Status = authUnsupported
	}
	return a
}

// pendingLogin is a started login, bound to the user who started it
type pendingLogin struct {
	mcp.OAuthLogin
	UserID    string `json:"userId"`
	ExpiresAt int64  `json:"expiresAt"`
}

type loginOutput struct {
	Body struct {
		AuthorizationURL string `json:"authorizationUrl" doc:"Where to send the browser to log in"`
	}
}

// login starts an OAuth login and returns the authorization server URL the browser goes to next
func (m *Module) login(ctx context.Context, in *idInput) (*loginOutput, error) {
	// The route only takes signed-in sessions, since the pending login belongs to the user who started it
	p, _ := principal.From(ctx)
	s, err := m.queries.GetServer(ctx, mcpserversdb.GetServerParams{WorkspaceID: p.WorkspaceID, ID: in.ID})
	if database.IsNotFound(err) {
		return nil, apperror.NotFound("MCP server")
	} else if err != nil {
		return nil, err
	}
	if s.Transport != mcp.TransportHTTP {
		return nil, apperror.Unsupported("Only HTTP servers can log in with OAuth")
	}

	authURL, err := m.beginLogin(ctx, p.WorkspaceID, p.UserID, s, false)
	if err != nil {
		return nil, err
	}
	out := &loginOutput{}
	out.Body.AuthorizationURL = authURL
	return out, nil
}

// beginLogin discovers the authorization server, registers a client if needed and stores the started login
func (m *Module) beginLogin(ctx context.Context, workspaceID, userID string, s mcpserversdb.McpServer, withoutScopes bool) (string, error) {
	headers, err := m.expandedHeaders(ctx, workspaceID, s)
	if err != nil {
		return "", err
	}
	var oc oauthConfig
	_ = json.Unmarshal([]byte(s.OauthConfig), &oc)
	secret, err := m.deps.Secrets.Expand(ctx, workspaceID, oc.ClientSecret)
	if err != nil {
		return "", err
	}

	// Discovery, client registration and the authorization URL all come from the engine
	authURL, login, err := m.manager.BeginOAuthLogin(ctx, mcp.OAuthLoginRequest{
		ServerURL: deref(s.Url), Headers: headers, RedirectURL: m.callbackURL(s.ID),
		Client: mcp.OAuthClient{ID: oc.ClientID, Secret: secret}, Scopes: oc.Scopes, WithoutScopes: withoutScopes,
	})
	if errors.Is(err, mcp.ErrOAuthUnsupported) {
		_ = m.queries.SetOAuthSupported(ctx, mcpserversdb.SetOAuthSupportedParams{WorkspaceID: workspaceID, ID: s.ID, OauthSupported: new(false)})
		return "", apperror.Unsupported("This server doesn't advertise an OAuth login")
	} else if err != nil {
		return "", apperror.ProviderError(err, "Failed to start the OAuth login: "+err.Error())
	}

	// The login waits encrypted in the database, since it carries the PKCE verifier and possibly a client secret
	raw, err := json.Marshal(pendingLogin{OAuthLogin: login, UserID: userID, ExpiresAt: time.Now().Add(pendingLoginTTL).UnixMilli()})
	if err != nil {
		return "", err
	}
	sealed, err := crypto.Encrypt(m.oauthKey, raw)
	if err != nil {
		return "", err
	}
	err = m.queries.SetOAuthPending(ctx, mcpserversdb.SetOAuthPendingParams{WorkspaceID: workspaceID, ID: s.ID, OauthPending: sealed})
	if err != nil {
		return "", fmt.Errorf("failed to store the started login: %w", err)
	}
	return authURL, nil
}

// callbackURL is where the authorization server sends the browser back to
// Each server has its own, so a code can only come back to the login of the server it was issued for
func (m *Module) callbackURL(serverID string) string {
	return m.deps.AppURL + "/api/mcp-servers/" + url.PathEscape(serverID) + "/oauth/callback"
}

type callbackInput struct {
	ID               string `path:"id"`
	Code             string `query:"code"`
	State            string `query:"state"`
	Iss              string `query:"iss"`
	Error            string `query:"error"`
	ErrorDescription string `query:"error_description"`
}

type redirectOutput struct {
	Status   int
	Location string `header:"Location"`
}

// callback finishes a login when the browser comes back, and sends it on to the MCP servers page with the outcome
func (m *Module) callback(ctx context.Context, in *callbackInput) (*redirectOutput, error) {
	p, _ := principal.From(ctx)
	back := func(message string) (*redirectOutput, error) {
		query := url.Values{"server": {in.ID}}
		if message == "" {
			query.Set("oauth", "success")
		} else {
			query.Set("oauthError", message)
		}
		return &redirectOutput{Status: http.StatusFound, Location: "/mcp?" + query.Encode()}, nil
	}

	// The pending login is checked against this user and state before it is taken, so a forged callback can't use it up
	sealed, err := m.queries.GetOAuthPending(ctx, mcpserversdb.GetOAuthPendingParams{WorkspaceID: p.WorkspaceID, ID: in.ID})
	if database.IsNotFound(err) {
		return back("The login expired or was already finished, start it again")
	} else if err != nil {
		return nil, err
	}
	var pending pendingLogin
	raw, err := crypto.Decrypt(m.oauthKey, sealed)
	if err == nil {
		err = json.Unmarshal(raw, &pending)
	}
	if err != nil || pending.UserID != p.UserID || time.Now().UnixMilli() > pending.ExpiresAt || subtle.ConstantTimeCompare([]byte(in.State), []byte(pending.State)) != 1 {
		return back("The login expired or doesn't belong to this browser session, start it again")
	}

	// Taking the pending login clears it, so the same callback URL can't be used twice
	taken, err := m.queries.ClearOAuthPending(ctx, mcpserversdb.ClearOAuthPendingParams{WorkspaceID: p.WorkspaceID, ID: in.ID, OauthPending: sealed})
	if err != nil {
		return nil, err
	}
	if taken == 0 {
		return back("The login expired or was already finished, start it again")
	}

	// A provider that rejects the discovered scopes gets one more attempt without them, like Codex
	if in.Error != "" {
		if (in.Error == "invalid_scope" || in.Error == "invalid_request") && pending.ScopesDiscovered {
			s, err := m.queries.GetServer(ctx, mcpserversdb.GetServerParams{WorkspaceID: p.WorkspaceID, ID: in.ID})
			if err == nil {
				authURL, err := m.beginLogin(ctx, p.WorkspaceID, p.UserID, s, true)
				if err == nil {
					return &redirectOutput{Status: http.StatusFound, Location: authURL}, nil
				}
			}
		}
		message := in.Error
		if in.ErrorDescription != "" {
			message += ": " + in.ErrorDescription
		}
		return back("The authorization server refused the login (" + message + ")")
	}

	// Exchange the code, then store the login encrypted
	creds, err := m.manager.FinishOAuthLogin(ctx, pending.OAuthLogin, in.State, in.Code, in.Iss)
	if err != nil {
		slog.InfoContext(ctx, "MCP OAuth login failed", slog.String("server", in.ID), slog.Any("error", err))
		return back("Failed to finish the login: " + err.Error())
	}
	if err := m.saveLogin(ctx, p.WorkspaceID, in.ID, creds); err != nil {
		return nil, err
	}
	return back("")
}

// saveLogin stores a new login, replacing the previous one
func (m *Module) saveLogin(ctx context.Context, workspaceID, serverID string, creds mcp.OAuthCredentials) error {
	sealed, err := m.sealCredentials(creds)
	if err != nil {
		return err
	}
	err = m.queries.SaveOAuthLogin(ctx, mcpserversdb.SaveOAuthLoginParams{
		WorkspaceID: workspaceID, ID: serverID, OauthCredentials: sealed, OauthKeyID: new(crypto.KeyIDV1),
		OauthExpiresAt: nonZero(creds.ExpiresAt), OauthRefreshable: creds.RefreshToken != "", OauthLoggedInAt: new(database.Now()),
	})
	if err != nil {
		return fmt.Errorf("failed to store the OAuth login: %w", err)
	}
	m.forgetTokens(serverID)
	return nil
}

// logout drops a server's OAuth login, like codex mcp logout
func (m *Module) logout(ctx context.Context, in *idInput) (*idOutput, error) {
	n, err := m.queries.ClearOAuthLogin(ctx, mcpserversdb.ClearOAuthLoginParams{WorkspaceID: principal.WorkspaceID(ctx), ID: in.ID})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, apperror.NotFound("MCP server")
	}
	m.forgetTokens(in.ID)
	return m.get(ctx, in)
}

// detectOAuth records whether an HTTP server advertises an OAuth login, which tells "not logged in" apart from "no login needed"
// A failed detection keeps what was known before, since it usually means the server was briefly unreachable
func (m *Module) detectOAuth(ctx context.Context, workspaceID string, s mcpserversdb.McpServer) {
	if s.Transport != mcp.TransportHTTP {
		return
	}
	headers, err := m.expandedHeaders(ctx, workspaceID, s)
	if err != nil || mcp.HasAuthorizationHeader(headers) {
		return
	}
	meta, err := m.manager.DiscoverOAuth(ctx, deref(s.Url), headers)
	if err != nil {
		slog.DebugContext(ctx, "Failed to detect OAuth support of an MCP server", slog.String("server", s.Name), slog.Any("error", err))
		return
	}
	err = m.queries.SetOAuthSupported(ctx, mcpserversdb.SetOAuthSupportedParams{WorkspaceID: workspaceID, ID: s.ID, OauthSupported: new(meta != nil)})
	if err != nil {
		slog.WarnContext(ctx, "Failed to store OAuth support of an MCP server", slog.String("server", s.Name), slog.Any("error", err))
	}
}

// oauthTokens returns the server's token refresher, shared by every session on this replica so concurrent runs refresh once
func (m *Module) oauthTokens(workspaceID string, s mcpserversdb.McpServer) (*mcp.OAuthTokens, error) {
	m.tokensMu.Lock()
	defer m.tokensMu.Unlock()
	loggedInAt := deref64(s.OauthLoggedInAt)
	if cached, ok := m.tokens[s.ID]; ok && cached.loggedInAt == loggedInAt {
		return cached.tokens, nil
	}
	creds, err := m.openCredentials(s.OauthCredentials)
	if err != nil {
		return nil, err
	}
	creds.LoggedInAt = loggedInAt
	tokens := m.manager.NewOAuthTokens(creds, oauthStore{m: m, workspaceID: workspaceID, serverID: s.ID})
	m.tokens[s.ID] = cachedTokens{loggedInAt: loggedInAt, tokens: tokens}
	return tokens, nil
}

// forgetTokens drops the cached refresher after a login changed, so the next session reads the new one
func (m *Module) forgetTokens(serverID string) {
	m.tokensMu.Lock()
	defer m.tokensMu.Unlock()
	delete(m.tokens, serverID)
}

type cachedTokens struct {
	loggedInAt int64
	tokens     *mcp.OAuthTokens
}

func (m *Module) sealCredentials(creds mcp.OAuthCredentials) ([]byte, error) {
	raw, err := json.Marshal(creds) // #nosec G117 -- the tokens are encrypted right below and never leave the host
	if err != nil {
		return nil, err
	}
	return crypto.Encrypt(m.oauthKey, raw)
}

func (m *Module) openCredentials(sealed []byte) (mcp.OAuthCredentials, error) {
	var creds mcp.OAuthCredentials
	raw, err := crypto.Decrypt(m.oauthKey, sealed)
	if err != nil {
		return creds, fmt.Errorf("failed to decrypt the OAuth login: %w", err)
	}
	return creds, json.Unmarshal(raw, &creds)
}

// oauthStore persists one server's login for its token refresher
type oauthStore struct {
	m           *Module
	workspaceID string
	serverID    string
}

func (s oauthStore) Load(ctx context.Context) (mcp.OAuthCredentials, error) {
	row, err := s.m.queries.GetOAuthLogin(ctx, mcpserversdb.GetOAuthLoginParams{WorkspaceID: s.workspaceID, ID: s.serverID})
	if database.IsNotFound(err) || (err == nil && (row.OauthCredentials == nil || row.OauthLoggedInAt == nil)) {
		return mcp.OAuthCredentials{}, mcp.ErrLoginExpired
	} else if err != nil {
		return mcp.OAuthCredentials{}, err
	}
	creds, err := s.m.openCredentials(row.OauthCredentials)
	creds.LoggedInAt = *row.OauthLoggedInAt
	return creds, err
}

// Lock leases the login's refresh in the database, since the token refresher only serializes the runs of its own replica
func (s oauthStore) Lock(ctx context.Context) (func(), error) {
	key := "mcp-oauth-refresh/" + s.serverID
	for {
		now := database.Now()
		until := strconv.FormatInt(now+refreshLeaseTTL.Milliseconds(), 10)
		n, err := s.m.queries.LeaseOAuthRefresh(ctx, mcpserversdb.LeaseOAuthRefreshParams{Key: key, Until: until, Now: now})
		if err != nil {
			return nil, fmt.Errorf("failed to lease the OAuth refresh: %w", err)
		}
		if n > 0 {
			return func() {
				// The lease is released even when the run was canceled meanwhile, so other replicas don't wait for it to run out
				ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer cancel()
				err := s.m.queries.ReleaseOAuthRefresh(ctx, mcpserversdb.ReleaseOAuthRefreshParams{Key: key, Until: until})
				if err != nil {
					slog.WarnContext(ctx, "Failed to release the lease on an MCP OAuth refresh", slog.Any("error", err))
				}
			}, nil
		}

		// Another replica is refreshing the login, and once its lease is free the refresher reloads what it stored instead of spending the refresh token again
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(refreshLeasePoll):
		}
	}
}

func (s oauthStore) Save(ctx context.Context, creds mcp.OAuthCredentials) error {
	sealed, err := s.m.sealCredentials(creds)
	if err != nil {
		return err
	}
	_, err = s.m.queries.SaveOAuthTokens(ctx, mcpserversdb.SaveOAuthTokensParams{
		WorkspaceID: s.workspaceID, ID: s.serverID, OauthCredentials: sealed, OauthKeyID: new(crypto.KeyIDV1),
		OauthExpiresAt: nonZero(creds.ExpiresAt), OauthRefreshable: creds.RefreshToken != "", OauthLoggedInAt: new(creds.LoggedInAt),
	})
	return err
}

func (s oauthStore) Expire(ctx context.Context, creds mcp.OAuthCredentials) error {
	s.m.forgetTokens(s.serverID)
	return s.m.queries.ClearExpiredOAuthLogin(ctx, mcpserversdb.ClearExpiredOAuthLoginParams{WorkspaceID: s.workspaceID, ID: s.serverID, OauthLoggedInAt: new(creds.LoggedInAt)})
}

// connectError turns a failed connection into advice when the cause is a missing or expired login
func connectError(err error) string {
	switch {
	case errors.Is(err, mcp.ErrLoginExpired):
		return "The OAuth login expired. Log in again under MCP servers."
	case errors.Is(err, mcp.ErrUnauthorized):
		return "The server requires authorization. Log in under MCP servers, or add an Authorization header."
	default:
		return err.Error()
	}
}

// validateOAuth checks the OAuth settings of a server body and keeps them only for HTTP servers
func validateOAuth(b *serverBody) error {
	if b.Transport != mcp.TransportHTTP || b.OAuth == nil {
		b.OAuth = nil
		return nil
	}
	b.OAuth.ClientID = strings.TrimSpace(b.OAuth.ClientID)
	if b.OAuth.ClientSecret != "" && b.OAuth.ClientID == "" {
		return apperror.InvalidField("oauth.clientSecret", "required", "needs a client ID")
	}
	scopes := make([]string, 0, len(b.OAuth.Scopes))
	for _, scope := range b.OAuth.Scopes {
		scope = strings.TrimSpace(scope)
		if strings.ContainsAny(scope, " \t\n") {
			return apperror.InvalidField("oauth.scopes", "invalid", "must not contain spaces")
		}
		if scope != "" {
			scopes = append(scopes, scope)
		}
	}
	b.OAuth.Scopes = scopes
	return nil
}

func nonZero(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func deref64(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
