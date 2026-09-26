package jobs

import (
	"encoding/json"
	"fmt"

	"github.com/stonith404/umpteenth/backend/internal/jobs/jobsdb"
)

// Job concurrency policies
const (
	ConcurrencySkip     = "skip"
	ConcurrencyQueue    = "queue"
	ConcurrencyParallel = "parallel"
)

// Run modes
const (
	ModeExplore  = "explore"
	ModeAssisted = "assisted"
	ModeScripted = "scripted"
)

// Schedule is the compiled schedule of a job
type Schedule struct {
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"`
	Human    string `json:"human"`
}

// IOField describes one job input or output
type IOField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// MCPNeed is an MCP server the job needs, with the reason
type MCPNeed struct {
	Server string `json:"server"`
	Why    string `json:"why"`
}

// Spec is the structured data compiled from a job's instruction
type Spec struct {
	Title string `json:"title"`
	Goal  string `json:"goal"`
	// Schedule is omitted for jobs without a recurring schedule, since Huma cannot mark a struct reference as nullable
	Schedule        *Schedule `json:"schedule,omitempty"`
	SuccessCriteria []string  `json:"successCriteria"`
	Inputs          []IOField `json:"inputs"`
	Outputs         []IOField `json:"outputs"`
	MCP             []MCPNeed `json:"mcp"`
	Network         string    `json:"network" enum:"none,internet"`
	Dockerfile      *string   `json:"dockerfile"`
	SideEffects     []string  `json:"sideEffects"`
}

// LimitOverrides are the per-job limits; unset fields fall back to the workspace defaults
type LimitOverrides struct {
	TimeoutSeconds *int     `json:"timeoutSeconds,omitempty" minimum:"30" maximum:"86400"`
	MaxTurns       *int     `json:"maxTurns,omitempty" minimum:"1" maximum:"1000"`
	MaxCostUSD     *float64 `json:"maxCostUsd,omitempty" minimum:"0"`
	CPUs           *float64 `json:"cpus,omitempty" minimum:"0.1" maximum:"64"`
	MemoryMB       *int     `json:"memoryMb,omitempty" minimum:"64" maximum:"262144"`
}

// LastRun summarizes the latest run of a job
type LastRun struct {
	ID       string `json:"id"`
	Number   int64  `json:"number"`
	Status   string `json:"status"`
	QueuedAt int64  `json:"queuedAt"`
}

// JobDto is a job as returned by the API
type JobDto struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	Instruction         string         `json:"instruction"`
	Spec                Spec           `json:"spec"`
	ModelID             *string        `json:"modelId"`
	Image               *string        `json:"image"`
	Network             string         `json:"network" enum:"none,internet,allowlist,unrestricted"`
	AllowedDomains      []string       `json:"allowedDomains"`
	AllowPrivateNetwork bool           `json:"allowPrivateNetwork"`
	RunAsRoot           bool           `json:"runAsRoot"`
	Limits              LimitOverrides `json:"limits"`
	SelfImprove         bool           `json:"selfImprove"`
	Graduate            bool           `json:"graduate" doc:"Whether the job may graduate to a main script that runs without the agent"`
	Concurrency         string         `json:"concurrency"`
	Cron                *string        `json:"cron"`
	Timezone            *string        `json:"timezone"`
	NextRunAt           *int64         `json:"nextRunAt"`
	PlaybookVersion     int64          `json:"playbookVersion"`
	HasWebhookToken     bool           `json:"hasWebhookToken"`
	CreatedAt           int64          `json:"createdAt"`
	UpdatedAt           int64          `json:"updatedAt"`
	LastRun             *LastRun       `json:"lastRun"`
	// NextMode is the mode the next run starts in, and Demoted tells whether repeated fallbacks took a graduated job back to Assisted
	NextMode         string `json:"nextMode,omitempty" enum:"explore,assisted,scripted"`
	Demoted          bool   `json:"demoted"`
	GraduatedVersion *int64 `json:"graduatedVersion" doc:"The playbook version the job last graduated to Scripted mode at"`
}

func toDto(j jobsdb.Job) (JobDto, error) {
	d := JobDto{
		ID:                  j.ID,
		Name:                j.Name,
		Instruction:         j.Instruction,
		ModelID:             j.ModelID,
		Image:               j.Image,
		Network:             j.Network,
		AllowPrivateNetwork: j.AllowPrivateNetwork,
		RunAsRoot:           j.RunAsRoot,
		SelfImprove:         j.SelfImprove,
		Graduate:            j.Graduate,
		Concurrency:         j.Concurrency,
		Cron:                j.Cron,
		Timezone:            j.Timezone,
		NextRunAt:           j.NextRunAt,
		GraduatedVersion:    j.GraduatedVersion,
		PlaybookVersion:     j.PlaybookVersion,
		HasWebhookToken:     j.WebhookTokenHash != nil,
		CreatedAt:           j.CreatedAt,
		UpdatedAt:           j.UpdatedAt,
	}

	// Only this package writes the JSON columns, so one that doesn't parse is damaged, and guessing could run a job without its limits
	for _, c := range []struct {
		name string
		raw  string
		dst  any
	}{{"spec", j.Spec, &d.Spec}, {"limits", j.Limits, &d.Limits}, {"allowed domains", j.AllowedDomains, &d.AllowedDomains}} {
		err := json.Unmarshal([]byte(c.raw), c.dst)
		if err != nil {
			return JobDto{}, fmt.Errorf("job %s has invalid %s: %w", j.ID, c.name, err)
		}
	}
	if d.AllowedDomains == nil {
		d.AllowedDomains = []string{}
	}
	normalizeSpec(&d.Spec)
	return d, nil
}

// normalizeSpec replaces nil slices so API clients never see null lists
func normalizeSpec(s *Spec) {
	if s.SuccessCriteria == nil {
		s.SuccessCriteria = []string{}
	}
	if s.Inputs == nil {
		s.Inputs = []IOField{}
	}
	if s.Outputs == nil {
		s.Outputs = []IOField{}
	}
	if s.MCP == nil {
		s.MCP = []MCPNeed{}
	}
	if s.SideEffects == nil {
		s.SideEffects = []string{}
	}
	if s.Network == "" {
		s.Network = "internet"
	}
}
