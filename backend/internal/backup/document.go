package backup

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Document is an export of one workspace's configuration
// It never holds a credential: secrets are listed by name, API keys and the notification webhook are left out, and credential-like MCP values, arguments and URL parts are redacted
type Document struct {
	Format     string      `json:"format"`
	Version    int         `json:"version"`
	ExportedAt string      `json:"exportedAt"`
	Settings   Settings    `json:"settings"`
	Providers  []Provider  `json:"providers"`
	Secrets    []string    `json:"secrets"`
	MCPServers []MCPServer `json:"mcpServers"`
	Jobs       []Job       `json:"jobs"`
}

// Document format identifiers
const (
	FormatName    = "umpteenth-export"
	FormatVersion = 1
)

// ModelRef names a model by its provider and model name, since IDs differ between instances
type ModelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Settings are the workspace settings, with models as references
type Settings struct {
	AgentModel         *ModelRef       `json:"agentModel,omitempty"`
	UtilityModel       *ModelRef       `json:"utilityModel,omitempty"`
	ReflectionModel    *ModelRef       `json:"reflectionModel,omitempty"`
	DefaultImage       string          `json:"defaultImage,omitempty"`
	DefaultLimits      json.RawMessage `json:"defaultLimits,omitempty"`
	DailySpendLimitUSD *float64        `json:"dailySpendLimitUsd,omitempty"`
	RetentionDays      *int            `json:"retentionDays,omitempty"`
	NotifyOn           []string        `json:"notifyOn,omitempty"`
	NotifySecret       string          `json:"notifySecret,omitempty"`
	// HadNotifyWebhook tells the import to ask for the webhook URL, which the export leaves out
	HadNotifyWebhook bool `json:"hadNotifyWebhook,omitempty"`
	// UsageUnit is price or tokens, and it is empty in exports of instances that predate it
	UsageUnit string `json:"usageUnit,omitempty"`
}

// Provider is a model provider without its API key
type Provider struct {
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	BaseURL string  `json:"baseUrl,omitempty"`
	Models  []Model `json:"models"`
}

type Model struct {
	Model string          `json:"model"`
	Label string          `json:"label,omitempty"`
	Price json.RawMessage `json:"price"`
	Caps  json.RawMessage `json:"caps"`
	// Disabled models were turned off by an admin and stay off after an import
	Disabled bool `json:"disabled,omitempty"`
}

// MCPServer is an MCP server's configuration, with Redacted naming what was left out: env and header keys, argument positions and the URL
type MCPServer struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Transport   string            `json:"transport"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	OAuth       *MCPServerOAuth   `json:"oauth,omitempty"`
	// LoggedIn records that the server had an OAuth login, which exports leave out like every other credential
	LoggedIn bool     `json:"loggedIn,omitempty"`
	Enabled  bool     `json:"enabled"`
	Redacted []string `json:"redacted,omitempty"`
}

// MCPServerOAuth is an HTTP server's OAuth client settings
type MCPServerOAuth struct {
	ClientID     string   `json:"clientId,omitempty"`
	ClientSecret string   `json:"clientSecret,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
}

// Job is a job's editable fields, its model, what it is attached to and its playbook
type Job struct {
	Fields     map[string]json.RawMessage `json:"fields"`
	Model      *ModelRef                  `json:"model,omitempty"`
	Secrets    []JobSecret                `json:"secrets,omitempty"`
	MCPServers []JobServer                `json:"mcpServers,omitempty"`
	Playbook   json.RawMessage            `json:"playbook,omitempty"`
	// History holds every playbook version, oldest first, when the export was asked for it
	History []Version `json:"history,omitempty"`
}

type JobSecret struct {
	Env    string `json:"env"`
	Secret string `json:"secret"`
}

type JobServer struct {
	Server       string   `json:"server"`
	AllowedTools []string `json:"allowedTools"`
}

type Version struct {
	Version int64           `json:"version"`
	Summary string          `json:"summary,omitempty"`
	Content json.RawMessage `json:"content"`
}

