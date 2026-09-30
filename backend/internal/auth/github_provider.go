package auth

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/auth/authdb"
	"github.com/stonith404/umpteenth/backend/internal/database"
)

const (
	githubURL    = "https://github.com"
	githubAPIURL = "https://api.github.com"
)

// githubProvider signs in with GitHub accounts through a GitHub OAuth app
// GitHub lets any account authorize an app, so only the configured users and members of the configured organizations get in
type githubProvider struct {
	cfg     ProviderConfig
	queries *authdb.Queries

	// webURL and apiURL point at GitHub, and tests swap them for a fake
	webURL string
	apiURL string
}

func newGitHubProvider(cfg ProviderConfig, queries *authdb.Queries) *githubProvider {
	return &githubProvider{cfg: cfg, queries: queries, webURL: githubURL, apiURL: githubAPIURL}
}

func (p *githubProvider) oauthConfig(redirectURL string) *oauth2.Config {
	// The primary email address can be private, and organization memberships are only visible with read:org
	scopes := []string{"user:email"}
	if len(p.cfg.AllowedOrganizations) > 0 || len(p.cfg.AdminOrganizations) > 0 {
		scopes = append(scopes, "read:org")
	}
	return &oauth2.Config{
		ClientID:     p.cfg.ClientID,
		ClientSecret: p.cfg.ClientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     oauth2.Endpoint{AuthURL: p.webURL + "/login/oauth/authorize", TokenURL: p.webURL + "/login/oauth/access_token"},
		Scopes:       scopes,
	}
}

// issuer is GitHub's web URL, since GitHub accounts have no OpenID issuer
func (p *githubProvider) issuer() string {
	return p.webURL
}

func (p *githubProvider) authCodeURL(_ context.Context, redirectURL string, state loginState) (string, error) {
	return p.oauthConfig(redirectURL).AuthCodeURL(state.State, oauth2.S256ChallengeOption(state.Verifier)), nil
}

func (p *githubProvider) identify(ctx context.Context, redirectURL, code string, state loginState) (identity, error) {
	// Exchange the code with the PKCE verifier for a token that acts as the user
	oauthCfg := p.oauthConfig(redirectURL)
	token, err := oauthCfg.Exchange(ctx, code, oauth2.VerifierOption(state.Verifier))
	if err != nil {
		return identity{}, apperror.LoginFailed(fmt.Errorf("code exchange failed: %w", err))
	}
	client := oauthCfg.Client(ctx, token)

	var user struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}
	found, err := p.get(ctx, client, "/user", &user)
	if err != nil {
		return identity{}, err
	}
	if !found || user.ID == 0 {
		return identity{}, apperror.LoginFailed(errors.New("GitHub returned no user for the token"))
	}

	allowed, admin, err := p.access(ctx, client, user.ID, user.Login)
	if err != nil {
		return identity{}, err
	}
	if !allowed {
		return identity{}, apperror.Forbidden("Your GitHub account is not allowed to sign in")
	}

	// The profile only shows a public email address, so a private one comes from the account's address list
	email := user.Email
	if email == "" {
		email, err = p.primaryEmail(ctx, client)
		if err != nil {
			return identity{}, err
		}
	}

	// The numeric ID stays the same when the account is renamed, unlike the username
	// GitHub only shows verified addresses on a profile, and the address list fallback only takes a verified one
	return identity{
		Issuer:        p.webURL,
		Subject:       strconv.FormatInt(user.ID, 10),
		Email:         email,
		EmailVerified: email != "",
		Name:          cmp.Or(user.Name, user.Login),
		Picture:       user.AvatarURL,
		Admin:         admin,
	}, nil
}

// access decides whether the account may sign in and whether it is an instance admin, whom it always lets in
// Usernames are checked before organizations, since they need no request
func (p *githubProvider) access(ctx context.Context, client *http.Client, accountID int64, login string) (allowed, admin bool, err error) {
	isUser := func(users []string) bool {
		return slices.ContainsFunc(users, func(u string) bool { return strings.EqualFold(u, login) })
	}

	// A listed username only counts for the account that first signed in with it
	listed := false
	if isUser(p.cfg.AdminUsers) || isUser(p.cfg.AllowedUsers) {
		listed, err = p.claim(ctx, "users", login, accountID)
		if err != nil {
			return false, false, err
		}
	}

	admin = listed && isUser(p.cfg.AdminUsers)
	if !admin {
		admin, err = p.memberOfAny(ctx, client, p.cfg.AdminOrganizations)
		if err != nil {
			return false, false, err
		}
	}
	if admin || listed {
		return true, admin, nil
	}
	allowed, err = p.memberOfAny(ctx, client, p.cfg.AllowedOrganizations)
	return allowed, false, err
}

