package backup

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
)

// ImportOptions shape an import
type ImportOptions struct {
	// SecretValues fill in secrets the target doesn't have yet, by name
	SecretValues map[string]string
	// DryRun reports what the import would do without changing anything
	DryRun bool
}

// Report says what an import did and what is left to do by hand
type Report struct {
	Created []string
	Skipped []string
	// Todo lists what the import couldn't restore, such as API keys and secret values
	Todo []string
}

// Import restores a document into the workspace the client's token belongs to
// Everything is matched by name, so importing twice creates nothing twice and a second run fills in what the first one had to skip
func Import(ctx context.Context, c *Client, doc *Document, opts ImportOptions) (*Report, error) {
	if doc.Format != FormatName || doc.Version != FormatVersion {
		return nil, fmt.Errorf("this is not an Umpteenth export of format version %d", FormatVersion)
	}
	// Providers and settings need an admin, so a member's token would stop halfway through otherwise
	var me struct {
		Workspace struct {
			Role string `json:"role"`
		} `json:"workspace"`
	}
	err := c.do(ctx, http.MethodGet, "/api/users/me", nil, &me)
	if err != nil {
		return nil, err
	}
	if me.Workspace.Role != "admin" && me.Workspace.Role != "owner" {
		return nil, errors.New("importing needs an API token created by an admin or the owner of the workspace")
	}

	r := &Report{}
	im := importer{c: c, opts: opts, r: r}

	// Providers and models come first, since jobs and settings reference models
	modelIDs, err := im.providers(ctx, doc.Providers)
	if err != nil {
		return r, err
	}
	secretIDs, err := im.secrets(ctx, doc.Secrets, jobSecretNames(doc.Jobs))
	if err != nil {
		return r, err
	}
	serverIDs, err := im.servers(ctx, doc.MCPServers)
	if err != nil {
		return r, err
	}
	if err := im.jobs(ctx, doc.Jobs, modelIDs, secretIDs, serverIDs); err != nil {
		return r, err
	}
	if err := im.settings(ctx, doc.Settings, modelIDs); err != nil {
		return r, err
	}
	return r, nil
}

type importer struct {
	c    *Client
	opts ImportOptions
	r    *Report
}

// create sends a create request unless this is a dry run, returning the new ID
func (im importer) create(ctx context.Context, path string, body any) (string, error) {
	if im.opts.DryRun {
		return "dry-run", nil
	}
	var out struct {
		ID string `json:"id"`
	}
	err := im.c.do(ctx, http.MethodPost, path, body, &out)
	return out.ID, err
}

func (im importer) put(ctx context.Context, path string, body any) error {
	if im.opts.DryRun {
		return nil
	}
	return im.c.do(ctx, http.MethodPut, path, body, nil)
}

func (im importer) patch(ctx context.Context, path string, body any) error {
	if im.opts.DryRun {
		return nil
	}
	return im.c.do(ctx, http.MethodPatch, path, body, nil)
}

func refKey(provider, model string) string {
	return provider + "\x00" + model
}

