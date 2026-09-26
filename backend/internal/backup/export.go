package backup

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// API shapes the export reads
type (
	apiProvider struct {
		ID      string  `json:"id"`
		Name    string  `json:"name"`
		Kind    string  `json:"kind"`
		BaseURL *string `json:"baseUrl"`
	}
	apiModel struct {
		ID         string          `json:"id"`
		ProviderID string          `json:"providerId"`
		Model      string          `json:"model"`
		Label      *string         `json:"label"`
		Price      json.RawMessage `json:"price"`
		Caps       json.RawMessage `json:"caps"`
		Enabled    bool            `json:"enabled"`
	}
	apiSecret struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	apiServer struct {
		ID          string            `json:"id"`
		Name        string            `json:"name"`
		Description *string           `json:"description"`
		Transport   string            `json:"transport"`
		Command     *string           `json:"command"`
		Args        []string          `json:"args"`
		Env         map[string]string `json:"env"`
		URL         *string           `json:"url"`
		Headers     map[string]string `json:"headers"`
		OAuth       apiServerOAuth    `json:"oauth"`
		Auth        struct {
			Status string `json:"status"`
		} `json:"auth"`
		Enabled bool `json:"enabled"`
	}
	apiServerOAuth struct {
		ClientID     string   `json:"clientId"`
		ClientSecret string   `json:"clientSecret"`
		Scopes       []string `json:"scopes"`
	}
	apiJobItem struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	apiJobSecret struct {
		EnvName    string `json:"envName"`
		SecretID   string `json:"secretId"`
		SecretName string `json:"secretName"`
	}
	apiJobServer struct {
		ServerID     string   `json:"serverId"`
		ServerName   string   `json:"serverName"`
		AllowedTools []string `json:"allowedTools"`
	}
	apiSettings struct {
		AgentModelID       *string         `json:"agentModelId"`
		UtilityModelID     *string         `json:"utilityModelId"`
		ReflectionModelID  *string         `json:"reflectionModelId"`
		DefaultImage       string          `json:"defaultImage"`
		DefaultLimits      json.RawMessage `json:"defaultLimits"`
		DailySpendLimitUSD float64         `json:"dailySpendLimitUsd"`
		RetentionDays      int             `json:"retentionDays"`
		NotifyOn           []string        `json:"notifyOn"`
		NotifySecret       *string         `json:"notifySecret"`
		NotifyWebhookURL   *string         `json:"notifyWebhookUrl"`
		UsageUnit          string          `json:"usageUnit"`
	}
	apiVersionItem struct {
		Version int64   `json:"version"`
		Summary *string `json:"summary"`
	}
	apiVersion struct {
		Version int64           `json:"version"`
		Summary *string         `json:"summary"`
		Content json.RawMessage `json:"content"`
	}
)

// ExportOptions shape what an export contains
type ExportOptions struct {
	// History includes every playbook version instead of only the current one
	History bool
}

