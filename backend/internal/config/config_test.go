//go:build unit

package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile creates a file in a temporary directory and returns its path
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

const testEncryptionKey = "unit-test-encryption-key"

// devEnv is the smallest environment that passes validation
var devEnv = map[string]string{"APP_ENV": "development", "APP_ENCRYPTION_KEY": testEncryptionKey}

func TestEveryOptionHasAUniqueSupportedName(t *testing.T) {
	s := newSchema(&Config{})
	seen := map[string]string{}
	for _, o := range s.options {
		name := EnvName(o.key)
		other, dup := seen[name]
		require.False(t, dup, "%s and %s both map to %s", o.key, other, name)
		seen[name] = o.key

		// An unsupported type fails on any value, while a supported one at most rejects the empty string
		err := o.set("")
		if err != nil {
			assert.NotContains(t, err.Error(), "unsupported", o.key)
		}
	}

	// The options of collection sections have to be supported too
	for _, c := range s.collections {
		for _, o := range c.template().options {
			err := o.set("")
			if err != nil {
				assert.NotContains(t, err.Error(), "unsupported", c.key+"."+o.key)
			}
		}
	}
	// Default panics on a default tag its option can't parse
	assert.NotPanics(t, func() { Default() })
}

func TestEnvNameFollowsTheYAMLPath(t *testing.T) {
	assert.Equal(t, "SERVER_PORT", EnvName("server.port"))
	assert.Equal(t, "FILE_STORAGE_S3_SECRET_ACCESS_KEY", EnvName("file_storage.s3.secret_access_key"))
	assert.Equal(t, "HA_ACTORS_BIND_ADDRESS", EnvName("ha.actors.bind_address"))
	assert.Equal(t, "AUTH_PROVIDERS_POCKET_ID_CLIENT_ID", EnvName("auth.providers.pocket-id.client_id"))
}

func TestDefaultAppliesTheSchemaDefaults(t *testing.T) {
	cfg := Default()
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, AppEnvProduction, cfg.App.Env)
	assert.True(t, cfg.FileStorage.S3.UseSSL)
	assert.True(t, cfg.Network.AllowPrivateTargets)
	assert.Equal(t, 12*time.Hour, cfg.Models.CatalogRefreshInterval)
	assert.Empty(t, cfg.Auth.Providers)
	assert.True(t, cfg.Auth.Passkeys.Enabled)
}

func TestLoadLayersTheFileOverDefaultsAndTheEnvironmentOverTheFile(t *testing.T) {
	path := writeFile(t, "config.yml", `
app:
  url: https://umpteenth.example.com/
  env: development
server:
  port: 9000
  broker_port: 9001
log:
  level: DEBUG
sandbox:
  docker:
    dns: 1.1.1.1, 9.9.9.9
models:
  catalog_refresh_interval: 0
`)

	cfg, err := load(path, map[string]string{
		"APP_ENCRYPTION_KEY": testEncryptionKey,
		"SERVER_PORT":        "9100",
		"LOG_JSON":           "true",
		// An empty variable is what Compose passes for an unset one, so it must not clear the file's value
		"SERVER_BROKER_PORT": "",
	})
	require.NoError(t, err)

	assert.Equal(t, "https://umpteenth.example.com", cfg.App.URL)
	assert.Equal(t, 9100, cfg.Server.Port)
	assert.Equal(t, 9001, cfg.Server.BrokerPort)
	assert.Equal(t, "debug", cfg.Log.Level)
	assert.True(t, cfg.Log.JSON)
	assert.Equal(t, []string{"1.1.1.1", "9.9.9.9"}, cfg.Sandbox.Docker.DNS)
	assert.Equal(t, time.Duration(0), cfg.Models.CatalogRefreshInterval)
	// Options the file leaves out keep their defaults
	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, 3, cfg.Runs.MaxConcurrent)
}

