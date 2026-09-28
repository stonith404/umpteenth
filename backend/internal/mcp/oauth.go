package mcp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// OAuth login for HTTP servers, detected and performed the way Codex does it
// Discovery follows the MCP authorization spec: the endpoint's 401 challenge, the protected resource metadata (RFC 9728), then authorization server metadata (RFC 8414, OpenID Connect discovery)

const (
	// oauthDiscoveryTimeout bounds detection like Codex does, so saving a server never hangs on a slow host
	oauthDiscoveryTimeout = 5 * time.Second
	// oauthRequestTimeout bounds client registration and token requests
	oauthRequestTimeout = 30 * time.Second
	// refreshSkew refreshes an access token this long before it expires, like Codex, so a request never races the expiry
	refreshSkew = 30 * time.Second
	// maxOAuthResponseBytes bounds metadata and token responses
	maxOAuthResponseBytes = 1 << 20
	// oauthClientName is the name authorization servers show on their consent screen
	oauthClientName = "Umpteenth"
)

var (
	// ErrUnauthorized means the server turned the connection away for missing or invalid credentials
	ErrUnauthorized = errors.New("the server requires authorization")
	// ErrLoginExpired means the OAuth login can't be refreshed anymore and has to be done again
	ErrLoginExpired = errors.New("the OAuth login expired, log in again")
	// ErrOAuthUnsupported means the server doesn't advertise an OAuth login
	ErrOAuthUnsupported = errors.New("the server doesn't advertise an OAuth login")
)

// OAuthMetadata is what discovery learned about a server's OAuth login
type OAuthMetadata struct {
	// Resource is the protected resource the tokens are for, sent as the RFC 8707 resource parameter
	Resource              string
	Issuer                string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
	// Scopes are what the server asks for in its challenge and resource metadata, else what the authorization server supports
	Scopes []string
	// OfflineAccess reports that the authorization server issues refresh tokens for the offline_access scope
	OfflineAccess         bool
	IssParameterSupported bool
	TokenAuthMethods      []string
}

// OAuthClient is how a login authenticates at the token endpoint
type OAuthClient struct {
	ID     string `json:"id"`
	Secret string `json:"secret,omitempty"`
	// AuthMethod is client_secret_basic, client_secret_post or none
	AuthMethod string `json:"authMethod,omitempty"`
}

// OAuthLoginRequest starts a login for a server
type OAuthLoginRequest struct {
	ServerURL   string
	Headers     map[string]string
	RedirectURL string
	// Client is used instead of dynamic client registration when it has an ID
	Client OAuthClient
	// Scopes replace the discovered scopes when set
	Scopes []string
	// WithoutScopes requests no scopes, which is the second attempt after a provider rejected the discovered ones
	WithoutScopes bool
}

// OAuthLogin is a started login, kept until the browser comes back to the redirect URL
type OAuthLogin struct {
	State         string      `json:"state"`
	Verifier      string      `json:"verifier"`
	RedirectURL   string      `json:"redirectUrl"`
	Client        OAuthClient `json:"client"`
	TokenEndpoint string      `json:"tokenEndpoint"`
	Issuer        string      `json:"issuer"`
	IssRequired   bool        `json:"issRequired,omitempty"`
	Resource      string      `json:"resource,omitempty"`
	// ScopesDiscovered allows one more attempt without scopes when the provider rejects them, like Codex
	ScopesDiscovered bool `json:"scopesDiscovered,omitempty"`
}

// OAuthCredentials is a finished login: the client, where to refresh, and the tokens
type OAuthCredentials struct {
	Client        OAuthClient `json:"client"`
	TokenEndpoint string      `json:"tokenEndpoint"`
	Resource      string      `json:"resource,omitempty"`
	AccessToken   string      `json:"accessToken"`
	RefreshToken  string      `json:"refreshToken,omitempty"`
	// ExpiresAt is the access token's expiry in Unix milliseconds, 0 when the server didn't say
	ExpiresAt int64 `json:"expiresAt,omitempty"`
	// LoggedInAt identifies the login, so a refresh never lands on a newer login or brings back one that was logged out
	LoggedInAt int64 `json:"-"`
}

