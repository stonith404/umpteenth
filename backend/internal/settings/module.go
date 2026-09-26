// Package settings stores per-workspace settings, with env config providing the defaults for new workspaces
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/settings/settingsdb"
)

// Limits are the per-run resource and budget limits
type Limits struct {
	TimeoutSeconds int     `json:"timeoutSeconds" minimum:"30" maximum:"86400"`
	MaxTurns       int     `json:"maxTurns" minimum:"1" maximum:"1000"`
	MaxCostUSD     float64 `json:"maxCostUsd" minimum:"0"`
	CPUs           float64 `json:"cpus" minimum:"0.1" maximum:"64"`
	MemoryMB       int     `json:"memoryMb" minimum:"64" maximum:"262144"`
}

// WorkspaceSettings are the settings of one workspace, always fully populated with defaults
type WorkspaceSettings struct {
	AgentModelID      *string `json:"agentModelId"`
	UtilityModelID    *string `json:"utilityModelId"`
	ReflectionModelID *string `json:"reflectionModelId"`
	DefaultImage      string  `json:"defaultImage"`
	DefaultLimits     Limits  `json:"defaultLimits"`
	DailySpendLimit   float64 `json:"dailySpendLimitUsd" doc:"0 means no limit"`
	RetentionDays     int     `json:"retentionDays" minimum:"1"`
	// Notifications go to a webhook for the events listed in NotifyOn, signed with a workspace secret when NotifySecret names one
	NotifyWebhookURL *string  `json:"notifyWebhookUrl"`
	NotifyOn         []string `json:"notifyOn" enum:"run.failed,run.fell_back,job.demoted"`
	NotifySecret     *string  `json:"notifySecret" doc:"Name of a workspace secret whose value signs every delivery"`
	// The UI shows usage in one unit only, while spend limits and model prices stay in US dollars either way
	UsageUnit string `json:"usageUnit" enum:"price,tokens" doc:"Whether the UI shows usage as a price in US dollars or as input and output tokens, while spend limits and model prices stay in US dollars"`
}

// Units the UI can show usage in
const (
	UsagePrice  = "price"
	UsageTokens = "tokens"
)

// Notification events
const (
	EventRunFailed   = "run.failed"
	EventRunFellBack = "run.fell_back"
	EventJobDemoted  = "job.demoted"
)

// URLChecker validates outbound URLs against the egress guard
type URLChecker interface {
	CheckURL(ctx context.Context, field, rawURL string) error
}

// Defaults are the instance-wide defaults taken from the environment
type Defaults struct {
	Image         string
	RetentionDays int
}

type Dependencies struct {
	DB       *database.DB
	Defaults Defaults
	// URLs checks the notification webhook URL against the egress guard
	URLs URLChecker
}

// ModelChecker confirms a model belongs to the workspace, so settings never point at a missing or foreign model
type ModelChecker interface {
	ModelExists(ctx context.Context, workspaceID, modelID string) (bool, error)
}

type Module struct {
	db       *database.DB
	queries  *settingsdb.Queries
	defaults Defaults
	models   ModelChecker
	urls     URLChecker
}

// SetModels wires the model checker, which is built after this module
func (m *Module) SetModels(c ModelChecker) { m.models = c }

func New(deps Dependencies) *Module {
	return &Module{db: deps.DB, queries: settingsdb.New(deps.DB), defaults: deps.Defaults, urls: deps.URLs}
}

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("get-settings", http.MethodGet, "/api/settings", "Settings"), auth, m.get)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("update-settings", http.MethodPatch, "/api/settings", "Settings"), httpserver.Access{MinRole: principal.RoleAdmin}), auth, m.update)
}

// DefaultLimits are used when neither the workspace nor the job sets a limit
func DefaultLimits() Limits {
	return Limits{TimeoutSeconds: 15 * 60, MaxTurns: 60, MaxCostUSD: 2, CPUs: 1, MemoryMB: 1024}
}

