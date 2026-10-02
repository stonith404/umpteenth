package config

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Validate normalizes derived values and rejects inconsistent combinations
func (c *Config) Validate() error {
	// Case and a trailing slash carry no meaning in these values
	c.App.URL = strings.TrimRight(c.App.URL, "/")
	c.App.Env = AppEnv(strings.ToLower(string(c.App.Env)))
	c.Log.Level = strings.ToLower(c.Log.Level)
	c.FileStorage.Backend = strings.ToLower(c.FileStorage.Backend)
	c.Sandbox.Adapter = strings.ToLower(c.Sandbox.Adapter)

	// The URL ends up in sign-in redirects, webhook URLs and notification links, so it must be absolute
	if !isAbsoluteHTTPURL(c.App.URL) {
		return fmt.Errorf("%s must be an absolute http:// or https:// URL, got %q", describe("app.url"), c.App.URL)
	}
	switch c.App.Env {
	case AppEnvProduction, AppEnvDevelopment, AppEnvTest:
	default:
		return fmt.Errorf("unknown %s %q, use production, development or test", describe("app.env"), c.App.Env)
	}

	var level slog.Level
	if level.UnmarshalText([]byte(c.Log.Level)) != nil {
		return fmt.Errorf("unknown %s %q, use debug, info, warn or error", describe("log.level"), c.Log.Level)
	}

	for _, port := range []struct {
		key   string
		value int
	}{{"server.port", c.Server.Port}, {"server.broker_port", c.Server.BrokerPort}, {"ha.actors.port", c.HA.Actors.Port}} {
		if port.value < 1 || port.value > 65535 {
			return fmt.Errorf("%s must be a port between 1 and 65535", describe(port.key))
		}
	}

	// The database and the stored files live in the data directory unless configured elsewhere
	if c.Database.ConnectionString == "" {
		c.Database.ConnectionString = filepath.Join(c.App.DataDir, "umpteenth.db")
	}
	if c.FileStorage.Path == "" {
		c.FileStorage.Path = filepath.Join(c.App.DataDir, "blobs")
	}

	switch c.FileStorage.Backend {
	case FileBackendFilesystem, FileBackendS3, FileBackendDatabase:
	default:
		return fmt.Errorf("unknown %s %q, use filesystem, s3 or database", describe("file_storage.backend"), c.FileStorage.Backend)
	}

	switch c.Sandbox.Adapter {
	case SandboxAdapterDocker, SandboxAdapterNone:
	case SandboxAdapterKubernetes:
		// Sandbox pods reach the broker of the replica executing their run by its pod address, which only the deployment knows
		if c.Sandbox.BrokerHost == "" {
			return fmt.Errorf("%s is required with the kubernetes adapter, set it to the pod IP, e.g. from the downward API", describe("sandbox.broker_host"))
		}
		if _, err := c.Sandbox.Kubernetes.ClusterPrefixes(); err != nil {
			return fmt.Errorf("%s: %w", describe("sandbox.kubernetes.cluster_ranges"), err)
		}
		for _, entry := range c.Sandbox.Kubernetes.NodeSelector {
			if key, _, ok := strings.Cut(entry, "="); !ok || key == "" {
				return fmt.Errorf("%s entry %q must be key=value", describe("sandbox.kubernetes.node_selector"), entry)
			}
		}
	default:
		return fmt.Errorf("unknown %s %q, use docker, kubernetes or none", describe("sandbox.adapter"), c.Sandbox.Adapter)
	}

	if _, err := c.Network.BlockedRanges(); err != nil {
		return fmt.Errorf("%s: %w", describe("network.blocked_targets"), err)
	}

	// Every instance needs its own key because the same key encrypts stored credentials and signs sessions
	if c.App.EncryptionKey == "" {
		return fmt.Errorf("%s is required", describe("app.encryption_key"))
	}
	if len(c.App.EncryptionKey) < 16 {
		return fmt.Errorf("%s must be at least 16 bytes", describe("app.encryption_key"))
	}

	err := c.Auth.validate()
	if err != nil {
		return err
	}

	// MCP clients sign in through one of the sign-in providers, and only OpenID Connect providers issue access tokens Umpteenth can verify
	if id := c.MCP.OAuthProvider; id != "" {
		p, ok := c.Auth.Providers[id]
		if !ok {
			return fmt.Errorf("%s names %q, which isn't one of the auth.providers", describe("mcp.oauth_provider"), id)
		}
		if strings.ToLower(p.Type) != AuthProviderOIDC {
			return fmt.Errorf("%s names %q, which has to be an oidc provider", describe("mcp.oauth_provider"), id)
		}
	}

	if c.Runs.MaxConcurrent < 1 {
		c.Runs.MaxConcurrent = 1
	}

	// The nightly prune empties every run that finished before now minus the retention, so 0 or less would wipe every finished run instead of keeping them forever
	if c.Runs.RetentionDays < 1 {
		return errors.New(describe("runs.retention_days") + " must be at least 1 day")
	}

	// A refresh more often than every few minutes would only hammer models.dev and the providers' servers
	interval := c.Models.CatalogRefreshInterval
	if interval < 0 || (interval > 0 && interval < 5*time.Minute) {
		return errors.New(describe("models.catalog_refresh_interval") + " must be 0 to turn it off or at least 5m")
	}

	return nil
}

