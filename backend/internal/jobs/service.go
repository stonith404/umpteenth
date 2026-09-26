package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/jobs/jobsdb"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/settings"
)

// Trigger starts a run of the job through its actor, which applies the concurrency policy
func (m *Module) Trigger(ctx context.Context, workspaceID, jobID string, req runs.TriggerRequest) (runs.TriggerResult, error) {
	err := m.JobExists(ctx, workspaceID, jobID)
	if err != nil {
		return runs.TriggerResult{}, err
	}
	var res runs.TriggerResult
	err = m.invoke(ctx, jobID, methodTrigger, triggerInput{WorkspaceID: workspaceID, Request: req}, &res)
	return res, err
}

// RunFinished implements runner.Notifier and releases the job's concurrency slot
func (m *Module) RunFinished(ctx context.Context, run runner.Run, _ runner.Final) {
	// A lost notification only delays the next queued run until the job's next turn repairs it, so it is logged rather than returned
	err := m.invoke(ctx, run.JobID, methodRunFinished, runFinishedInput{RunID: run.ID}, nil)
	if err != nil {
		slog.WarnContext(ctx, "Failed to tell the job that its run finished", slog.String("job", run.JobID), slog.String("run", run.ID), slog.Any("error", err))
	}
}

// Reschedule re-arms the job's schedule alarm after a change
func (m *Module) Reschedule(ctx context.Context, jobID string) error {
	return m.invoke(ctx, jobID, methodReschedule, nil, nil)
}

// JobExists checks that a live job exists in the workspace
func (m *Module) JobExists(ctx context.Context, workspaceID, jobID string) error {
	_, err := m.getJob(ctx, workspaceID, jobID)
	return err
}

func (m *Module) getJob(ctx context.Context, workspaceID, jobID string) (jobsdb.Job, error) {
	job, err := m.queries.GetJob(ctx, jobsdb.GetJobParams{WorkspaceID: workspaceID, ID: jobID})
	if database.IsNotFound(err) {
		return jobsdb.Job{}, apperror.NotFound("Job")
	} else if err != nil {
		return jobsdb.Job{}, fmt.Errorf("failed to load job: %w", err)
	}
	return job, nil
}

// newRun prepares a run row for a trigger, deciding the mode from the current playbook
func (m *Module) newRun(ctx context.Context, job jobsdb.Job, req runs.TriggerRequest) (runs.NewRun, error) {
	number, err := m.queries.NextRunNumber(ctx, jobsdb.NextRunNumberParams{WorkspaceID: job.WorkspaceID, ID: job.ID})
	if err != nil {
		return runs.NewRun{}, fmt.Errorf("failed to allocate run number: %w", err)
	}

	mode, _, err := m.nextMode(ctx, job)
	if err != nil {
		return runs.NewRun{}, err
	}

	return runs.NewRun{
		WorkspaceID:     job.WorkspaceID,
		JobID:           job.ID,
		Number:          number,
		Mode:            mode,
		Trigger:         req.Trigger,
		TriggeredBy:     req.TriggeredBy,
		Input:           req.Input,
		Instructions:    req.Instructions,
		PlaybookVersion: job.PlaybookVersion,
		ModelID:         job.ModelID,
		Status:          runner.StatusQueued,
	}, nil
}

// nextMode derives the mode of the job's next run from its playbook and history (PLAN.md §10.1)
// Nothing learned yet explores, know-how assists, and a main script runs without the agent unless repeated fallbacks demoted the job
func (m *Module) nextMode(ctx context.Context, job jobsdb.Job) (mode string, demoted bool, err error) {
	content, err := m.deps.Playbooks.Content(ctx, job.ID, job.PlaybookVersion)
	if err != nil {
		return "", false, fmt.Errorf("failed to load the playbook: %w", err)
	}
	pin := ""
	if job.ModePin != nil {
		pin = *job.ModePin
	}
	if content.Main != nil {
		demoted, err = m.demoted(ctx, job)
		if err != nil {
			return "", false, err
		}
	}

	switch {
	case content.IsEmpty() || pin == ModeExplore:
		return ModeExplore, demoted, nil
	case content.Main == nil || pin == ModeAssisted:
		return ModeAssisted, demoted, nil
	case pin == ModeScripted || !demoted:
		return ModeScripted, demoted, nil
	}
	return ModeAssisted, demoted, nil
}

