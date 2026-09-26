//go:build unit

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// env fakes the environment with a fixed set of variables
func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// writeFile creates a file in a temporary directory and returns its path
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// devEnv is the smallest environment that passes validation without a key
var devEnv = map[string]string{"APP_ENV": "development"}

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
	// Default panics on a default tag its option can't parse
	assert.NotPanics(t, func() { Default() })
}

func TestEnvNameFollowsTheYAMLPath(t *testing.T) {
	assert.Equal(t, "SERVER_PORT", EnvName("server.port"))
	assert.Equal(t, "FILE_STORAGE_S3_SECRET_ACCESS_KEY", EnvName("file_storage.s3.secret_access_key"))
	assert.Equal(t, "HA_ACTORS_BIND_ADDRESS", EnvName("ha.actors.bind_address"))
}

func TestDefaultAppliesTheSchemaDefaults(t *testing.T) {
	cfg := Default()
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, AppEnvProduction, cfg.App.Env)
	assert.True(t, cfg.FileStorage.S3.UseSSL)
	assert.True(t, cfg.Network.AllowPrivateTargets)
	assert.Equal(t, 12*time.Hour, cfg.Models.CatalogRefreshInterval)
	assert.Empty(t, cfg.OIDC.AllowedGroups)
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
oidc:
  client_secret:
  allowed_groups: [admins, ops]
sandbox:
  docker:
    dns: 1.1.1.1, 9.9.9.9
models:
  catalog_refresh_interval: 0