// NeedsRefresh reports whether the access token expires within the refresh skew
func (c OAuthCredentials) NeedsRefresh(now time.Time) bool {
	return c.ExpiresAt != 0 && now.Add(refreshSkew).UnixMilli() >= c.ExpiresAt
}

// LoginUsable reports whether a login still yields access tokens without the user, which is what Codex shows as logged in
// ExpiresAt is the access token's expiry in Unix milliseconds, 0 when unknown
func LoginUsable(expiresAt int64, refreshable bool, now time.Time) bool {
	return expiresAt == 0 || now.Add(refreshSkew).UnixMilli() < expiresAt || refreshable
}

// DiscoverOAuth detects whether an HTTP server advertises an OAuth login, returning nil when it doesn't
func (m *Manager) DiscoverOAuth(ctx context.Context, serverURL string, headers map[string]string) (*OAuthMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthDiscoveryTimeout)
	defer cancel()
	base, err := url.Parse(serverURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("invalid server URL %q", serverURL)
	}
	client := m.discoveryClient(base, headers)

	// Probe the endpoint, since a server that requires a login answers 401 with a pointer to its resource metadata
	pointer, challengeScopes, err := probeChallenge(ctx, client, base)
	if err != nil {
		return nil, err
	}

	// Look for the protected resource metadata where the challenge pointed, then at the well-known locations
	prm, err := findResourceMetadata(ctx, client, base, pointer)
	if err != nil {
		return nil, err
	}
	if prm != nil && len(prm.AuthorizationServers) > 0 {
		var lastErr error
		for _, issuer := range prm.AuthorizationServers {
			asm, err := findAuthServerMetadata(ctx, client, issuer, issuer)
			if err != nil {
				lastErr = err
				continue
			}
			if asm == nil {
				continue
			}
			resource := prm.Resource
			if resource == "" {
				resource = serverURL
			}
			return newOAuthMetadata(asm, issuer, resource, union(challengeScopes, prm.ScopesSupported)), nil
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, errors.New("the protected resource metadata names authorization servers that publish no metadata")
	}

	// Servers from before the 2025-06-18 spec are their own authorization server
	asm, err := findAuthServerMetadata(ctx, client, serverURL, "")
	if err != nil || asm == nil {
		return nil, err
	}
	return newOAuthMetadata(asm, asm.Issuer, "", challengeScopes), nil
}

func newOAuthMetadata(asm *oauthex.AuthServerMeta, issuer, resource string, scopes []string) *OAuthMetadata {
	if asm.Issuer != "" {
		issuer = asm.Issuer
	}
	if len(scopes) == 0 {
		scopes = union(asm.ScopesSupported)
	}
	return &OAuthMetadata{
		Resource:              resource,
		Issuer:                issuer,
		AuthorizationEndpoint: asm.AuthorizationEndpoint,
		TokenEndpoint:         asm.TokenEndpoint,
		RegistrationEndpoint:  asm.RegistrationEndpoint,
		Scopes:                scopes,
		OfflineAccess:         slices.Contains(asm.ScopesSupported, "offline_access"),
		IssParameterSupported: asm.AuthorizationResponseIssParameterSupported,
		TokenAuthMethods:      asm.TokenEndpointAuthMethodsSupported,
	}
}

// discoveryClient sends the server's configured headers to the server's own origin only, never to a third-party authorization server
func (m *Manager) discoveryClient(base *url.URL, headers map[string]string) *http.Client {
	client := m.guard.HTTPClient(oauthDiscoveryTimeout)
	client.Transport = &originHeaderTransport{base: client.Transport, origin: origin(base), headers: headers}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	}
	return client
}

// probeChallenge requests the endpoint without credentials and reads the resource metadata pointer and scopes of a 401 challenge
func probeChallenge(ctx context.Context, client *http.Client, base *url.URL) (*url.URL, []string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to reach the server: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxOAuthResponseBytes))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return nil, nil, nil
	}

	// A malformed challenge only means there is no pointer to follow, the well-known locations still apply
	challenges, _ := oauthex.ParseWWWAuthenticate(resp.Header.Values("WWW-Authenticate"))
	var pointer *url.URL
	var scopes []string
	for _, c := range challenges {
		if raw := c.Params["resource_metadata"]; raw != "" && pointer == nil {
			// Like Codex's client, a pointer to another origin is ignored so a server can't send discovery elsewhere
			if u, err := base.Parse(raw); err == nil && origin(u) == origin(base) {
				pointer = u
			}
		}
		if c.Scheme == "bearer" && c.Params["scope"] != "" && scopes == nil {
			scopes = strings.Fields(c.Params["scope"])
		}
	}
	return pointer, scopes, nil
}