func (im importer) providers(ctx context.Context, providers []Provider) (map[string]string, error) {
	existing, err := listAll[apiProvider](ctx, im.c, "/api/providers")
	if err != nil {
		return nil, err
	}
	models, err := listAll[apiModel](ctx, im.c, "/api/models")
	if err != nil {
		return nil, err
	}
	providerIDs := map[string]string{}
	providerNames := map[string]string{}
	for _, p := range existing {
		providerIDs[p.Name] = p.ID
		providerNames[p.ID] = p.Name
	}
	modelIDs := map[string]string{}
	for _, m := range models {
		if name, ok := providerNames[m.ProviderID]; ok {
			modelIDs[refKey(name, m.Model)] = m.ID
		}
	}

	for _, p := range providers {
		id, ok := providerIDs[p.Name]
		if !ok {
			body := map[string]any{"name": p.Name, "kind": p.Kind}
			if p.BaseURL != "" {
				body["baseUrl"] = p.BaseURL
			}
			id, err = im.create(ctx, "/api/providers", body)
			if err != nil {
				return nil, fmt.Errorf("failed to create provider %s: %w", p.Name, err)
			}
			providerIDs[p.Name] = id
			im.r.Created = append(im.r.Created, "provider "+p.Name)
			if p.Kind != "fake" {
				im.r.Todo = append(im.r.Todo, fmt.Sprintf("Set the API key of provider %s in Settings → Providers & models", p.Name))
			}

			// A new provider lists its catalog or server models right away, and those are reused instead of created twice
			if !im.opts.DryRun {
				synced, err := listAll[apiModel](ctx, im.c, "/api/models?provider="+url.QueryEscape(id))
				if err != nil {
					return nil, err
				}
				for _, m := range synced {
					modelIDs[refKey(p.Name, m.Model)] = m.ID
				}
			}
		}
		for _, m := range p.Models {
			mid, ok := modelIDs[refKey(p.Name, m.Model)]
			if !ok {
				body := map[string]any{"providerId": id, "model": m.Model, "price": m.Price, "caps": m.Caps}
				if m.Label != "" {
					body["label"] = m.Label
				}
				mid, err = im.create(ctx, "/api/models", body)
				if err != nil {
					return nil, fmt.Errorf("failed to create model %s of %s: %w", m.Model, p.Name, err)
				}
				modelIDs[refKey(p.Name, m.Model)] = mid
				im.r.Created = append(im.r.Created, fmt.Sprintf("model %s/%s", p.Name, m.Model))
			}

			// A model that is a default of this instance can't be turned off, which is left for the admin rather than failing the import
			if m.Disabled {
				err = im.patch(ctx, "/api/models/"+esc(mid), map[string]any{"enabled": false})
				if err != nil {
					im.r.Todo = append(im.r.Todo, fmt.Sprintf("Turn off model %s of provider %s in Settings → Providers & models, the import could not: %v", m.Model, p.Name, err))
				}
			}
		}
	}
	return modelIDs, nil
}

// jobSecretNames are the secrets jobs map into their environment, which a later import can still attach
func jobSecretNames(jobs []Job) map[string]bool {
	used := map[string]bool{}
	for _, j := range jobs {
		for _, s := range j.Secrets {
			used[s.Secret] = true
		}
	}
	return used
}

func (im importer) secrets(ctx context.Context, names []string, usedByJobs map[string]bool) (map[string]string, error) {
	existing, err := listAll[apiSecret](ctx, im.c, "/api/secrets")
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for _, s := range existing {
		ids[s.Name] = s.ID
	}
	for _, name := range names {
		if _, ok := ids[name]; ok {
			continue
		}
		value, ok := im.opts.SecretValues[name]
		if !ok {
			todo := fmt.Sprintf("Create secret %s in Settings → Secrets", name)
			if usedByJobs[name] {
				todo += ", then import again to attach it to its jobs"
			}
			im.r.Todo = append(im.r.Todo, todo)
			continue
		}
		id, err := im.create(ctx, "/api/secrets", map[string]string{"name": name, "value": value})
		if err != nil {
			return nil, fmt.Errorf("failed to create secret %s: %w", name, err)
		}
		ids[name] = id
		im.r.Created = append(im.r.Created, "secret "+name)
	}
	return ids, nil
}

func (im importer) servers(ctx context.Context, servers []MCPServer) (map[string]string, error) {
	existing, err := listAll[apiServer](ctx, im.c, "/api/mcp-servers")
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for _, s := range existing {
		ids[s.Name] = s.ID
	}
	for _, s := range servers {
		if _, ok := ids[s.Name]; ok {
			im.r.Skipped = append(im.r.Skipped, "MCP server "+s.Name+" (exists)")
			continue
		}
		body := map[string]any{"name": s.Name, "transport": s.Transport, "enabled": s.Enabled}
		for key, value := range map[string]any{"description": s.Description, "command": s.Command, "url": s.URL} {
			if value != "" {
				body[key] = value
			}
		}
		if len(s.Args) > 0 {
			body["args"] = s.Args
		}
		if len(s.Env) > 0 {
			body["env"] = s.Env
		}
		if len(s.Headers) > 0 {
			body["headers"] = s.Headers
		}
		if s.OAuth != nil {
			body["oauth"] = s.OAuth
		}
		id, err := im.create(ctx, "/api/mcp-servers", body)
		if err != nil {
			return nil, fmt.Errorf("failed to create MCP server %s: %w", s.Name, err)
		}
		ids[s.Name] = id
		im.r.Created = append(im.r.Created, "MCP server "+s.Name)
		for _, key := range s.Redacted {
			im.r.Todo = append(im.r.Todo, fmt.Sprintf("Fill in %s of MCP server %s, which the export left out", key, s.Name))
		}
		if s.LoggedIn {
			im.r.Todo = append(im.r.Todo, fmt.Sprintf("Log in to MCP server %s under MCP servers, since exports leave out OAuth logins", s.Name))
		}
	}
	return ids, nil
}