func TestLoadReadsFileVariantsOfEveryOption(t *testing.T) {
	keyFile := writeFile(t, "key", "a-key-that-is-long-enough\n")

	cfg, err := load("", map[string]string{"APP_ENCRYPTION_KEY_FILE": keyFile})
	require.NoError(t, err)
	assert.Equal(t, "a-key-that-is-long-enough", cfg.App.EncryptionKey)

	_, err = load("", map[string]string{"APP_ENCRYPTION_KEY_FILE": keyFile, "APP_ENCRYPTION_KEY": "another-long-enough-key"})
	require.ErrorContains(t, err, "set either APP_ENCRYPTION_KEY or APP_ENCRYPTION_KEY_FILE")
}

func TestLoadRejectsMistakesInTheFile(t *testing.T) {
	cases := map[string]struct{ yaml, err string }{
		"unknown option":         {"server:\n  prot: 1\n", "line 2: unknown option server.prot"},
		"unknown section":        {"sever:\n  port: 1\n", "line 1: unknown option sever"},
		"value for a section":    {"server: 8080\n", "server must contain options"},
		"list for a single":      {"server:\n  port: [1, 2]\n", "server.port: expects a single value"},
		"mapping for a value":    {"app:\n  url:\n    host: x\n", "app.url: expects a value"},
		"invalid number":         {"server:\n  port: eighty\n", `line 2: server.port: invalid whole number "eighty"`},
		"duration without unit":  {"models:\n  catalog_refresh_interval: 12\n", "invalid duration"},
		"invalid after defaults": {"file_storage:\n  backend: nfs\n", "unknown file_storage.backend (FILE_STORAGE_BACKEND)"},
		"relative app url":       {"app:\n  url: localhost:8080\n", "app.url (APP_URL) must be an absolute http:// or https:// URL"},
		"unknown sandbox":        {"sandbox:\n  adapter: firecracker\n", "unknown sandbox.adapter (SANDBOX_ADAPTER)"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := load(writeFile(t, "config.yml", c.yaml), devEnv)
			require.ErrorContains(t, err, c.err)
		})
	}
}

func TestLoadNamesTheEnvironmentVariableOfAnInvalidValue(t *testing.T) {
	_, err := load("", map[string]string{"APP_ENV": "development", "APP_ENCRYPTION_KEY": testEncryptionKey, "RUNS_MAX_CONCURRENT": "many"})
	require.ErrorContains(t, err, `RUNS_MAX_CONCURRENT: invalid whole number "many"`)
}

func TestLoadFindsTheDefaultFileOnlyWhenPresent(t *testing.T) {
	t.Chdir(t.TempDir())

	// Without a file the defaults and the environment are enough
	cfg, err := load("", devEnv)
	require.NoError(t, err)
	assert.Equal(t, 8080, cfg.Server.Port)

	require.NoError(t, os.WriteFile("config.yaml", []byte("server:\n  port: 9000\n"), 0o600))
	cfg, err = load("", devEnv)
	require.NoError(t, err)
	assert.Equal(t, 9000, cfg.Server.Port)

	// A file that was asked for by name has to exist
	_, err = load("missing.yml", devEnv)
	require.ErrorContains(t, err, "failed to read the config file")
}

func TestLoadAcceptsAnEmptyFileAndSections(t *testing.T) {
	cfg, err := load(writeFile(t, "config.yml", "# nothing yet\nauth:\n  providers:\n"), devEnv)
	require.NoError(t, err)
	assert.Empty(t, cfg.Auth.Providers)
}

func TestValidateDerivesPathsFromTheDataDirectory(t *testing.T) {
	cfg, err := load("", map[string]string{"APP_ENV": "development", "APP_ENCRYPTION_KEY": testEncryptionKey, "APP_DATA_DIR": "/srv/umpteenth"})
	require.NoError(t, err)
	assert.Equal(t, "/srv/umpteenth/umpteenth.db", cfg.Database.ConnectionString)
	assert.Equal(t, DbProviderSqlite, cfg.Database.Provider())
	assert.Equal(t, "/srv/umpteenth/blobs", cfg.FileStorage.Path)
}