// findResourceMetadata tries the challenge's pointer, then the well-known locations with the path inserted, appended and left out
func findResourceMetadata(ctx context.Context, client *http.Client, base *url.URL, pointer *url.URL) (*oauthex.ProtectedResourceMetadata, error) {
	var candidates []string
	if pointer != nil {
		candidates = append(candidates, pointer.String())
	}
	candidates = append(candidates, wellKnownURLs(base, "oauth-protected-resource")...)

	for _, candidate := range union(candidates) {
		prm, err := getJSON[oauthex.ProtectedResourceMetadata](ctx, client, candidate)
		if err != nil {
			return nil, err
		}
		if prm == nil || !resourceCovers(prm.Resource, base) {
			continue
		}
		for _, issuer := range prm.AuthorizationServers {
			if !isHTTPURL(issuer) {
				return nil, fmt.Errorf("the protected resource metadata names an invalid authorization server %q", issuer)
			}
		}
		return prm, nil
	}
	return nil, nil
}

// wellKnownURLs lists the RFC 8414 locations of a well-known document for a URL with a path, ending with the root
func wellKnownURLs(base *url.URL, name string) []string {
	path := strings.Trim(base.Path, "/")
	at := func(p string) string {
		return (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: p}).String()
	}
	if path == "" {
		return []string{at("/.well-known/" + name)}
	}
	return []string{at("/.well-known/" + name + "/" + path), at("/" + path + "/.well-known/" + name), at("/.well-known/" + name)}
}

// resourceCovers reports whether a protected resource identifier names the server or a parent of it on the same origin (RFC 9728 §3.3)
func resourceCovers(resource string, base *url.URL) bool {
	if resource == "" {
		return true
	}
	u, err := url.Parse(resource)
	if err != nil || origin(u) != origin(base) {
		return false
	}
	prefix := strings.TrimRight(u.Path, "/")
	return prefix == "" || base.Path == prefix || strings.HasPrefix(base.Path, prefix+"/")
}

// findAuthServerMetadata tries the OAuth and OpenID Connect metadata locations of an issuer in the order the MCP spec gives
// An expected issuer, as named by resource metadata, must match what the document says about itself (RFC 8414 §3.3)
func findAuthServerMetadata(ctx context.Context, client *http.Client, issuer, expectedIssuer string) (*oauthex.AuthServerMeta, error) {
	base, err := url.Parse(issuer)
	if err != nil || !isHTTPURL(issuer) {
		return nil, fmt.Errorf("invalid authorization server %q", issuer)
	}
	var candidates []string
	if path := strings.Trim(base.Path, "/"); path == "" {
		candidates = wellKnownURLs(base, "oauth-authorization-server")
		candidates = append(candidates, wellKnownURLs(base, "openid-configuration")...)
	} else {
		oauthURLs, oidcURLs := wellKnownURLs(base, "oauth-authorization-server"), wellKnownURLs(base, "openid-configuration")
		candidates = []string{oauthURLs[0], oidcURLs[0], oidcURLs[1], oauthURLs[2]}
	}

	for _, candidate := range candidates {
		asm, err := getJSON[oauthex.AuthServerMeta](ctx, client, candidate)
		if err != nil {
			return nil, err
		}
		if asm == nil {
			continue
		}
		if expectedIssuer != "" && asm.Issuer != "" && !issuersEqual(asm.Issuer, expectedIssuer) {
			return nil, fmt.Errorf("authorization server metadata at %s is for issuer %q, not %q", candidate, asm.Issuer, expectedIssuer)
		}
		if asm.AuthorizationEndpoint == "" || asm.TokenEndpoint == "" {
			continue
		}
		if !isHTTPURL(asm.AuthorizationEndpoint) || !isHTTPURL(asm.TokenEndpoint) || (asm.RegistrationEndpoint != "" && !isHTTPURL(asm.RegistrationEndpoint)) {
			return nil, fmt.Errorf("authorization server metadata at %s has an invalid endpoint", candidate)
		}
		// Every login uses PKCE with S256, so a server that only offers other methods can't be used
		if len(asm.CodeChallengeMethodsSupported) > 0 && !slices.Contains(asm.CodeChallengeMethodsSupported, "S256") {
			return nil, errors.New("the authorization server doesn't support PKCE with S256")
		}
		return asm, nil
	}
	return nil, nil
}