// demoted reports whether the last two scripted runs since the job graduated both fell back to the agent (PLAN.md §10.4)
// The job stays demoted until reflection graduates it again, which moves the graduated version past these runs
func (m *Module) demoted(ctx context.Context, job jobsdb.Job) (bool, error) {
	since := int64(0)
	if job.GraduatedVersion != nil {
		since = *job.GraduatedVersion
	}
	fellBack, err := m.queries.RecentScriptedRuns(ctx, jobsdb.RecentScriptedRunsParams{WorkspaceID: job.WorkspaceID, JobID: job.ID, SinceVersion: since})
	if err != nil {
		return false, fmt.Errorf("failed to load recent scripted runs: %w", err)
	}
	return len(fellBack) == 2 && fellBack[0] && fellBack[1], nil
}

// Demoted reports whether a job is demoted, which reflection needs to decide whether the job may graduate again
func (m *Module) Demoted(ctx context.Context, workspaceID, jobID string) (bool, error) {
	job, err := m.getJob(ctx, workspaceID, jobID)
	if err != nil {
		return false, err
	}
	return m.demoted(ctx, job)
}

// LoadForRun implements runner.JobLoader: the job merged with workspace defaults and its playbook
func (m *Module) LoadForRun(ctx context.Context, workspaceID, jobID string, playbookVersion int64) (runner.JobConfig, error) {
	job, err := m.queries.GetJob(ctx, jobsdb.GetJobParams{WorkspaceID: workspaceID, ID: jobID})
	if err != nil {
		return runner.JobConfig{}, err
	}
	ws, err := m.deps.Settings.Get(ctx, workspaceID)
	if err != nil {
		return runner.JobConfig{}, err
	}
	dto := toDto(job)

	cfg := runner.JobConfig{
		ID:                  job.ID,
		WorkspaceID:         job.WorkspaceID,
		Name:                job.Name,
		Instruction:         job.Instruction,
		SuccessCriteria:     dto.Spec.SuccessCriteria,
		BaseImage:           ws.DefaultImage,
		Network:             sandbox.NetworkPolicy(job.Network),
		AllowPrivateNetwork: job.AllowPrivateNetwork,
		RunAsRoot:           job.RunAsRoot,
		SelfImprove:         job.SelfImprove,
		Limits:              effectiveLimits(ws.DefaultLimits, dto.Limits),
		DailySpendLimit:     usdToMicro(ws.DailySpendLimit),
		PlaybookVersion:     playbookVersion,
		SideEffects:         dto.Spec.SideEffects,
	}
	_ = json.Unmarshal([]byte(job.AllowedDomains), &cfg.AllowedDomains)
	for _, o := range dto.Spec.Outputs {
		cfg.OutputNames = append(cfg.OutputNames, o.Name)
		cfg.Outputs = append(cfg.Outputs, describeField(o))
	}
	for _, in := range dto.Spec.Inputs {
		cfg.InputNames = append(cfg.InputNames, in.Name)
		cfg.Inputs = append(cfg.Inputs, describeField(in))
	}
	if job.Image != nil && *job.Image != "" {
		cfg.BaseImage = *job.Image
	}
	if job.Timezone != nil {
		cfg.Timezone = *job.Timezone
	}

	// The job's model wins over the workspace default, unless an admin disabled it or its provider stopped listing it
	switch {
	case job.ModelID != nil && *job.ModelID != "" && m.modelAvailable(ctx, workspaceID, *job.ModelID):
		cfg.ModelID = *job.ModelID
	case ws.AgentModelID != nil:
		cfg.ModelID = *ws.AgentModelID
	}

	// The playbook version is fixed when the run is created, so a run never sees a half-applied change
	content, err := m.deps.Playbooks.Content(ctx, job.ID, playbookVersion)
	if err != nil {
		return runner.JobConfig{}, err
	}
	err = cfg.ApplyPlaybook(content)
	if err != nil {
		return runner.JobConfig{}, err
	}

	if m.deps.Secrets != nil {
		cfg.Env, err = m.deps.Secrets.EnvForJob(ctx, workspaceID, jobID)
		if err != nil {
			return runner.JobConfig{}, fmt.Errorf("failed to load job secrets: %w", err)
		}
	}
	return cfg, nil
}

// describeField renders a declared input or output for prompts, e.g. "count (integer): How many stories to report"
func describeField(f IOField) string {
	desc := f.Name + " (" + f.Type + ")"
	if f.Description != "" {
		desc += ": " + f.Description
	}
	return desc
}

func domainsJSON(domains []string) string {
	if domains == nil {
		domains = []string{}
	}
	raw, _ := json.Marshal(domains)
	return string(raw)
}

// usdToMicro converts dollars to micro-USD, rounding so that e.g. $0.29 doesn't become 289999 through float error
func usdToMicro(usd float64) int64 {
	return int64(math.Round(usd * 1e6))
}