// Get returns the workspace settings merged over the defaults
// Settings are read from the database on every call, never cached per replica
func (m *Module) Get(ctx context.Context, workspaceID string) (WorkspaceSettings, error) {
	s := WorkspaceSettings{
		DefaultImage:  m.defaults.Image,
		DefaultLimits: DefaultLimits(),
		RetentionDays: m.defaults.RetentionDays,
		NotifyOn:      []string{EventRunFailed, EventJobDemoted},
		UsageUnit:     UsagePrice,
	}

	rows, err := m.queries.ListSettings(ctx, workspaceID)
	if err != nil {
		return s, fmt.Errorf("failed to load settings: %w", err)
	}

	// Each key holds the JSON of one field, so partial updates never clobber unrelated settings
	fields := map[string]any{
		"agentModelId":       &s.AgentModelID,
		"utilityModelId":     &s.UtilityModelID,
		"reflectionModelId":  &s.ReflectionModelID,
		"defaultImage":       &s.DefaultImage,
		"defaultLimits":      &s.DefaultLimits,
		"dailySpendLimitUsd": &s.DailySpendLimit,
		"retentionDays":      &s.RetentionDays,
		"notifyWebhookUrl":   &s.NotifyWebhookURL,
		"notifyOn":           &s.NotifyOn,
		"notifySecret":       &s.NotifySecret,
		"usageUnit":          &s.UsageUnit,
	}
	for _, row := range rows {
		// A value that doesn't fit its field keeps the default instead of failing every read of the settings
		if target, ok := fields[row.Key]; ok {
			_ = json.Unmarshal([]byte(row.Value), target)
		}
	}

	// A unit the UI doesn't know would leave every usage figure blank, so it reads as the default
	if s.UsageUnit != UsageTokens {
		s.UsageUnit = UsagePrice
	}
	return s, nil
}