func TestValidateRequiresEncryptionKeyInEveryEnvironment(t *testing.T) {
	for _, appEnv := range []string{"production", "development", "test"} {
		t.Run(appEnv, func(t *testing.T) {
			_, err := load("", map[string]string{"APP_ENV": appEnv})
			require.ErrorContains(t, err, "app.encryption_key (APP_ENCRYPTION_KEY) is required")
		})
	}
}

func TestValidateRejectsInconsistentCombinations(t *testing.T) {
	_, err := load("", map[string]string{"APP_ENV": "development", "APP_ENCRYPTION_KEY": testEncryptionKey, "MODELS_CATALOG_REFRESH_INTERVAL": "1m"})
	require.ErrorContains(t, err, "must be 0 to turn it off or at least 5m")

	_, err = load("", map[string]string{"APP_ENV": "development", "APP_ENCRYPTION_KEY": testEncryptionKey, "HA_ACTORS_PORT": "70000"})
	require.ErrorContains(t, err, "ha.actors.port (HA_ACTORS_PORT) must be a port")

	_, err = load("", map[string]string{"APP_ENV": "development", "APP_ENCRYPTION_KEY": testEncryptionKey, "NETWORK_BLOCKED_TARGETS": "203.0.113.7, example.com"})
	require.ErrorContains(t, err, `network.blocked_targets (NETWORK_BLOCKED_TARGETS): "example.com" is neither an IP address nor a CIDR range`)
}