func effectiveLimits(d settings.Limits, o LimitOverrides) runner.Limits {
	l := runner.Limits{
		TimeoutSeconds: d.TimeoutSeconds,
		MaxTurns:       d.MaxTurns,
		MaxCost:        usdToMicro(d.MaxCostUSD),
		CPUs:           d.CPUs,
		MemoryMB:       d.MemoryMB,
		PidsLimit:      d.PidsLimit,
	}
	if o.TimeoutSeconds != nil {
		l.TimeoutSeconds = *o.TimeoutSeconds
	}
	if o.MaxTurns != nil {
		l.MaxTurns = *o.MaxTurns
	}
	if o.MaxCostUSD != nil {
		l.MaxCost = usdToMicro(*o.MaxCostUSD)
	}
	if o.CPUs != nil {
		l.CPUs = *o.CPUs
	}
	if o.MemoryMB != nil {
		l.MemoryMB = *o.MemoryMB
	}
	if o.PidsLimit != nil {
		l.PidsLimit = *o.PidsLimit
	}
	return l
}

// jobFields are the editable fields shared by create and update
type jobFields struct {
	Name        string  `json:"name" minLength:"1" maxLength:"200"`
	Instruction string  `json:"instruction" minLength:"1" maxLength:"20000"`
	Spec        *Spec   `json:"spec,omitempty"`
	ModelID     *string `json:"modelId,omitempty"`
	Image       *string `json:"image,omitempty" maxLength:"500"`
	Network     string  `json:"network,omitempty" enum:"none,internet,allowlist"`
	// AllowedDomains are what an allowlist job may reach, e.g. api.github.com or *.example.com
	AllowedDomains []string `json:"allowedDomains,omitempty" maxItems:"100"`
	// AllowPrivateNetwork lets an internet or allowlist job reach private ranges such as the LAN, which are blocked otherwise
	AllowPrivateNetwork bool           `json:"allowPrivateNetwork,omitempty"`
	RunAsRoot           bool           `json:"runAsRoot,omitempty"`
	Limits              LimitOverrides `json:"limits,omitempty"`
	SelfImprove         *bool          `json:"selfImprove,omitempty"`
	ModePin             *string        `json:"modePin,omitempty" enum:"explore,assisted,scripted"`
	Concurrency         string         `json:"concurrency,omitempty" enum:"skip,queue,parallel"`
	Cron                *string        `json:"cron,omitempty" maxLength:"100"`
	Timezone            *string        `json:"timezone,omitempty" maxLength:"100"`
	Enabled             *bool          `json:"enabled,omitempty"`
}

// jobPatch is a partial update: omitted fields keep their value, and an empty string clears an optional text field
type jobPatch struct {
	Name                *string         `json:"name,omitempty" minLength:"1" maxLength:"200"`
	Instruction         *string         `json:"instruction,omitempty" minLength:"1" maxLength:"20000"`
	Spec                *Spec           `json:"spec,omitempty"`
	ModelID             *string         `json:"modelId,omitempty"`
	Image               *string         `json:"image,omitempty" maxLength:"500"`
	Network             *string         `json:"network,omitempty" enum:"none,internet,allowlist"`
	AllowedDomains      *[]string       `json:"allowedDomains,omitempty" maxItems:"100"`
	AllowPrivateNetwork *bool           `json:"allowPrivateNetwork,omitempty"`
	RunAsRoot           *bool           `json:"runAsRoot,omitempty"`
	Limits              *LimitOverrides `json:"limits,omitempty"`
	SelfImprove         *bool           `json:"selfImprove,omitempty"`
	ModePin             *string         `json:"modePin,omitempty" enum:"explore,assisted,scripted,"`
	Concurrency         *string         `json:"concurrency,omitempty" enum:"skip,queue,parallel"`
	Cron                *string         `json:"cron,omitempty" maxLength:"100"`
	Timezone            *string         `json:"timezone,omitempty" maxLength:"100"`
	Enabled             *bool           `json:"enabled,omitempty"`
}

// fieldsOf returns a stored job's editable fields, the base that a patch is applied to
func fieldsOf(j jobsdb.Job) jobFields {
	f := jobFields{
		Name:                j.Name,
		Instruction:         j.Instruction,
		Spec:                &Spec{},
		ModelID:             j.ModelID,
		Image:               j.Image,
		Network:             j.Network,
		AllowPrivateNetwork: j.AllowPrivateNetwork,
		RunAsRoot:           j.RunAsRoot,
		SelfImprove:         &j.SelfImprove,
		ModePin:             j.ModePin,
		Concurrency:         j.Concurrency,
		Cron:                j.Cron,
		Timezone:            j.Timezone,
		Enabled:             &j.Enabled,
	}
	_ = json.Unmarshal([]byte(j.Spec), f.Spec)
	_ = json.Unmarshal([]byte(j.Limits), &f.Limits)
	_ = json.Unmarshal([]byte(j.AllowedDomains), &f.AllowedDomains)
	return f
}