// jobFieldKeys are the fields of a job that an import sends back to the create endpoint
var jobFieldKeys = []string{
	"name", "instruction", "spec", "image", "network", "allowedDomains", "allowPrivateNetwork", "runAsRoot", "limits",
	"selfImprove", "graduate", "concurrency", "cron", "timezone",
}

var (
	// credentialName matches env and header names that usually carry a credential
	credentialName = regexp.MustCompile(`(?i)(auth|token|key|secret|pass|cookie|credential|session|bearer)`)
	// credentialValue matches values shaped like well-known credentials
	// A URL with a user and password, such as a database connection string, counts as one too
	credentialValue = regexp.MustCompile(`(?i)(^bearer\s|\bsk-[a-z0-9_-]{16,}|\bgh[pousr]_[a-z0-9]{20,}|\bxox[abposr]-|\bAKIA[0-9A-Z]{16}\b|-----BEGIN [A-Z ]*PRIVATE KEY-----|[a-z][a-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@)`)
	// secretRef is a value made only of {{secret:NAME}} references and plain text around them, which is safe to keep
	secretRef = regexp.MustCompile(`\{\{\s*secret:[^}]+\}\}`)
)

// redact removes the values of a map that could be credentials, keeping secret references, and returns the removed keys
func redact(values map[string]string) (map[string]string, []string) {
	if len(values) == 0 {
		return nil, nil
	}
	kept := map[string]string{}
	var removed []string
	for k, v := range values {
		literal := strings.TrimSpace(secretRef.ReplaceAllString(v, ""))
		switch {
		case literal == "" || (secretRef.MatchString(v) && !credentialValue.MatchString(literal)):
			// A value that is a secret reference, possibly with a prefix such as "Bearer ", carries no credential itself
			kept[k] = v
		case credentialName.MatchString(k) || credentialValue.MatchString(v):
			removed = append(removed, k)
		default:
			kept[k] = v
		}
	}
	return kept, removed
}

// redactedValue stands in for a credential an export leaves out of a list or URL, where the entry itself has to stay
const redactedValue = "REDACTED"

// redactArgs replaces arguments that carry a credential, and the value after a credential-like flag, returning the positions it replaced
func redactArgs(args []string) ([]string, []string) {
	out := slices.Clone(args)
	var removed []string
	for i := 0; i < len(out); i++ {
		arg := out[i]
		literal := strings.TrimSpace(secretRef.ReplaceAllString(arg, ""))
		flag, value, hasValue := strings.Cut(arg, "=")
		isFlag := strings.HasPrefix(arg, "-") && credentialName.MatchString(flag)
		switch {
		case secretRef.MatchString(arg) && !credentialValue.MatchString(literal):
			continue
		case isFlag && hasValue && value != "":
			out[i] = flag + "=" + redactedValue
		case isFlag && !hasValue && i+1 < len(out) && !strings.HasPrefix(out[i+1], "-"):
			// The flag stays and the value after it goes, e.g. --api-key abc
			i++
			if secretRef.MatchString(out[i]) {
				continue
			}
			out[i] = redactedValue
		case credentialValue.MatchString(arg):
			out[i] = redactedValue
		default:
			continue
		}
		removed = append(removed, fmt.Sprintf("args.%d", i))
	}
	return out, removed
}

// redactURL removes the password and credential-like query parameters of a URL, reporting whether it changed anything
// A user without a password is removed too, since a URL such as https://TOKEN@host carries its credential as the user
func redactURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || secretRef.MatchString(raw) {
		return raw, false
	}
	changed := false
	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), redactedValue)
		} else {
			u.User = url.User(redactedValue)
		}
		changed = true
	}
	query := u.Query()
	for key := range query {
		if credentialName.MatchString(key) {
			query.Set(key, redactedValue)
			changed = true
		}
	}
	if !changed {
		return raw, false
	}
	u.RawQuery = query.Encode()
	return u.String(), true
}