func TestBlockedRangesAcceptAddressesAndRanges(t *testing.T) {
	ranges, err := Network{BlockedTargets: []string{"203.0.113.7", " 198.51.100.9/24", "2001:db8::1", "::ffff:192.0.2.1"}}.BlockedRanges()
	require.NoError(t, err)
	assert.Equal(t, []netip.Prefix{
		netip.MustParsePrefix("203.0.113.7/32"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("2001:db8::1/128"),
		netip.MustParsePrefix("192.0.2.1/32"),
	}, ranges)
}

func TestDatabaseProviderRecognizesPostgresURLs(t *testing.T) {
	assert.Equal(t, DbProviderPostgres, Database{ConnectionString: "postgresql://db/umpteenth"}.Provider())
}

func TestExampleFileShowsValidOptionsWithTheirDefaults(t *testing.T) {
	const example = "../../../config.example.yml"

	// The example only holds known options, and the values it shows are the defaults
	cfg := Default()
	require.NoError(t, newSchema(cfg).applyFile(example))
	assert.Equal(t, Default(), cfg)
}

func TestLoadReadsSignInProvidersFromTheFileAndTheEnvironment(t *testing.T) {
	secretFile := writeFile(t, "secret", "file-secret\n")
	path := writeFile(t, "config.yml", `
auth:
  providers:
    pocket-id:
      type: oidc
      name: Pocket ID
      icon: https://id.example.com/logo.png
      issuer: https://id.example.com
      client_id: umpteenth
      allowed_groups: [admins]
      primary: true
    github:
      type: github
      name: GitHub
      client_id: from-the-file
      client_secret: secret
      allowed_users: [octocat]
`)

	vars := map[string]string{
		"APP_ENV":            "development",
		"APP_ENCRYPTION_KEY": testEncryptionKey,
		// Hyphens of an ID become underscores, and an option can still be read from a file
		"AUTH_PROVIDERS_POCKET_ID_CLIENT_SECRET_FILE": secretFile,
		// The environment wins over the file, as it does for every other option
		"AUTH_PROVIDERS_GITHUB_CLIENT_ID": "from-the-env",
		// A provider can live in the environment alone, where a list is comma-separated
		"AUTH_PROVIDERS_CORP_SSO_TYPE":           "OIDC",
		"AUTH_PROVIDERS_CORP_SSO_NAME":           "Corp",
		"AUTH_PROVIDERS_CORP_SSO_ISSUER":         "https://sso.corp.example",
		"AUTH_PROVIDERS_CORP_SSO_CLIENT_ID":      "umpteenth",
		"AUTH_PROVIDERS_CORP_SSO_ALLOWED_GROUPS": "ops, dev",
		// An empty variable is unset, so it neither creates a provider nor clears an option
		"AUTH_PROVIDERS_EMPTY_ISSUER": "",
		"AUTH_PROVIDERS_GITHUB_ICON":  "",
	}
	cfg, err := load(path, vars)
	require.NoError(t, err)

	assert.Equal(t, map[string]*AuthProvider{
		"pocket-id": {Type: "oidc", Name: "Pocket ID", Icon: "https://id.example.com/logo.png", Primary: true, ClientID: "umpteenth", ClientSecret: "file-secret", Issuer: "https://id.example.com", AllowedGroups: []string{"admins"}},
		"github":    {Type: "github", Name: "GitHub", ClientID: "from-the-env", ClientSecret: "secret", AllowedUsers: []string{"octocat"}},
		"corp-sso":  {Type: "oidc", Name: "Corp", ClientID: "umpteenth", Issuer: "https://sso.corp.example", AllowedGroups: []string{"ops", "dev"}},
	}, cfg.Auth.Providers)
}

func TestLoadRejectsMistakesInSignInProviders(t *testing.T) {
	oidc := "      type: oidc\n      name: Pocket ID\n      issuer: https://id.example.com\n      client_id: umpteenth\n"
	github := "      type: github\n      name: GitHub\n      client_id: umpteenth\n      client_secret: secret\n"
	provider := func(id, options string) string { return "auth:\n  providers:\n    " + id + ":\n" + options }
	cases := map[string]struct{ yaml, err string }{
		"invalid id":              {provider("Pocket_ID", oidc), `line 3: auth.providers: invalid ID "Pocket_ID"`},
		"unknown option":          {provider("pocket-id", "      isuer: x\n"), "line 4: unknown option auth.providers.pocket-id.isuer"},
		"value for ids":           {"auth:\n  providers: pocket-id\n", "auth.providers must map IDs to options"},
		"empty provider":          {provider("pocket-id", ""), "auth.providers.pocket-id.type (AUTH_PROVIDERS_POCKET_ID_TYPE) is required, use oidc or github"},
		"unknown type":            {provider("pocket-id", "      type: saml\n"), `unknown auth.providers.pocket-id.type (AUTH_PROVIDERS_POCKET_ID_TYPE) "saml", use oidc or github`},
		"missing name":            {provider("pocket-id", "      type: oidc\n"), "auth.providers.pocket-id.name (AUTH_PROVIDERS_POCKET_ID_NAME) is required"},
		"missing client id":       {provider("pocket-id", "      type: oidc\n      name: Pocket ID\n"), "auth.providers.pocket-id.client_id (AUTH_PROVIDERS_POCKET_ID_CLIENT_ID) is required"},
		"missing issuer":          {provider("pocket-id", "      type: oidc\n      name: Pocket ID\n      client_id: umpteenth\n"), "auth.providers.pocket-id.issuer (AUTH_PROVIDERS_POCKET_ID_ISSUER) must be an absolute"},
		"relative issuer":         {provider("pocket-id", "      type: oidc\n      name: Pocket ID\n      client_id: umpteenth\n      issuer: id.example.com\n"), "auth.providers.pocket-id.issuer (AUTH_PROVIDERS_POCKET_ID_ISSUER) must be an absolute"},
		"script icon":             {provider("pocket-id", oidc+"      icon: javascript:alert(1)\n"), "auth.providers.pocket-id.icon (AUTH_PROVIDERS_POCKET_ID_ICON) must be an http:// or https:// URL or a data:image URI"},
		"github option on oidc":   {provider("pocket-id", oidc+"      allowed_users: [octocat]\n"), "auth.providers.pocket-id.allowed_users (AUTH_PROVIDERS_POCKET_ID_ALLOWED_USERS) only applies to github providers"},
		"oidc option on github":   {provider("github", github+"      allowed_users: [octocat]\n      allowed_groups: [admins]\n"), "auth.providers.github.allowed_groups (AUTH_PROVIDERS_GITHUB_ALLOWED_GROUPS) only applies to oidc providers"},
		"admin option on oidc":    {provider("pocket-id", oidc+"      admin_users: [octocat]\n"), "auth.providers.pocket-id.admin_users (AUTH_PROVIDERS_POCKET_ID_ADMIN_USERS) only applies to github providers"},
		"github without secret":   {provider("github", "      type: github\n      name: GitHub\n      client_id: umpteenth\n      allowed_users: [octocat]\n"), "auth.providers.github.client_secret (AUTH_PROVIDERS_GITHUB_CLIENT_SECRET) is required"},
		"github open to everyone": {provider("github", github), "auth.providers.github.allowed_users (AUTH_PROVIDERS_GITHUB_ALLOWED_USERS) or auth.providers.github.allowed_organizations (AUTH_PROVIDERS_GITHUB_ALLOWED_ORGANIZATIONS) has to list who may sign in"},
		"reserved id":             {provider("passkey", oidc), "auth.providers.passkey is reserved for passkeys, give the provider another ID"},
		"two primaries":           {"auth:\n  providers:\n    a:\n" + oidc + "      primary: true\n    b:\n" + oidc + "      primary: true\n", "only one sign-in provider can be primary, but auth.providers.a.primary (AUTH_PROVIDERS_A_PRIMARY) and auth.providers.b.primary (AUTH_PROVIDERS_B_PRIMARY) both are"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := load(writeFile(t, "config.yml", c.yaml), devEnv)
			require.ErrorContains(t, err, c.err)
		})
	}

	// A GitHub provider is accepted once it says who may sign in, through users or organizations, and admins may sign in too
	for _, allowed := range []string{"      allowed_users: [octocat]\n", "      allowed_organizations: [acme]\n", "      admin_users: [octocat]\n", "      admin_organizations: [acme]\n"} {
		_, err := load(writeFile(t, "config.yml", provider("github", github+allowed)), devEnv)
		require.NoError(t, err)
	}
}