// getJSON fetches a metadata document, returning nil when it isn't published there
func getJSON[T any](ctx context.Context, client *http.Client, rawURL string) (*T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	// A body that isn't the document, such as an HTML page served for every path, counts as not published
	var out T
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOAuthResponseBytes)).Decode(&out); err != nil {
		return nil, nil
	}
	return &out, nil
}

// BeginOAuthLogin discovers the server's authorization server, registers a client if needed and returns the URL to send the browser to
func (m *Manager) BeginOAuthLogin(ctx context.Context, req OAuthLoginRequest) (string, OAuthLogin, error) {
	meta, err := m.DiscoverOAuth(ctx, req.ServerURL, req.Headers)
	if err != nil {
		return "", OAuthLogin{}, fmt.Errorf("failed to discover the OAuth login: %w", err)
	}
	if meta == nil {
		return "", OAuthLogin{}, ErrOAuthUnsupported
	}

	// Configured scopes win over discovered ones, and offline_access asks for a refresh token so unattended runs keep working
	scopes, discovered := req.Scopes, false
	if req.WithoutScopes {
		scopes = nil
	} else if len(scopes) == 0 {
		scopes, discovered = meta.Scopes, len(meta.Scopes) > 0
	}
	if len(scopes) > 0 && meta.OfflineAccess && !slices.Contains(scopes, "offline_access") {
		scopes = append(slices.Clone(scopes), "offline_access")
	}

	// A configured client is used as is, otherwise one is registered dynamically (RFC 7591)
	client, err := m.resolveClient(ctx, meta, req)
	if err != nil {
		return "", OAuthLogin{}, err
	}

	// PKCE and the state tie the browser's return to this login
	login := OAuthLogin{
		State: rand.Text(), Verifier: oauth2.GenerateVerifier(), RedirectURL: req.RedirectURL, Client: client,
		TokenEndpoint: meta.TokenEndpoint, Issuer: meta.Issuer, IssRequired: meta.IssParameterSupported,
		Resource: meta.Resource, ScopesDiscovered: discovered,
	}
	cfg := oauth2.Config{ClientID: client.ID, RedirectURL: req.RedirectURL, Scopes: scopes, Endpoint: oauth2.Endpoint{AuthURL: meta.AuthorizationEndpoint}}
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(login.Verifier)}
	if meta.Resource != "" {
		opts = append(opts, oauth2.SetAuthURLParam("resource", meta.Resource))
	}
	return cfg.AuthCodeURL(login.State, opts...), login, nil
}

