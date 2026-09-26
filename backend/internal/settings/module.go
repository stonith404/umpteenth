// Package settings stores per-workspace settings, with env config providing the defaults for new workspaces
package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	PidsLimit      int     `json:"pidsLimit" minimum:"16" maximum:"65536"`
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
}

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
	Image           string
	DailySpendLimit float64
	RetentionDays   int
}

type Dependencies struct {
	DB       *database.DB
	Defaults Defaults
}

// ModelChecker confirms a model belongs to the workspace, so settings never point at a missing or foreign model
type ModelChecker interface {
	ModelExists(ctx context.Context, workspaceID, modelID string) (bool, error)
}

type Module struct {
	queries  *settingsdb.Queries
	defaults Defaults
	models   ModelChecker
	urls     URLChecker
}

// SetURLChecker wires the egress guard, which checks the notification webhook URL
func (m *Module) SetURLChecker(c URLChecker) { m.urls = c }

// SetModels wires the model checker, which is built after this module
func (m *Module) SetModels(c ModelChecker) { m.models = c }

func New(deps Dependencies) *Module {
	return &Module{queries: settingsdb.New(deps.DB), defaults: deps.Defaults}
}

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	httpserver.Register(api, httpserver.Operation("get-settings", http.MethodGet, "/api/settings", "Settings"), auth, m.get)
	httpserver.Register(api, httpserver.Operation("update-settings", http.MethodPatch, "/api/settings", "Settings"), auth, m.update)
}

// DefaultLimits are used when neither the workspace nor the job sets a limit
func DefaultLimits() Limits {
	return Limits{TimeoutSeconds: 15 * 60, MaxTurns: 60, MaxCostUSD: 2, CPUs: 1, MemoryMB: 1024, PidsLimit: 256}
}

// Get returns the workspace settings merged over the defaults
// Settings are read from the database on every call, never cached per replica (PLAN.md §3.4)
func (m *Module) Get(ctx context.Context, workspaceID string) (WorkspaceSettings, error) {
	s := WorkspaceSettings{
		DefaultImage:    m.defaults.Image,
		DefaultLimits:   DefaultLimits(),
		DailySpendLimit: m.defaults.DailySpendLimit,
		RetentionDays:   m.defaults.RetentionDays,
		NotifyOn:        []string{EventRunFailed, EventJobDemoted},
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
	}
	for _, row := range rows {
		if target, ok := fields[row.Key]; ok {
			_ = json.Unmarshal([]byte(row.Value), target)
		}
	}
	return s, nil
}

// Set stores one setting value
func (m *Module) Set(ctx context.Context, workspaceID, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return m.queries.UpsertSetting(ctx, settingsdb.UpsertSettingParams{WorkspaceID: workspaceID, Key: key, Value: string(raw)})
}

type settingsOutput struct {
	Body WorkspaceSettings
}

func (m *Module) get(ctx context.Context, _ *struct{}) (*settingsOutput, error) {
	s, err := m.Get(ctx, principal.WorkspaceID(ctx))
	if err != nil {
		return nil, err
	}
	return &settingsOutput{Body: s}, nil
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
		if id == nil || *id == "" || m.models == nil || (stored[field] != nil && *stored[field] == *id) {
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
		if url != "" && m.urls != nil {
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

	for key, value := range updates {
		if err := m.Set(ctx, wid, key, value); err != nil {
			return nil, fmt.Errorf("failed to save setting %s: %w", key, err)
		}
	}
	if resetImage {
		err := m.queries.DeleteSetting(ctx, settingsdb.DeleteSettingParams{WorkspaceID: wid, Key: "defaultImage"})
		if err != nil {
			return nil, fmt.Errorf("failed to reset the default image: %w", err)
		}
	}
	return m.get(ctx, nil)
}

func emptyToNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
