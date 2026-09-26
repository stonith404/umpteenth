// Package config loads the instance configuration from an optional YAML file and the environment
package config

import (
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
	OIDC        OIDC        `yaml:"oidc"`
	Sandbox     Sandbox     `yaml:"sandbox"`
	Runs        Runs        `yaml:"runs"`
	Network     Network     `yaml:"network"`
	Models      Models      `yaml:"models"`
	Providers   Providers   `yaml:"providers"`
	HA          HA          `yaml:"ha"`
}

type App struct {
	// URL is the public URL, used for the OIDC and MCP OAuth callbacks, webhook URLs and links in notifications
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

type OIDC struct {
	Issuer       string `yaml:"issuer"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	// AllowedGroups narrows who may sign in to members of these groups, checked against the groups claim
	AllowedGroups []string `yaml:"allowed_groups"`
}

type Sandbox struct {
	Adapter string `yaml:"adapter" default:"docker"`
	Image   string `yaml:"image" default:"ghcr.io/stonith404/umpteenth-sandbox:latest"`
	// BrokerHost overrides the address sandboxes use to reach this replica's broker
	BrokerHost string `yaml:"broker_host"`
	// EgressFilter decides how internet sandboxes are kept off private networks: auto, required or off
	EgressFilter string   `yaml:"egress_filter" default:"auto"`
	Registry     Registry `yaml:"registry"`
	Docker       Docker   `yaml:"docker"`
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
	// DNS are the resolvers of internet sandboxes instead of Docker's embedded DNS, which gVisor can't reach
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
}

type Models struct {
	// CatalogRefreshInterval is how often the models.dev catalog is fetched and every provider's model list synced; 0 keeps the catalog bundled with the build
	CatalogRefreshInterval time.Duration `yaml:"catalog_refresh_interval" default:"12h"`
}

// Providers holds the API keys of the Anthropic and OpenAI providers a fresh install creates on first start, and is ignored afterwards
type Providers struct {
	AnthropicAPIKey string `yaml:"anthropic_api_key"`
	OpenAIAPIKey    string `yaml:"openai_api_key"`
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
