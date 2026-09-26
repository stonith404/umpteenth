// Package config loads the instance configuration from an optional YAML file and the environment
package config

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
)

type (
	AppEnv     string
	DbProvider string
)

const (
	AppEnvProduction  AppEnv = "production"
	AppEnvDevelopment AppEnv = "development"
	AppEnvTest        AppEnv = "test"

	DbProviderSqlite   DbProvider = "sqlite"
	DbProviderPostgres DbProvider = "postgres"

	FileBackendFilesystem = "filesystem"
	FileBackendS3         = "s3"
	FileBackendDatabase   = "database"

	SandboxAdapterDocker = "docker"
	SandboxAdapterNone   = "none"
)

func (a AppEnv) IsProduction() bool { return a == AppEnvProduction }
func (a AppEnv) IsTest() bool       { return a == AppEnvTest }

// Config is the schema of the YAML config file
// The yaml tags are the only place option names are defined: every option can also be set through the environment variable EnvName derives from its YAML path
// A default tag holds the option's default, written the way its environment variable would be
type Config struct {
	App         App         `yaml:"app"`
	Server      Server      `yaml:"server"`
	Log         Log         `yaml:"log"`
	Database    Database    `yaml:"database"`
	FileStorage FileStorage `yaml:"file_storage"`
	Auth        Auth        `yaml:"auth"`
	Workspaces  Workspaces  `yaml:"workspaces"`
	Sandbox     Sandbox     `yaml:"sandbox"`
	Runs        Runs        `yaml:"runs"`
	Network     Network     `yaml:"network"`
	Models      Models      `yaml:"models"`
	HA          HA          `yaml:"ha"`
}

type App struct {
	// URL is the public URL, used for the sign-in and MCP OAuth callbacks, webhook URLs and links in notifications
	URL string `yaml:"url" default:"http://localhost:8080"`
	Env AppEnv `yaml:"env" default:"production"`
	// DataDir holds the SQLite database and the stored files unless those are configured elsewhere
	DataDir string `yaml:"data_dir" default:"data"`
	// EncryptionKey encrypts secrets at rest and signs sessions
	EncryptionKey string `yaml:"encryption_key"`
}

type Server struct {
	Host string `yaml:"host" default:"0.0.0.0"`
	Port int    `yaml:"port" default:"8080"`
	// BrokerPort serves the API sandboxes talk to, separate from Port so the two can be exposed differently
	BrokerPort int `yaml:"broker_port" default:"8081"`
	// TrustProxy takes client IPs from X-Forwarded-For, which is only safe behind a reverse proxy that sets it
	TrustProxy bool `yaml:"trust_proxy"`
}

type Log struct {
	Level string `yaml:"level" default:"info"`
	JSON  bool   `yaml:"json"`
}

type Database struct {
	// ConnectionString is a SQLite file path or a postgres:// URL, and defaults to umpteenth.db in the data directory
	ConnectionString string `yaml:"connection_string"`
}

// Provider derives the database engine from the connection string, like Pocket ID
func (d Database) Provider() DbProvider {
	if strings.HasPrefix(d.ConnectionString, "postgres://") || strings.HasPrefix(d.ConnectionString, "postgresql://") {
		return DbProviderPostgres
	}
	return DbProviderSqlite
}

// FileStorage holds blobs such as tool outputs, artifacts and build logs
type FileStorage struct {
	Backend string `yaml:"backend" default:"filesystem"`
	// Path is the directory of the filesystem backend, and defaults to blobs in the data directory
	Path string `yaml:"path"`
	S3   S3     `yaml:"s3"`
}

type S3 struct {
	Endpoint        string `yaml:"endpoint"`
	Region          string `yaml:"region"`
	Bucket          string `yaml:"bucket"`
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
	UseSSL          bool   `yaml:"use_ssl" default:"true"`
}

// Sign-in provider types, each backed by its own implementation in the auth module
const (
	AuthProviderOIDC   = "oidc"
	AuthProviderGitHub = "github"
)

type Auth struct {
	// Providers are the ways users can sign in, keyed by an ID of lowercase letters, digits and hyphens
	// Their options are set through variables like AUTH_PROVIDERS_POCKET_ID_ISSUER, where the ID's hyphens become underscores
	Providers map[string]*AuthProvider `yaml:"providers"`
}