// UsageUnit returns the unit the workspace's UI shows usage in, for the session that loads it on every page
func (m *Module) UsageUnit(ctx context.Context, workspaceID string) (string, error) {
	s, err := m.Get(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	return s.UsageUnit, nil
}

// Set stores one setting value
func (m *Module) Set(ctx context.Context, workspaceID, key string, value any) error {
	return upsert(ctx, m.queries, workspaceID, key, value)
}

func upsert(ctx context.Context, q *settingsdb.Queries, workspaceID, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return q.UpsertSetting(ctx, settingsdb.UpsertSettingParams{WorkspaceID: workspaceID, Key: key, Value: string(raw)})
}

type settingsOutput struct {
	Body WorkspaceSettings
}

func (m *Module) get(ctx context.Context, _ *struct{}) (*settingsOutput, error) {
	s, err := m.Get(ctx, principal.WorkspaceID(ctx))
	if err != nil {
		return nil, err
	}

	// A chat webhook URL is the credential to post into its channel, so only admins, who may change it, read it whole
	if s.NotifyWebhookURL != nil && !principal.RoleOf(ctx).AtLeast(principal.RoleAdmin) {
		s.NotifyWebhookURL = new(webhookOrigin(*s.NotifyWebhookURL))
	}
	return &settingsOutput{Body: s}, nil
}

// webhookOrigin shows where a webhook URL points without its path and query, which usually carry its secret
func webhookOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "…"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

type updateInput struct {
	Body struct {
		AgentModelID      *string  `json:"agentModelId,omitempty"`
		UtilityModelID    *string  `json:"utilityModelId,omitempty"`
		ReflectionModelID *string  `json:"reflectionModelId,omitempty"`
		DefaultImage      *string  `json:"defaultImage,omitempty" maxLength:"500" doc:"An empty value resets to the server default"`
		DefaultLimits     *Limits  `json:"defaultLimits,omitempty"`
		DailySpendLimit   *float64 `json:"dailySpendLimitUsd,omitempty" minimum:"0"`
		RetentionDays     *int     `json:"retentionDays,omitempty" minimum:"1" maximum:"3650"`
		NotifyWebhookURL  *string  `json:"notifyWebhookUrl,omitempty" maxLength:"2000" doc:"An empty value turns notifications off"`
		NotifyOn          []string `json:"notifyOn,omitempty" enum:"run.failed,run.fell_back,job.demoted" uniqueItems:"true"`
		NotifySecret      *string  `json:"notifySecret,omitempty" maxLength:"100" doc:"An empty value sends deliveries unsigned"`
		UsageUnit         *string  `json:"usageUnit,omitempty" enum:"price,tokens"`
	}
}

func (m *Module) update(ctx context.Context, in *updateInput) (*settingsOutput, error) {
	wid := principal.WorkspaceID(ctx)
	updates := map[string]any{}
	b := in.Body

	// Model settings must name an enabled model of this workspace, or every run would fail later
	// An unchanged value is not checked again, so the form still saves when its provider stopped listing the model
	current, err := m.Get(ctx, wid)
	if err != nil {
		return nil, err
	}
	stored := map[string]*string{"agentModelId": current.AgentModelID, "utilityModelId": current.UtilityModelID, "reflectionModelId": current.ReflectionModelID}
	for field, id := range map[string]*string{"agentModelId": b.AgentModelID, "utilityModelId": b.UtilityModelID, "reflectionModelId": b.ReflectionModelID} {
		if id == nil || *id == "" || (stored[field] != nil && *stored[field] == *id) {
			continue
		}
		ok, err := m.models.ModelExists(ctx, wid, *id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, apperror.InvalidField(field, "not_found", "is not an enabled model of this workspace")
		}
	}

	if b.AgentModelID != nil {
		updates["agentModelId"] = emptyToNil(*b.AgentModelID)
	}
	if b.UtilityModelID != nil {
		updates["utilityModelId"] = emptyToNil(*b.UtilityModelID)
	}
	if b.ReflectionModelID != nil {
		updates["reflectionModelId"] = emptyToNil(*b.ReflectionModelID)
	}
	resetImage := false
	if b.DefaultImage != nil {
		if image := strings.TrimSpace(*b.DefaultImage); image != "" {
			updates["defaultImage"] = image
		} else {
			resetImage = true
		}
	}
	if b.DefaultLimits != nil {
		updates["defaultLimits"] = *b.DefaultLimits
	}
	if b.DailySpendLimit != nil {
		updates["dailySpendLimitUsd"] = *b.DailySpendLimit
	}
	if b.RetentionDays != nil {
		updates["retentionDays"] = *b.RetentionDays
	}

	// The webhook must be a URL the egress guard lets Umpteenth call, or every delivery would fail later
	if b.NotifyWebhookURL != nil {
		url := strings.TrimSpace(*b.NotifyWebhookURL)
		if url != "" {
			if err := m.urls.CheckURL(ctx, "notifyWebhookUrl", url); err != nil {
				return nil, err
			}
		}
		updates["notifyWebhookUrl"] = emptyToNil(url)
	}
	if b.NotifyOn != nil {
		updates["notifyOn"] = b.NotifyOn
	}
	if b.NotifySecret != nil {
		updates["notifySecret"] = emptyToNil(strings.TrimSpace(*b.NotifySecret))
	}
	if b.UsageUnit != nil {
		updates["usageUnit"] = *b.UsageUnit
	}

	// The fields are saved together, so a failed save never leaves the form half applied
	err = m.db.InTx(ctx, func(tx *database.Tx) error {
		q := settingsdb.New(tx)
		for key, value := range updates {
			if err := upsert(ctx, q, wid, key, value); err != nil {
				return fmt.Errorf("failed to save setting %s: %w", key, err)
			}
		}
		if resetImage {
			if err := q.DeleteSetting(ctx, settingsdb.DeleteSettingParams{WorkspaceID: wid, Key: "defaultImage"}); err != nil {
				return fmt.Errorf("failed to reset the default image: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return m.get(ctx, nil)
}

func emptyToNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