// resolveClient picks the configured client or registers one, and how it authenticates at the token endpoint
func (m *Manager) resolveClient(ctx context.Context, meta *OAuthMetadata, req OAuthLoginRequest) (OAuthClient, error) {
	// A configured client without a secret is a public client, otherwise the secret goes where the server prefers it
	if req.Client.ID != "" {
		client := req.Client
		switch {
		case client.Secret == "":
			client.AuthMethod = "none"
		case slices.Contains(meta.TokenAuthMethods, "client_secret_post"):
			client.AuthMethod = "client_secret_post"
		default:
			client.AuthMethod = "client_secret_basic"
		}
		return client, nil
	}
	if meta.RegistrationEndpoint == "" {
		return OAuthClient{}, errors.New("the authorization server doesn't support dynamic client registration, so the server needs an OAuth client ID")
	}

	// A public client with PKCE is registered where the server allows it, since there is no secret to lose then
	method := ""
	switch {
	case len(meta.TokenAuthMethods) == 0 || slices.Contains(meta.TokenAuthMethods, "none"):
		method = "none"
	case slices.Contains(meta.TokenAuthMethods, "client_secret_post"):
		method = "client_secret_post"
	case slices.Contains(meta.TokenAuthMethods, "client_secret_basic"):
		method = "client_secret_basic"
	}
	reg, err := oauthex.RegisterClient(ctx, meta.RegistrationEndpoint, &oauthex.ClientRegistrationMetadata{
		RedirectURIs:            []string{req.RedirectURL},
		TokenEndpointAuthMethod: method,
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientName:              oauthClientName,
	}, m.guard.HTTPClient(oauthRequestTimeout))
	if err != nil {
		return OAuthClient{}, fmt.Errorf("failed to register a client with the authorization server: %w", err)
	}

	// The server's answer decides how the client authenticates, since it may not grant what was asked for
	client := OAuthClient{ID: reg.ClientID, Secret: reg.ClientSecret, AuthMethod: reg.TokenEndpointAuthMethod}
	if client.AuthMethod == "" {
		client.AuthMethod = method
	}
	if client.AuthMethod == "" || (client.AuthMethod == "none" && client.Secret != "") {
		client.AuthMethod = "client_secret_basic"
	}
	if client.Secret == "" {
		client.AuthMethod = "none"
	}
	return client, nil
}

// FinishOAuthLogin checks the authorization response and exchanges its code for tokens
func (m *Manager) FinishOAuthLogin(ctx context.Context, login OAuthLogin, state, code, iss string) (OAuthCredentials, error) {
	if login.State == "" || subtle.ConstantTimeCompare([]byte(state), []byte(login.State)) != 1 {
		return OAuthCredentials{}, errors.New("the login's state doesn't match, start the login again")
	}

	// The RFC 9207 issuer stops a code from one authorization server being redeemed at another
	if iss == "" && login.IssRequired {
		return OAuthCredentials{}, errors.New("the authorization server didn't identify itself in its response")
	}
	if iss != "" && !issuersEqual(iss, login.Issuer) {
		return OAuthCredentials{}, fmt.Errorf("the response came from issuer %q instead of %q", iss, login.Issuer)
	}

	// Exchange the code with the PKCE verifier
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {login.RedirectURL}, "code_verifier": {login.Verifier}}
	if login.Resource != "" {
		form.Set("resource", login.Resource)
	}
	tok, err := login.Client.requestToken(ctx, m.guard.HTTPClient(oauthRequestTimeout), login.TokenEndpoint, form)
	if err != nil {
		return OAuthCredentials{}, fmt.Errorf("failed to exchange the authorization code: %w", err)
	}

	creds := OAuthCredentials{Client: login.Client, TokenEndpoint: login.TokenEndpoint, Resource: login.Resource}
	return creds.withToken(tok, time.Now()), nil
}

// withToken applies a token response, keeping the refresh token when the server didn't rotate it
func (c OAuthCredentials) withToken(tok *tokenResponse, now time.Time) OAuthCredentials {
	c.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		c.RefreshToken = tok.RefreshToken
	}
	c.ExpiresAt = 0
	if tok.ExpiresIn > 0 {
		c.ExpiresAt = now.Add(time.Duration(tok.ExpiresIn) * time.Second).UnixMilli()
	}
	return c
}

// tokenResponse is a successful token endpoint response (RFC 6749 §5.1)
type tokenResponse struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresIn    expiresIn `json:"expires_in"`
}

// expiresIn accepts a number or a numeric string, since some servers send the lifetime as a string
type expiresIn int64

func (e *expiresIn) UnmarshalJSON(b []byte) error {
	// An unreadable lifetime counts as unknown rather than failing the login
	*e = 0
	if n, err := strconv.ParseInt(strings.Trim(string(b), `"`), 10, 64); err == nil {
		*e = expiresIn(n)
	}
	return nil
}

// TokenError is an error response of the token endpoint (RFC 6749 §5.2)
type TokenError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *TokenError) Error() string {
	if e.Description != "" {
		return e.Code + ": " + e.Description
	}
	return e.Code
}