// Export reads the workspace's configuration into a document
func Export(ctx context.Context, c *Client, opts ExportOptions) (*Document, error) {
	doc := &Document{Format: FormatName, Version: FormatVersion, ExportedAt: time.Now().UTC().Format(time.RFC3339)}

	// Providers and their models, where models are referenced by provider and name everywhere else
	providers, err := listAll[apiProvider](ctx, c, "/api/providers")
	if err != nil {
		return nil, err
	}
	models, err := listAll[apiModel](ctx, c, "/api/models")
	if err != nil {
		return nil, err
	}
	providerNames := map[string]string{}
	for _, p := range providers {
		providerNames[p.ID] = p.Name
		out := Provider{Name: p.Name, Kind: p.Kind, BaseURL: deref(p.BaseURL), Models: []Model{}}
		for _, m := range models {
			if m.ProviderID == p.ID {
				out.Models = append(out.Models, Model{Model: m.Model, Label: deref(m.Label), Price: m.Price, Caps: m.Caps, Disabled: !m.Enabled})
			}
		}
		doc.Providers = append(doc.Providers, out)
	}
	modelRef := func(id *string) *ModelRef {
		if id == nil {
			return nil
		}
		for _, m := range models {
			if m.ID == *id {
				return &ModelRef{Provider: providerNames[m.ProviderID], Model: m.Model}
			}
		}
		return nil
	}

	// Settings without the notification webhook, whose URL often is the credential itself
	var s apiSettings
	if err := c.do(ctx, http.MethodGet, "/api/settings", nil, &s); err != nil {
		return nil, err
	}
	doc.Settings = Settings{
		AgentModel: modelRef(s.AgentModelID), UtilityModel: modelRef(s.UtilityModelID), ReflectionModel: modelRef(s.ReflectionModelID),
		DefaultImage: s.DefaultImage, DefaultLimits: s.DefaultLimits, DailySpendLimitUSD: &s.DailySpendLimitUSD, RetentionDays: &s.RetentionDays,
		NotifyOn: s.NotifyOn, NotifySecret: deref(s.NotifySecret), HadNotifyWebhook: s.NotifyWebhookURL != nil, UsageUnit: s.UsageUnit,
	}

	// Secrets by name only
	secrets, err := listAll[apiSecret](ctx, c, "/api/secrets")
	if err != nil {
		return nil, err
	}
	doc.Secrets = []string{}
	for _, sec := range secrets {
		doc.Secrets = append(doc.Secrets, sec.Name)
	}
	slices.Sort(doc.Secrets)

	// MCP servers, with credential-like values redacted
	servers, err := listAll[apiServer](ctx, c, "/api/mcp-servers")
	if err != nil {
		return nil, err
	}
	for _, sv := range servers {
		env, envRedacted := redact(sv.Env)
		headers, headerRedacted := redact(sv.Headers)
		args, argsRedacted := redactArgs(sv.Args)
		redacted := append(prefixed("env.", envRedacted), prefixed("headers.", headerRedacted)...)
		redacted = append(redacted, argsRedacted...)
		serverURL, urlRedacted := redactURL(deref(sv.URL))
		if urlRedacted {
			redacted = append(redacted, "url")
		}
		oauth, oauthRedacted := exportOAuth(sv.OAuth)
		redacted = append(redacted, oauthRedacted...)
		slices.Sort(redacted)
		doc.MCPServers = append(doc.MCPServers, MCPServer{
			Name: sv.Name, Description: deref(sv.Description), Transport: sv.Transport, Command: deref(sv.Command), Args: args,
			Env: env, URL: serverURL, Headers: headers, OAuth: oauth, LoggedIn: sv.Auth.Status == "oauth", Enabled: sv.Enabled, Redacted: redacted,
		})
	}

	// Jobs with their attachments and playbooks
	jobs, err := listAll[apiJobItem](ctx, c, "/api/jobs")
	if err != nil {
		return nil, err
	}
	for _, item := range jobs {
		job, err := exportJob(ctx, c, item.ID, modelRef, opts)
		if err != nil {
			return nil, err
		}
		doc.Jobs = append(doc.Jobs, job)
	}
	return doc, nil
}

func exportJob(ctx context.Context, c *Client, id string, modelRef func(*string) *ModelRef, opts ExportOptions) (Job, error) {
	var full map[string]json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/jobs/"+esc(id), nil, &full); err != nil {
		return Job{}, err
	}
	job := Job{Fields: map[string]json.RawMessage{}}
	for _, key := range jobFieldKeys {
		if v, ok := full[key]; ok && string(v) != "null" {
			job.Fields[key] = v
		}
	}
	var modelID *string
	_ = json.Unmarshal(full["modelId"], &modelID)
	job.Model = modelRef(modelID)

	var secrets []apiJobSecret
	if err := c.do(ctx, http.MethodGet, "/api/jobs/"+esc(id)+"/secrets", nil, &secrets); err != nil {
		return Job{}, err
	}
	for _, s := range secrets {
		job.Secrets = append(job.Secrets, JobSecret{Env: s.EnvName, Secret: s.SecretName})
	}
	var servers []apiJobServer
	if err := c.do(ctx, http.MethodGet, "/api/jobs/"+esc(id)+"/mcp-servers", nil, &servers); err != nil {
		return Job{}, err
	}
	for _, s := range servers {
		job.MCPServers = append(job.MCPServers, JobServer{Server: s.ServerName, AllowedTools: s.AllowedTools})
	}

	var current apiVersion
	if err := c.do(ctx, http.MethodGet, "/api/jobs/"+esc(id)+"/playbook", nil, &current); err != nil {
		return Job{}, err
	}
	if current.Version > 0 {
		job.Playbook = current.Content
	}
	if opts.History && current.Version > 0 {
		versions, err := listAll[apiVersionItem](ctx, c, "/api/jobs/"+esc(id)+"/playbook/versions?sort=version")
		if err != nil {
			return Job{}, err
		}
		for _, v := range versions {
			var full apiVersion
			if err := c.do(ctx, http.MethodGet, "/api/jobs/"+esc(id)+"/playbook/versions/"+strconv.FormatInt(v.Version, 10), nil, &full); err != nil {
				return Job{}, err
			}
			job.History = append(job.History, Version{Version: full.Version, Summary: deref(full.Summary), Content: full.Content})
		}
	}
	return job, nil
}

func prefixed(prefix string, keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, prefix+k)
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// exportOAuth keeps a server's OAuth client settings, leaving out a client secret that isn't a secret reference
func exportOAuth(o apiServerOAuth) (*MCPServerOAuth, []string) {
	if o.ClientID == "" && len(o.Scopes) == 0 {
		return nil, nil
	}
	kept, removed := redact(map[string]string{"clientSecret": o.ClientSecret})
	out := &MCPServerOAuth{ClientID: o.ClientID, ClientSecret: kept["clientSecret"], Scopes: o.Scopes}
	if o.ClientSecret == "" {
		removed = nil
	}
	return out, prefixed("oauth.", removed)
}