// AuthProvider is an OAuth client users sign in through, registered with the redirect URI <app.url>/api/auth/callback/<id>
// Every type shares the options up to client_secret, and the others only apply to the type their comment names
type AuthProvider struct {
	// Type is oidc for any OpenID Connect identity provider or github for GitHub accounts
	Type string `yaml:"type"`
	// Name labels the provider's sign-in button
	Name string `yaml:"name"`
	// Icon is an image shown on the sign-in button, given as an http(s) URL or a data:image URI
	Icon string `yaml:"icon"`
	// Primary gives the provider the large sign-in button, which at most one provider can have
	Primary      bool   `yaml:"primary"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	// Issuer is the URL of an oidc provider
	Issuer string `yaml:"issuer"`
	// AllowedGroups narrows who may sign in through an oidc provider to members of these groups, checked against the groups claim
	AllowedGroups []string `yaml:"allowed_groups"`
	// AdminGroups makes the members of these groups instance admins when they sign in through an oidc provider
	AdminGroups []string `yaml:"admin_groups"`
	// AllowedUsers are the GitHub usernames that may sign in through a github provider
	AllowedUsers []string `yaml:"allowed_users"`
	// AllowedOrganizations lets the members of these GitHub organizations sign in through a github provider
	AllowedOrganizations []string `yaml:"allowed_organizations"`
	// AdminUsers are the GitHub usernames that become instance admins when they sign in through a github provider
	AdminUsers []string `yaml:"admin_users"`
	// AdminOrganizations makes the members of these GitHub organizations instance admins when they sign in through a github provider
	AdminOrganizations []string `yaml:"admin_organizations"`
}

// Workspaces decides whether people can have several workspaces
type Workspaces struct {
	// Enabled lets people create workspaces, invite others to them and switch between them
	// When it is off, everyone who signs in shares one workspace
	Enabled bool `yaml:"enabled"`
}

type Sandbox struct {
	Adapter string `yaml:"adapter" default:"docker"`
	Image   string `yaml:"image" default:"ghcr.io/stonith404/umpteenth-sandbox:latest"`
	// BrokerHost overrides the address sandboxes use to reach this replica's broker
	BrokerHost string `yaml:"broker_host"`
	// AllowUnrestrictedNetwork offers jobs the unrestricted network, which bypasses the egress proxy and reaches everything the host can
	AllowUnrestrictedNetwork bool     `yaml:"allow_unrestricted_network" default:"true"`
	Registry                 Registry `yaml:"registry"`
	Docker                   Docker   `yaml:"docker"`
}

// Registry stores job images so replicas share builds
type Registry struct {
	// Repository is where job images are pushed, e.g. ghcr.io/acme/umpteenth-jobs
	Repository string `yaml:"repository"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
}

type Docker struct {
	Runtime string `yaml:"runtime" default:"runc"`
	// DNS are the resolvers of unrestricted sandboxes instead of Docker's embedded DNS, which gVisor can't reach
	DNS []string `yaml:"dns"`
}

type Runs struct {
	// MaxConcurrent is how many runs execute at once on each replica
	MaxConcurrent int `yaml:"max_concurrent" default:"3"`
	// DailySpendLimitUSD and RetentionDays only seed new workspaces, which can change them in their settings
	DailySpendLimitUSD float64 `yaml:"daily_spend_limit_usd"`
	RetentionDays      int     `yaml:"retention_days" default:"90"`
}

type Network struct {
	// AllowPrivateTargets lets Umpteenth itself call private addresses, e.g. a local Ollama, an MCP server on the LAN or a notification webhook
	AllowPrivateTargets bool `yaml:"allow_private_targets" default:"true"`
	// BlockedTargets are IP addresses and CIDR ranges that neither Umpteenth itself nor sandboxes may reach, e.g. the host's own public address
	BlockedTargets []string `yaml:"blocked_targets"`
}

// BlockedRanges parses the blocked targets, where a single address stands for a range of just itself
func (n Network) BlockedRanges() ([]netip.Prefix, error) {
	ranges := make([]netip.Prefix, 0, len(n.BlockedTargets))
	for _, entry := range n.BlockedTargets {
		entry = strings.TrimSpace(entry)
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			ranges = append(ranges, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("%q is neither an IP address nor a CIDR range", entry)
		}
		addr = addr.Unmap()
		ranges = append(ranges, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return ranges, nil
}

type Models struct {
	// CatalogRefreshInterval is how often the models.dev catalog is fetched and every provider's model list synced; 0 keeps the catalog bundled with the build
	CatalogRefreshInterval time.Duration `yaml:"catalog_refresh_interval" default:"12h"`
}

type HA struct {
	Enabled bool `yaml:"enabled"`
	// ReplicaID names this replica, and defaults to the hostname
	ReplicaID string `yaml:"replica_id"`
	Actors    Actors `yaml:"actors"`
}

// Actors configures the Francis actor host, whose peer connections link the replicas
type Actors struct {
	// Host is the address other replicas reach this replica at
	Host string `yaml:"host" default:"127.0.0.1"`
	Port int    `yaml:"port" default:"7571"`
	// BindAddress is the interface the peer server listens on, and defaults to all interfaces with HA and to Host otherwise
	BindAddress string `yaml:"bind_address"`
}