// requestToken posts a token request, authenticating the client the way it was registered
func (c OAuthClient) requestToken(ctx context.Context, client *http.Client, endpoint string, form url.Values) (*tokenResponse, error) {
	if c.AuthMethod == "client_secret_basic" {
		form.Del("client_id")
	} else {
		form.Set("client_id", c.ID)
		if c.Secret != "" && c.AuthMethod == "client_secret_post" {
			form.Set("client_secret", c.Secret)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if c.AuthMethod == "client_secret_basic" {
		req.SetBasicAuth(url.QueryEscape(c.ID), url.QueryEscape(c.Secret))
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOAuthResponseBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		var tokenErr TokenError
		if json.Unmarshal(body, &tokenErr) == nil && tokenErr.Code != "" {
			return nil, &tokenErr
		}
		return nil, fmt.Errorf("the token endpoint answered %s", resp.Status)
	}

	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("the token endpoint sent an unreadable response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, errors.New("the token endpoint sent no access token")
	}
	if tok.TokenType != "" && !strings.EqualFold(tok.TokenType, "bearer") {
		return nil, fmt.Errorf("the token endpoint sent a %s token, only bearer tokens are supported", tok.TokenType)
	}
	return &tok, nil
}

// OAuthStore persists a login's tokens, so a refresh is shared with later runs and other replicas
type OAuthStore interface {
	// Load returns the stored login, which a run on another replica may have refreshed meanwhile
	Load(ctx context.Context) (OAuthCredentials, error)
	// Lock waits until no other replica refreshes the login and holds theirs off until unlock is called
	Lock(ctx context.Context) (unlock func(), err error)
	// Save stores refreshed tokens on the login they belong to
	Save(ctx context.Context, creds OAuthCredentials) error
	// Expire drops a login whose refresh token the authorization server rejected
	Expire(ctx context.Context, creds OAuthCredentials) error
}

// OAuthTokens hands out a login's access token and refreshes it before it expires
// One instance per server is shared by all sessions of a replica, so concurrent runs never spend a rotating refresh token twice
type OAuthTokens struct {
	mu     sync.Mutex
	creds  OAuthCredentials
	store  OAuthStore
	client *http.Client
}

// NewOAuthTokens wraps a stored login
func (m *Manager) NewOAuthTokens(creds OAuthCredentials, store OAuthStore) *OAuthTokens {
	return &OAuthTokens{creds: creds, store: store, client: m.guard.HTTPClient(oauthRequestTimeout)}
}

// Token returns a current access token, refreshing it shortly before it expires
func (t *OAuthTokens) Token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.creds.NeedsRefresh(time.Now()) {
		return t.creds.AccessToken, nil
	}
	return t.refreshLocked(ctx)
}

// refreshRejected refreshes a token the server rejected before its expiry, unless a concurrent request replaced it already
// It returns an empty token when no fresh one can be had
func (t *OAuthTokens) refreshRejected(ctx context.Context, rejected string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.creds.AccessToken != rejected {
		return t.creds.AccessToken
	}
	fresh, err := t.refreshLocked(ctx)
	if err != nil {
		slog.DebugContext(ctx, "Failed to refresh a rejected MCP OAuth token", slog.Any("error", err))
		return ""
	}
	return fresh
}

func (t *OAuthTokens) refreshLocked(ctx context.Context) (string, error) {
	// Replicas take turns refreshing, since spending a rotated refresh token twice loses the login: the second refresh is rejected and expires it, and servers with reuse detection revoke it
	unlock, err := t.store.Lock(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()

	// A run on another replica may have refreshed already, and a rotated refresh token only works once
	stored, err := t.store.Load(ctx)
	if err != nil {
		return "", err
	}
	if stored.AccessToken != t.creds.AccessToken {
		t.creds = stored
		if !stored.NeedsRefresh(time.Now()) {
			return stored.AccessToken, nil
		}
	}
	if t.creds.RefreshToken == "" {
		return "", ErrLoginExpired
	}

	// Refresh with the same resource the login was for (RFC 8707)
	used := t.creds
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {used.RefreshToken}}
	if used.Resource != "" {
		form.Set("resource", used.Resource)
	}
	tok, err := used.Client.requestToken(ctx, t.client, used.TokenEndpoint, form)

	// A rejected refresh token ends the login, unless another replica rotated it in the meantime
	if tokenErr, ok := errors.AsType[*TokenError](err); ok && tokenErr.Code == "invalid_grant" {
		if latest, loadErr := t.store.Load(ctx); loadErr == nil && latest.RefreshToken != used.RefreshToken && !latest.NeedsRefresh(time.Now()) {
			t.creds = latest
			return latest.AccessToken, nil
		}
		if err := t.store.Expire(ctx, used); err != nil {
			slog.WarnContext(ctx, "Failed to drop an expired MCP OAuth login", slog.Any("error", err))
		}
		return "", ErrLoginExpired
	}
	if err != nil {
		return "", fmt.Errorf("failed to refresh the OAuth token: %w", err)
	}

	// A failed save only costs another refresh later, so the fresh token is used either way
	t.creds = used.withToken(tok, time.Now())
	if err := t.store.Save(ctx, t.creds); err != nil {
		slog.WarnContext(ctx, "Failed to store a refreshed MCP OAuth token", slog.Any("error", err))
	}
	return t.creds.AccessToken, nil
}

// oauthTransport adds the login's access token to every request for the server's origin, and refreshes it once when the server rejects it early
type oauthTransport struct {
	base   http.RoundTripper
	origin string
	tokens *OAuthTokens
}

func (t *oauthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if origin(req.URL) != t.origin {
		return t.base.RoundTrip(req)
	}
	access, err := t.tokens.Token(req.Context())
	if err != nil {
		return nil, err
	}
	resp, err := t.base.RoundTrip(withBearer(req, access))
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}

	// A token rejected before its expiry was revoked or rotated, so one refresh and retry is worth it when the body can be replayed
	// Otherwise the 401 goes back to the caller, which reports the server as needing a login
	retry := replayable(req)
	if retry == nil {
		return resp, nil
	}
	fresh := t.tokens.refreshRejected(req.Context(), access)
	if fresh == "" || fresh == access {
		return resp, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxOAuthResponseBytes))
	_ = resp.Body.Close()
	return t.base.RoundTrip(withBearer(retry, fresh))
}

