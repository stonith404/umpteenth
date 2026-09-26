package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// insecureDevelopmentKey stands in for a missing encryption key outside production
const insecureDevelopmentKey = "umpteenth-insecure-development-key"

// Validate normalizes derived values and rejects inconsistent combinations
func (c *Config) Validate() error {
	// Case and a trailing slash carry no meaning in these values
	c.App.URL = strings.TrimRight(c.App.URL, "/")
	c.App.Env = AppEnv(strings.ToLower(string(c.App.Env)))
	c.Log.Level = strings.ToLower(c.Log.Level)
	c.FileStorage.Backend = strings.ToLower(c.FileStorage.Backend)
	c.Sandbox.EgressFilter = strings.ToLower(c.Sandbox.EgressFilter)

	_, err := url.Parse(c.App.URL)
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", describe("app.url"), err)
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
		// Installs from before the rename to Umpteenth keep their database file until it is moved, otherwise they would start empty
		if legacy := filepath.Join(c.App.DataDir, "agent-gig.db"); !fileExists(c.Database.ConnectionString) && fileExists(legacy) {
			c.Database.ConnectionString = legacy
		}
	}
	if c.FileStorage.Path == "" {
		c.FileStorage.Path = filepath.Join(c.App.DataDir, "blobs")
	}

	// HA needs a database every replica can reach
	if c.HA.Enabled && c.Database.Provider() != DbProviderPostgres {
		return fmt.Errorf("%s requires a Postgres %s", describe("ha.enabled"), describe("database.connection_string"))
	}

	switch c.FileStorage.Backend {
	case FileBackendFilesystem, FileBackendS3, FileBackendDatabase:
	default:
		return fmt.Errorf("unknown %s %q, use filesystem, s3 or database", describe("file_storage.backend"), c.FileStorage.Backend)
	}

	switch c.Sandbox.EgressFilter {
	case "auto", "required", "off":
	default:
		return fmt.Errorf("unknown %s %q, use auto, required or off", describe("sandbox.egress_filter"), c.Sandbox.EgressFilter)
	}

	// A development or test instance may run without a configured key, but production must not
	if c.App.EncryptionKey == "" {
		if c.App.Env.IsProduction() {
			return fmt.Errorf("%s is required", describe("app.encryption_key"))
		}
		c.App.EncryptionKey = insecureDevelopmentKey
	}
	if len(c.App.EncryptionKey) < 16 {
		return fmt.Errorf("%s must be at least 16 bytes", describe("app.encryption_key"))
	}

	if c.Runs.MaxConcurrent < 1 {
		c.Runs.MaxConcurrent = 1
	}

	// A refresh more often than every few minutes would only hammer models.dev and the providers' servers
	interval := c.Models.CatalogRefreshInterval
	if interval < 0 || (interval > 0 && interval < 5*time.Minute) {
		return errors.New(describe("models.catalog_refresh_interval") + " must be 0 to turn it off or at least 5m")
	}

	return nil
}