// apply overlays the fields present in the patch
func (p jobPatch) apply(f *jobFields) {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&f.Name, p.Name)
	set(&f.Instruction, p.Instruction)
	set(&f.Network, p.Network)
	set(&f.Concurrency, p.Concurrency)
	if p.Spec != nil {
		f.Spec = p.Spec
	}
	if p.Limits != nil {
		f.Limits = *p.Limits
	}
	if p.RunAsRoot != nil {
		f.RunAsRoot = *p.RunAsRoot
	}
	if p.AllowedDomains != nil {
		f.AllowedDomains = *p.AllowedDomains
	}
	if p.AllowPrivateNetwork != nil {
		f.AllowPrivateNetwork = *p.AllowPrivateNetwork
	}

	// Optional fields are replaced as given, so an empty string reaches normalize and clears them
	for _, opt := range []struct{ dst, src **string }{
		{&f.ModelID, &p.ModelID}, {&f.Image, &p.Image}, {&f.ModePin, &p.ModePin}, {&f.Cron, &p.Cron}, {&f.Timezone, &p.Timezone},
	} {
		if *opt.src != nil {
			*opt.dst = *opt.src
		}
	}
	if p.SelfImprove != nil {
		f.SelfImprove = p.SelfImprove
	}
	if p.Enabled != nil {
		f.Enabled = p.Enabled
	}
}

func (f *jobFields) normalize() error {
	f.Name = strings.TrimSpace(f.Name)
	f.Instruction = strings.TrimSpace(f.Instruction)

	// The length checks run before trimming, so text made only of spaces has to be caught here
	if f.Name == "" {
		return apperror.InvalidField("name", "required", "must not be empty")
	}
	if f.Instruction == "" {
		return apperror.InvalidField("instruction", "required", "must not be empty")
	}
	if f.Network == "" {
		f.Network = "internet"
	}
	domains, err := normalizeDomains(f.AllowedDomains)
	if err != nil {
		return err
	}
	f.AllowedDomains = domains
	if f.Network == string(sandbox.NetworkAllowlist) && len(f.AllowedDomains) == 0 {
		return apperror.InvalidField("allowedDomains", "required", "an allow-list job needs at least one domain")
	}
	if f.Concurrency == "" {
		f.Concurrency = ConcurrencySkip
	}
	if f.Cron != nil && strings.TrimSpace(*f.Cron) == "" {
		f.Cron = nil
	}
	if f.ModelID != nil && *f.ModelID == "" {
		f.ModelID = nil
	}
	if f.Image != nil && strings.TrimSpace(*f.Image) == "" {
		f.Image = nil
	}
	if f.ModePin != nil && *f.ModePin == "" {
		f.ModePin = nil
	}
	if f.Timezone != nil && strings.TrimSpace(*f.Timezone) == "" {
		f.Timezone = nil
	}
	if f.Spec == nil {
		f.Spec = &Spec{}
	}
	normalizeSpec(f.Spec)
	return validateSchedule(f.Cron, f.Timezone)
}

// domainPattern is a host name, optionally with a *. prefix that matches every name below it
var domainPattern = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// normalizeDomains lowercases and deduplicates an allow-list, rejecting entries the egress proxy couldn't match
func normalizeDomains(in []string) ([]string, error) {
	out := []string{}
	for i, d := range in {
		d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
		if d == "" || slices.Contains(out, d) {
			continue
		}
		if _, err := netip.ParseAddr(d); err == nil || !domainPattern.MatchString(d) || len(d) > 253 {
			return nil, apperror.InvalidField(fmt.Sprintf("allowedDomains[%d]", i), "invalid", "must be a host name such as api.example.com or *.example.com, without scheme, port or path")
		}
		out = append(out, d)
	}
	return out, nil
}

// modelAvailable reports whether a job's model can still be picked, counting a failed check as available so the run reports the real error
func (m *Module) modelAvailable(ctx context.Context, workspaceID, modelID string) bool {
	if m.deps.Models == nil {
		return true
	}
	ok, err := m.deps.Models.ModelExists(ctx, workspaceID, modelID)
	return err != nil || ok
}