// replayable returns a copy of the request with a fresh body, or nil when the body can't be sent again
func replayable(req *http.Request) *http.Request {
	retry := req.Clone(req.Context())
	if req.Body == nil || req.Body == http.NoBody {
		return retry
	}
	if req.GetBody == nil {
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil
	}
	retry.Body = body
	return retry
}

func withBearer(req *http.Request, token string) *http.Request {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

// originHeaderTransport adds headers to requests for one origin only
type originHeaderTransport struct {
	base    http.RoundTripper
	origin  string
	headers map[string]string
}

func (h *originHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(h.headers) == 0 || origin(req.URL) != h.origin {
		return h.base.RoundTrip(req)
	}
	req = req.Clone(req.Context())
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	return h.base.RoundTrip(req)
}

// unauthorizedRecorder notes whether the server answered 401, so a failed connection can say it needs credentials
type unauthorizedRecorder struct {
	base http.RoundTripper
	mu   sync.Mutex
	seen bool
}

func (u *unauthorizedRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := u.base.RoundTrip(req)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		u.mu.Lock()
		u.seen = true
		u.mu.Unlock()
	}
	return resp, err
}

func (u *unauthorizedRecorder) sawUnauthorized() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.seen
}

// HasAuthorizationHeader reports whether configured headers already authenticate, which is what Codex shows as a bearer token
func HasAuthorizationHeader(headers map[string]string) bool {
	for k := range headers {
		if strings.EqualFold(k, "Authorization") {
			return true
		}
	}
	return false
}

func origin(u *url.URL) string {
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func issuersEqual(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// union joins scope lists, dropping blanks and duplicates while keeping their order
// The lists come from a server's metadata, so duplicates are tracked in a set to keep a huge document from costing quadratic time
func union(lists ...[]string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, list := range lists {
		for _, s := range list {
			s = strings.TrimSpace(s)
			if _, dup := seen[s]; s != "" && !dup {
				seen[s] = struct{}{}
				out = append(out, s)
			}
		}
	}
	return out
}