func TestLoadRejectsMistakesInSignInProviderVariables(t *testing.T) {
	cases := map[string]struct {
		name, err string
	}{
		"unknown option": {"AUTH_PROVIDERS_POCKET_ID_ISUER", "AUTH_PROVIDERS_POCKET_ID_ISUER doesn't end in an option of auth.providers"},
		"no id":          {"AUTH_PROVIDERS_ISSUER", "AUTH_PROVIDERS_ISSUER doesn't end in an option of auth.providers"},
		"double hyphen":  {"AUTH_PROVIDERS_POCKET__ID_ISSUER", `AUTH_PROVIDERS_POCKET__ID_ISSUER names the invalid ID "pocket--id"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			vars := map[string]string{"APP_ENV": "development", "APP_ENCRYPTION_KEY": testEncryptionKey, c.name: "https://id.example.com"}
			_, err := load("", vars)
			require.ErrorContains(t, err, c.err)
		})
	}
}

func TestValidateRejectsARetentionThatWouldPruneEveryFinishedRun(t *testing.T) {
	// The nightly prune deletes everything that finished before now minus the retention, so 0 or less would empty every finished run
	for _, days := range []string{"0", "-1"} {
		t.Run(days, func(t *testing.T) {
			_, err := load("", map[string]string{"APP_ENV": "development", "APP_ENCRYPTION_KEY": testEncryptionKey, "RUNS_RETENTION_DAYS": days})
			require.ErrorContains(t, err, "runs.retention_days (RUNS_RETENTION_DAYS)")
		})
	}

	// The file is held to the same rule as the environment
	_, err := load(writeFile(t, "config.yml", "runs:\n  retention_days: 0\n"), devEnv)
	require.ErrorContains(t, err, "runs.retention_days (RUNS_RETENTION_DAYS)")

	// A single day is the smallest retention the settings page accepts too
	cfg, err := load("", map[string]string{"APP_ENV": "development", "APP_ENCRYPTION_KEY": testEncryptionKey, "RUNS_RETENTION_DAYS": "1"})
	require.NoError(t, err)
	assert.Equal(t, 1, cfg.Runs.RetentionDays)
}