func (im importer) jobs(ctx context.Context, jobs []Job, modelIDs, secretIDs, serverIDs map[string]string) error {
	existing, err := listAll[apiJobItem](ctx, im.c, "/api/jobs")
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, j := range existing {
		names[j.Name] = j.ID
	}

	for _, job := range jobs {
		var name string
		_ = json.Unmarshal(job.Fields["name"], &name)
		id, exists := names[name]
		if !exists {
			// Only known fields are sent, since the create endpoint rejects the rest and older backups carry fields that were removed since
			body := map[string]json.RawMessage{}
			for _, key := range jobFieldKeys {
				if v, ok := job.Fields[key]; ok {
					body[key] = v
				}
			}

			// Older backups pin a run mode instead, and only an Assisted pin kept the job from graduating
			if _, ok := body["graduate"]; !ok && string(job.Fields["modePin"]) == `"assisted"` {
				body["graduate"] = json.RawMessage("false")
			}
			if job.Model != nil {
				if mid, ok := modelIDs[refKey(job.Model.Provider, job.Model.Model)]; ok {
					raw, _ := json.Marshal(mid)
					body["modelId"] = raw
				}
			}
			id, err = im.create(ctx, "/api/jobs", body)
			if err != nil {
				return fmt.Errorf("failed to create job %s: %w", name, err)
			}
			im.r.Created = append(im.r.Created, "job "+name)
			if err := im.playbook(ctx, id, job); err != nil {
				return fmt.Errorf("failed to restore the playbook of job %s: %w", name, err)
			}
		} else {
			im.r.Skipped = append(im.r.Skipped, "job "+name+" (exists, only its secrets and MCP servers are filled in)")
		}

		// Attachments are filled in on every import, so secrets created after a first import get attached by the next one
		// The endpoints replace the whole list, so what the job already has is sent along and never dropped
		if err := im.attachSecrets(ctx, id, name, job.Secrets, secretIDs); err != nil {
			return err
		}
		if err := im.attachServers(ctx, id, name, job.MCPServers, serverIDs); err != nil {
			return err
		}
	}
	return nil
}

// attachSecrets adds the export's secret mappings the job doesn't have yet, keeping the ones it has
func (im importer) attachSecrets(ctx context.Context, jobID, name string, wanted []JobSecret, secretIDs map[string]string) error {
	var current []apiJobSecret
	if !im.opts.DryRun {
		if err := im.c.do(ctx, http.MethodGet, "/api/jobs/"+esc(jobID)+"/secrets", nil, &current); err != nil {
			return fmt.Errorf("failed to read the secrets of job %s: %w", name, err)
		}
	}
	secrets := make([]map[string]string, 0, len(current)+len(wanted))
	envs := map[string]bool{}
	for _, s := range current {
		secrets = append(secrets, map[string]string{"envName": s.EnvName, "secretId": s.SecretID})
		envs[s.EnvName] = true
	}
	added := false
	for _, s := range wanted {
		if sid, ok := secretIDs[s.Secret]; ok && !envs[s.Env] {
			secrets = append(secrets, map[string]string{"envName": s.Env, "secretId": sid})
			envs[s.Env] = true
			added = true
		}
	}
	if !added {
		return nil
	}
	if err := im.put(ctx, "/api/jobs/"+esc(jobID)+"/secrets", secrets); err != nil {
		return fmt.Errorf("failed to attach secrets to job %s: %w", name, err)
	}
	return nil
}