`)

	cfg, err := load(path, env(map[string]string{
		"SERVER_PORT": "9100",
		"LOG_JSON":    "true",
		// An empty variable is what Compose passes for an unset one, so it must not clear the file's value
		"SERVER_BROKER_PORT": "",
	}))
	require.NoError(t, err)

	assert.Equal(t, "https://umpteenth.example.com", cfg.App.URL)
	assert.Equal(t, 9100, cfg.Server.Port)
	assert.Equal(t, 9001, cfg.Server.BrokerPort)
	assert.Equal(t, "debug", cfg.Log.Level)
	assert.True(t, cfg.Log.JSON)
	assert.Equal(t, []string{"admins", "ops"}, cfg.OIDC.AllowedGroups)
	assert.Equal(t, []string{"1.1.1.1", "9.9.9.9"}, cfg.Sandbox.Docker.DNS)
	assert.Equal(t, time.Duration(0), cfg.Models.CatalogRefreshInterval)
	// Options the file leaves out keep their defaults
	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, 3, cfg.Runs.MaxConcurrent)
}

func TestLoadReadsFileVariantsOfEveryOption(t *testing.T) {
	keyFile := writeFile(t, "key", "a-key-that-is-long-enough\n")

	cfg, err := load("", env(map[string]string{"APP_ENCRYPTION_KEY_FILE": keyFile}))
	require.NoError(t, err)
	assert.Equal(t, "a-key-that-is-long-enough", cfg.App.EncryptionKey)

	_, err = load("", env(map[string]string{"APP_ENCRYPTION_KEY_FILE": keyFile, "APP_ENCRYPTION_KEY": "another-long-enough-key"}))
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
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := load(writeFile(t, "config.yml", c.yaml), env(devEnv))
			require.ErrorContains(t, err, c.err)
		})
	}
}

func TestLoadNamesTheEnvironmentVariableOfAnInvalidValue(t *testing.T) {
	_, err := load("", env(map[string]string{"APP_ENV": "development", "RUNS_MAX_CONCURRENT": "many"}))
	require.ErrorContains(t, err, `RUNS_MAX_CONCURRENT: invalid whole number "many"`)
}

func TestLoadFindsTheDefaultFileOnlyWhenPresent(t *testing.T) {
	t.Chdir(t.TempDir())

	// Without a file the defaults and the environment are enough
	cfg, err := load("", env(devEnv))
	require.NoError(t, err)
	assert.Equal(t, 8080, cfg.Server.Port)

	require.NoError(t, os.WriteFile("config.yaml", []byte("server:\n  port: 9000\n"), 0o600))
	cfg, err = load("", env(devEnv))
	require.NoError(t, err)
	assert.Equal(t, 9000, cfg.Server.Port)

	// A file that was asked for by name has to exist
	_, err = load("missing.yml", env(devEnv))
	require.ErrorContains(t, err, "failed to read the config file")
}

func TestLoadAcceptsAnEmptyFileAndSections(t *testing.T) {
	cfg, err := load(writeFile(t, "config.yml", "# nothing yet\noidc:\n"), env(devEnv))
	require.NoError(t, err)
	assert.Empty(t, cfg.OIDC.Issuer)
}

func TestValidateDerivesPathsFromTheDataDirectory(t *testing.T) {
	cfg, err := load("", env(map[string]string{"APP_ENV": "development", "APP_DATA_DIR": "/srv/umpteenth"}))
	require.NoError(t, err)
	assert.Equal(t, "/srv/umpteenth/umpteenth.db", cfg.Database.ConnectionString)
	assert.Equal(t, DbProviderSqlite, cfg.Database.Provider())
	assert.Equal(t, "/srv/umpteenth/blobs", cfg.FileStorage.Path)
}

func TestValidateKeepsADatabaseFromBeforeTheRename(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent-gig.db"), nil, 0o600))

	cfg, err := load("", env(map[string]string{"APP_ENV": "development", "APP_DATA_DIR": dir}))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "agent-gig.db"), cfg.Database.ConnectionString)
}

func TestValidateRejectsInconsistentCombinations(t *testing.T) {
	_, err := load("", env(map[string]string{}))
	require.ErrorContains(t, err, "app.encryption_key (APP_ENCRYPTION_KEY) is required")

	_, err = load("", env(map[string]string{"APP_ENV": "development", "HA_ENABLED": "true"}))
	require.ErrorContains(t, err, "ha.enabled (HA_ENABLED) requires a Postgres database.connection_string (DATABASE_CONNECTION_STRING)")

	_, err = load("", env(map[string]string{"APP_ENV": "development", "MODELS_CATALOG_REFRESH_INTERVAL": "1m"}))
	require.ErrorContains(t, err, "must be 0 to turn it off or at least 5m")

	_, err = load("", env(map[string]string{"APP_ENV": "development", "HA_ACTORS_PORT": "70000"}))
	require.ErrorContains(t, err, "ha.actors.port (HA_ACTORS_PORT) must be a port")
}

func TestValidateFillsADevelopmentKeyOutsideProduction(t *testing.T) {
	cfg, err := load("", env(devEnv))
	require.NoError(t, err)
	assert.Equal(t, insecureDevelopmentKey, cfg.App.EncryptionKey)
	assert.Equal(t, DbProviderPostgres, Database{ConnectionString: "postgresql://db/umpteenth"}.Provider())
}

func TestExampleFileDocumentsEveryOptionWithItsDefault(t *testing.T) {
	const example = "../../../config.example.yml"

	// Every option of the schema appears in the example, so admins can discover it there
	content, err := os.ReadFile(example)
	require.NoError(t, err)
	var doc yaml.Node
	require.NoError(t, yaml.Unmarshal(content, &doc))
	documented := map[string]bool{}
	var walk func(n *yaml.Node, prefix string)
	walk = func(n *yaml.Node, prefix string) {
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := prefix + n.Content[i].Value
			documented[key] = true
			if n.Content[i+1].Kind == yaml.MappingNode {
				walk(n.Content[i+1], key+".")
			}
		}
	}
	walk(doc.Content[0], "")
	for _, o := range newSchema(&Config{}).options {
		assert.True(t, documented[o.key], "%s is missing from config.example.yml", o.key)
	}

	// The values shown are the defaults, apart from the OIDC placeholders
	cfg := Default()
	require.NoError(t, newSchema(cfg).applyFile(example))
	want := Default()
	want.OIDC.Issuer, want.OIDC.ClientID = "https://id.example.com", "umpteenth"
	want.OIDC.AllowedGroups, want.Sandbox.Docker.DNS = []string{}, []string{}
	assert.Equal(t, want, cfg)
}