// checkModel rejects a job model that is not an enabled model of the workspace, which would fail every run
func (m *Module) checkModel(ctx context.Context, workspaceID string, modelID *string) error {
	if modelID == nil || m.deps.Models == nil {
		return nil
	}
	ok, err := m.deps.Models.ModelExists(ctx, workspaceID, *modelID)
	if err != nil {
		return err
	}
	if !ok {
		return apperror.InvalidField("modelId", "not_found", "is not an enabled model of this workspace")
	}
	return nil
}

// createJob stores a new job and its first playbook version when the spec proposes a Dockerfile
func (m *Module) createJob(ctx context.Context, workspaceID string, userID *string, f jobFields) (jobsdb.Job, error) {
	err := f.normalize()
	if err != nil {
		return jobsdb.Job{}, err
	}
	err = m.checkModel(ctx, workspaceID, f.ModelID)
	if err != nil {
		return jobsdb.Job{}, err
	}
	spec, _ := json.Marshal(f.Spec)
	limits, _ := json.Marshal(f.Limits)
	id := database.NewID()
	err = m.queries.CreateJob(ctx, jobsdb.CreateJobParams{
		ID:                  id,
		WorkspaceID:         workspaceID,
		Name:                f.Name,
		Instruction:         f.Instruction,
		Spec:                string(spec),
		SpecOverrides:       "{}",
		ModelID:             f.ModelID,
		Image:               f.Image,
		Network:             f.Network,
		AllowedDomains:      domainsJSON(f.AllowedDomains),
		AllowPrivateNetwork: f.AllowPrivateNetwork,
		RunAsRoot:           f.RunAsRoot,
		Limits:              string(limits),
		SelfImprove:         f.SelfImprove == nil || *f.SelfImprove,
		ModePin:             f.ModePin,
		Concurrency:         f.Concurrency,
		Cron:                f.Cron,
		Timezone:            f.Timezone,
		Enabled:             f.Enabled == nil || *f.Enabled,
		CreatedBy:           userID,
		CreatedAt:           database.Now(),
	})
	if err != nil {
		return jobsdb.Job{}, fmt.Errorf("failed to create job: %w", err)
	}

	// A Dockerfile proposed by the compile step becomes the first playbook version
	if f.Spec.Dockerfile != nil && strings.TrimSpace(*f.Spec.Dockerfile) != "" {
		_, err = m.deps.Playbooks.Save(ctx, workspaceID, id, playbook.Content{Dockerfile: f.Spec.Dockerfile}, playbook.VersionMeta{
			Author: playbook.AuthorCompile, UserID: userID, Summary: "Environment proposed when the job was created",
		})
		if err != nil {
			return jobsdb.Job{}, err
		}
	}

	err = m.Reschedule(ctx, id)
	if err != nil {
		return jobsdb.Job{}, err
	}
	return m.getJob(ctx, workspaceID, id)
}

// StateStore implements agent.StateStore for one job
type StateStore struct {
	queries *jobsdb.Queries
	jobID   string
}

// ForJob implements runner.StateStores
func (m *Module) ForJob(_ string, jobID string) *StateStore {
	return &StateStore{queries: m.queries, jobID: jobID}
}

func (s *StateStore) Get(ctx context.Context, key string) (string, bool, error) {
	v, err := s.queries.GetState(ctx, jobsdb.GetStateParams{JobID: s.jobID, Key: key})
	if database.IsNotFound(err) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (s *StateStore) Set(ctx context.Context, key, value string) error {
	if len(key) > 200 || len(value) > 1<<20 {
		return apperror.InvalidField("state", "too_large", "keys are limited to 200 characters and values to 1 MiB")
	}

	// A runaway script could otherwise write keys without bound, so new keys stop at a limit while existing ones stay writable
	_, err := s.queries.GetState(ctx, jobsdb.GetStateParams{JobID: s.jobID, Key: key})
	if database.IsNotFound(err) {
		count, err := s.queries.CountState(ctx, s.jobID)
		if err != nil {
			return fmt.Errorf("failed to count state keys: %w", err)
		}
		if count >= maxStateKeys {
			return apperror.InvalidField("state", "too_many", fmt.Sprintf("a job keeps at most %d state keys", maxStateKeys))
		}
	} else if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}
	return s.queries.SetState(ctx, jobsdb.SetStateParams{JobID: s.jobID, Key: key, Value: value, UpdatedAt: database.Now()})
}

// maxStateKeys bounds how many keys one job's state can hold
const maxStateKeys = 1000

// List returns all state entries of the job
func (s *StateStore) List(ctx context.Context) (map[string]string, error) {
	rows, err := s.queries.ListState(ctx, s.jobID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}