// attachServers adds the export's MCP servers the job doesn't have yet, keeping the ones it has and their tool lists
func (im importer) attachServers(ctx context.Context, jobID, name string, wanted []JobServer, serverIDs map[string]string) error {
	var current []apiJobServer
	if !im.opts.DryRun {
		if err := im.c.do(ctx, http.MethodGet, "/api/jobs/"+esc(jobID)+"/mcp-servers", nil, &current); err != nil {
			return fmt.Errorf("failed to read the MCP servers of job %s: %w", name, err)
		}
	}
	servers := make([]map[string]any, 0, len(current)+len(wanted))
	attached := map[string]bool{}
	for _, s := range current {
		servers = append(servers, map[string]any{"serverId": s.ServerID, "allowedTools": s.AllowedTools})
		attached[s.ServerID] = true
	}
	added := false
	for _, s := range wanted {
		if sid, ok := serverIDs[s.Server]; ok && !attached[sid] {
			servers = append(servers, map[string]any{"serverId": sid, "allowedTools": s.AllowedTools})
			attached[sid] = true
			added = true
		}
	}
	if !added {
		return nil
	}
	if err := im.put(ctx, "/api/jobs/"+esc(jobID)+"/mcp-servers", servers); err != nil {
		return fmt.Errorf("failed to attach MCP servers to job %s: %w", name, err)
	}
	return nil
}

// playbook restores a new job's playbook, replaying its history when the export has it
func (im importer) playbook(ctx context.Context, jobID string, job Job) error {
	versions := job.History
	if len(versions) == 0 && len(job.Playbook) > 0 {
		versions = []Version{{Content: job.Playbook, Summary: "Imported"}}
	}
	slices.SortStableFunc(versions, func(a, b Version) int { return cmp.Compare(a.Version, b.Version) })
	for _, v := range versions {
		summary := v.Summary
		if summary == "" {
			summary = "Imported"
		}
		err := im.put(ctx, "/api/jobs/"+esc(jobID)+"/playbook", map[string]any{"content": v.Content, "summary": summary})
		if apiErr, ok := errors.AsType[*APIError](err); ok && apiErr.Status == http.StatusNotFound {
			return errors.New("the job disappeared during the import")
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (im importer) settings(ctx context.Context, s Settings, modelIDs map[string]string) error {
	body := map[string]any{}
	for key, ref := range map[string]*ModelRef{"agentModelId": s.AgentModel, "utilityModelId": s.UtilityModel, "reflectionModelId": s.ReflectionModel} {
		if ref == nil {
			continue
		}
		if id, ok := modelIDs[refKey(ref.Provider, ref.Model)]; ok {
			body[key] = id
		}
	}
	if s.DefaultImage != "" {
		body["defaultImage"] = s.DefaultImage
	}
	if len(s.DefaultLimits) > 0 {
		body["defaultLimits"] = s.DefaultLimits
	}
	if s.DailySpendLimitUSD != nil {
		body["dailySpendLimitUsd"] = *s.DailySpendLimitUSD
	}
	if s.RetentionDays != nil {
		body["retentionDays"] = *s.RetentionDays
	}
	if len(s.NotifyOn) > 0 {
		body["notifyOn"] = s.NotifyOn
	}
	if s.NotifySecret != "" {
		body["notifySecret"] = s.NotifySecret
	}
	if s.UsageUnit != "" {
		body["usageUnit"] = s.UsageUnit
	}
	if s.HadNotifyWebhook {
		im.r.Todo = append(im.r.Todo, "Set the notification webhook URL in Settings, which exports leave out")
	}
	if len(body) == 0 || im.opts.DryRun {
		return nil
	}
	if err := im.c.do(ctx, http.MethodPatch, "/api/settings", body, nil); err != nil {
		return fmt.Errorf("failed to restore the settings: %w", err)
	}
	im.r.Created = append(im.r.Created, "settings")
	return nil
}