// authTypeOptions are the options that only apply to one provider type, so setting one for another type fails instead of being ignored
var authTypeOptions = map[string][]string{
	AuthProviderOIDC:   {"issuer", "allowed_groups", "admin_groups"},
	AuthProviderGitHub: {"allowed_users", "allowed_organizations", "admin_users", "admin_organizations"},
}

// validate checks that every sign-in provider has what its type needs and that the login page has at most one primary button
func (a Auth) validate() error {
	var primary string
	for _, id := range slices.Sorted(maps.Keys(a.Providers)) {
		p := a.Providers[id]
		key := "auth.providers." + id

		// Sessions and the login page tell passkey accounts apart by this ID
		if id == PasskeyProviderID {
			return fmt.Errorf("%s is reserved for passkeys, give the provider another ID", key)
		}
		p.Type = strings.ToLower(p.Type)

		switch p.Type {
		case AuthProviderOIDC, AuthProviderGitHub:
		case "":
			return fmt.Errorf("%s is required, use oidc or github", describe(key+".type"))
		default:
			return fmt.Errorf("unknown %s %q, use oidc or github", describe(key+".type"), p.Type)
		}

		// Every type needs a label for its button and a client to sign in with
		for _, required := range []struct{ option, value string }{{"name", p.Name}, {"client_id", p.ClientID}} {
			if required.value == "" {
				return fmt.Errorf("%s is required", describe(key+"."+required.option))
			}
		}

		// The icon becomes an image source on the login page, which only needs web URLs and inline images
		if p.Icon != "" && !isAbsoluteHTTPURL(p.Icon) && !strings.HasPrefix(p.Icon, "data:image/") {
			return fmt.Errorf("%s must be an http:// or https:// URL or a data:image URI", describe(key+".icon"))
		}

		set := map[string]bool{
			"issuer":                p.Issuer != "",
			"allowed_groups":        len(p.AllowedGroups) > 0,
			"admin_groups":          len(p.AdminGroups) > 0,
			"allowed_users":         len(p.AllowedUsers) > 0,
			"allowed_organizations": len(p.AllowedOrganizations) > 0,
			"admin_users":           len(p.AdminUsers) > 0,
			"admin_organizations":   len(p.AdminOrganizations) > 0,
		}
		for _, other := range slices.Sorted(maps.Keys(authTypeOptions)) {
			for _, option := range authTypeOptions[other] {
				if other != p.Type && set[option] {
					return fmt.Errorf("%s only applies to %s providers", describe(key+"."+option), other)
				}
			}
		}

		switch p.Type {
		case AuthProviderOIDC:
			if !isAbsoluteHTTPURL(p.Issuer) {
				return fmt.Errorf("%s must be an absolute http:// or https:// URL, got %q", describe(key+".issuer"), p.Issuer)
			}
		case AuthProviderGitHub:
			// GitHub only redeems authorization codes together with the client secret
			if p.ClientSecret == "" {
				return fmt.Errorf("%s is required", describe(key+".client_secret"))
			}
			// Anyone with a GitHub account can authorize an OAuth app, so the provider has to name who may sign in
			// Admins may sign in too, so an admin list alone also names who may
			if !set["allowed_users"] && !set["allowed_organizations"] && !set["admin_users"] && !set["admin_organizations"] {
				return fmt.Errorf("%s or %s has to list who may sign in, since anyone with a GitHub account could otherwise", describe(key+".allowed_users"), describe(key+".allowed_organizations"))
			}
		}

		if p.Primary {
			if primary != "" {
				return fmt.Errorf("only one sign-in provider can be primary, but %s and %s both are", describe("auth.providers."+primary+".primary"), describe(key+".primary"))
			}
			primary = id
		}
	}
	return nil
}

func isAbsoluteHTTPURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