// memberOfAny reports whether the signed-in user is an active member of one of the organizations
func (p *githubProvider) memberOfAny(ctx context.Context, client *http.Client, orgs []string) (bool, error) {
	for _, org := range orgs {
		// An invitation that wasn't accepted yet leaves the membership pending
		var membership struct {
			State        string `json:"state"`
			Organization struct {
				ID int64 `json:"id"`
			} `json:"organization"`
		}
		found, err := p.get(ctx, client, "/user/memberships/orgs/"+url.PathEscape(org), &membership)
		if err != nil {
			return false, err
		}

		// Only an active membership counts, and only with the organization's ID, which the listed name is tied to
		if !found || membership.State != "active" || membership.Organization.ID == 0 {
			continue
		}

		// A listed organization name only counts for the organization that held it when one of its members first signed in
		claimed, err := p.claim(ctx, "orgs", org, membership.Organization.ID)
		if err != nil {
			return false, err
		}
		if claimed {
			return true, nil
		}
	}
	return false, nil
}

// pin ties every listed name nothing claimed yet to the account or organization holding it now, which every replica does once on start
// Otherwise a name is only tied at its first sign-in, and one given up before that would go to whoever registers it next
// A lookup that fails, such as over GitHub's rate limit, leaves the name to its first sign-in
func (p *githubProvider) pin(ctx context.Context) {
	lists := []struct {
		kind, accountType string
		names             []string
	}{
		{"users", "User", slices.Concat(p.cfg.AllowedUsers, p.cfg.AdminUsers)},
		{"orgs", "Organization", slices.Concat(p.cfg.AllowedOrganizations, p.cfg.AdminOrganizations)},
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for _, list := range lists {
		for _, name := range list.names {
			err := p.pinName(ctx, client, list.kind, list.accountType, name)
			if err != nil {
				slog.WarnContext(ctx, "Failed to look up a listed GitHub name, so its first sign-in ties it to its holder", slog.String("name", name), slog.Any("error", err))
			}
		}
	}
}

// pinName ties the listed name to the numeric ID of its current holder unless it is tied already
func (p *githubProvider) pinName(ctx context.Context, client *http.Client, kind, accountType, name string) error {
	_, err := p.queries.GetGitHubClaim(ctx, p.claimKey(kind, name))
	if err == nil {
		return nil
	} else if !database.IsNotFound(err) {
		return err
	}

	// The public users endpoint answers for organizations as well and tells the two apart by type
	var holder struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	}
	found, err := p.get(ctx, client, "/users/"+url.PathEscape(name), &holder)
	if err != nil || !found || holder.ID == 0 || holder.Type != accountType {
		return err
	}
	_, err = p.claim(ctx, kind, name, holder.ID)
	return err
}

// claimKey is the kv key that ties a listed name to the numeric ID holding it
func (p *githubProvider) claimKey(kind, name string) string {
	return "github-name/" + p.webURL + "/" + kind + "/" + strings.ToLower(name)
}

// claim ties a listed name to the numeric ID of the account or organization that holds it the first time it lets someone in, unless pin did at the start, and reports whether id is that one
// GitHub gives a renamed account's or organization's old name to whoever registers it next, and the list still means the one that held it before
func (p *githubProvider) claim(ctx context.Context, kind, name string, id int64) (bool, error) {
	key := p.claimKey(kind, name)
	value := strconv.FormatInt(id, 10)
	owner, err := p.queries.ClaimGitHubName(ctx, authdb.ClaimGitHubNameParams{Key: key, Value: value})
	if err != nil {
		return false, fmt.Errorf("failed to claim GitHub name %q: %w", name, err)
	}
	if owner != value {
		slog.WarnContext(ctx, "Refused a listed GitHub name that another account or organization claimed first, and deleting its kv row lets the new holder claim it", slog.String("key", key), slog.String("claimed", owner), slog.Int64("refused", id))
		return false, nil
	}
	return true, nil
}

// primaryEmail returns the account's verified primary email address, or nothing when it has none
func (p *githubProvider) primaryEmail(ctx context.Context, client *http.Client) (string, error) {
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	_, err := p.get(ctx, client, "/user/emails", &emails)
	if err != nil {
		return "", err
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	return "", nil
}

// get calls the GitHub API as the signed-in user and decodes a successful response into v
// A 403 or 404 only means the user can't see the resource, e.g. an organization they aren't a member of, so it reports not found instead of an error
func (p *githubProvider) get(ctx context.Context, client *http.Client, path string, v any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiURL+path, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	res, err := client.Do(req)
	if err != nil {
		return false, apperror.Unavailable(err, "GitHub is unreachable")
	}
	defer func() { _ = res.Body.Close() }()

	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusNotFound:
		return false, nil
	default:
		return false, apperror.Unavailable(fmt.Errorf("GitHub answered %s with %s", path, res.Status), "GitHub is unavailable")
	}

	err = json.NewDecoder(res.Body).Decode(v)
	if err != nil {
		return false, apperror.LoginFailed(fmt.Errorf("invalid GitHub response to %s: %w", path, err))
	}
	return true, nil
}
