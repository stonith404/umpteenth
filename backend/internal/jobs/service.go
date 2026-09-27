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
	"sync"
	"time"

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
	err = m.invoke(ctx, jobID, methodTrigger, req, &res)
	return res, err
}

// RunFinished implements runner.Notifier and releases the job's concurrency slot
func (m *Module) RunFinished(ctx context.Context, run runner.Run, _ runner.Final) {
	// A lost notification only delays the next queued run until the job's next turn repairs it, so it is logged rather than returned
	err := m.invoke(ctx, run.JobID, methodRunFinished, nil, nil)
	if err != nil {
		slog.WarnContext(ctx, "Failed to tell the job that its run finished", slog.String("job", run.JobID), slog.String("run", run.ID), slog.Any("error", err))
	}
}

// Reschedule re-arms the job's schedule alarm after a change
func (m *Module) Reschedule(ctx context.Context, jobID string) error {
	return m.invoke(ctx, jobID, methodReschedule, nil, nil)
}

// Forget releases a job's schedule and actor state before its workspace is deleted
func (m *Module) Forget(ctx context.Context, jobID string) error {
	return m.invoke(ctx, jobID, methodForget, nil, nil)
}

// ListJobIDs lists every job of the workspace, archived ones included, since those can still hold actor state
func (m *Module) ListJobIDs(ctx context.Context, workspaceID string) ([]string, error) {
	ids, err := m.queries.ListJobIDs(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	return ids, nil
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

// nextMode derives the mode of the job's next run from its playbook and history
// Nothing learned yet explores, know-how assists, and a main script runs without the agent unless repeated fallbacks demoted the job
func (m *Module) nextMode(ctx context.Context, job jobsdb.Job) (mode string, demoted bool, err error) {
	content, err := m.deps.Playbooks.Content(ctx, job.ID, job.PlaybookVersion)
	if err != nil {
		return "", false, fmt.Errorf("failed to load the playbook: %w", err)
	}
	if content.Main != nil {
		demoted, err = m.demoted(ctx, job)
		if err != nil {
			return "", false, err
		}
	}
	return modeFor(content, job.Graduate, demoted), demoted, nil
}

// modeFor applies the mode rules to a job's current playbook, whether it may graduate and whether it is demoted
// The job page and the jobs list both decide the next mode here, so they never disagree
func modeFor(content playbook.Content, graduate, demoted bool) string {
	switch {
	case content.IsEmpty():
		return ModeExplore
	case content.Main == nil || !graduate || demoted:
		return ModeAssisted
	}
	return ModeScripted
}

// demoted reports whether the last two scripted runs since the job graduated both fell back to the agent
// The job stays demoted until reflection graduates it again, which moves the graduated version past these runs
func (m *Module) demoted(ctx context.Context, job jobsdb.Job) (bool, error) {
	// A job with graduation turned off never runs its main script, so it is never demoted and nothing may report it as such
	if !job.Graduate {
		return false, nil
	}

	fellBack, err := m.queries.RecentScriptedRuns(ctx, jobsdb.RecentScriptedRunsParams{WorkspaceID: job.WorkspaceID, JobID: job.ID, SinceVersion: deref(job.GraduatedVersion)})
	if err != nil {
		return false, fmt.Errorf("failed to load recent scripted runs: %w", err)
	}
	return demotedBy(fellBack), nil
}

// demotedBy applies the demotion rule to whether each of a job's latest two scripted runs since it graduated fell back
func demotedBy(fellBack []bool) bool {
	return len(fellBack) == 2 && fellBack[0] && fellBack[1]
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
	job, err := m.getJob(ctx, workspaceID, jobID)
	if err != nil {
		return runner.JobConfig{}, err
	}
	dto, err := toDto(job)
	if err != nil {
		return runner.JobConfig{}, err
	}
	ws, err := m.deps.Settings.Get(ctx, workspaceID)
	if err != nil {
		return runner.JobConfig{}, err
	}

	cfg := runner.JobConfig{
		ID:                  job.ID,
		Instruction:         job.Instruction,
		SuccessCriteria:     dto.Spec.SuccessCriteria,
		BaseImage:           ws.DefaultImage,
		Network:             sandbox.NetworkPolicy(job.Network),
		AllowedDomains:      dto.AllowedDomains,
		AllowPrivateNetwork: job.AllowPrivateNetwork,
		RunAsRoot:           job.RunAsRoot,
		SelfImprove:         job.SelfImprove,
		Graduate:            job.Graduate,
		Limits:              effectiveLimits(ws.DefaultLimits, dto.Limits),
		DailySpendLimit:     usdToMicro(ws.DailySpendLimit),
		SideEffects:         dto.Spec.SideEffects,
		Timezone:            deref(job.Timezone),
	}
	for _, o := range dto.Spec.Outputs {
		cfg.OutputNames = append(cfg.OutputNames, o.Name)
		cfg.Outputs = append(cfg.Outputs, describeField(o))
	}
	for _, in := range dto.Spec.Inputs {
		cfg.InputNames = append(cfg.InputNames, in.Name)
		cfg.Inputs = append(cfg.Inputs, describeField(in))
	}
	if deref(job.Image) != "" {
		cfg.BaseImage = *job.Image
	}

	// The job's model wins over the workspace default, unless an admin disabled it or its provider stopped listing it
	switch {
	case deref(job.ModelID) != "" && m.modelAvailable(ctx, workspaceID, *job.ModelID):
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
	return l
}

// jobFields are the editable fields shared by create and update
type jobFields struct {
	Name        string  `json:"name" minLength:"1" maxLength:"200"`
	Instruction string  `json:"instruction" minLength:"1" maxLength:"20000"`
	Spec        *Spec   `json:"spec,omitempty"`
	ModelID     *string `json:"modelId,omitempty"`
	Image       *string `json:"image,omitempty" maxLength:"500"`
	Network     string  `json:"network,omitempty" enum:"none,internet,allowlist,unrestricted"`
	// AllowedDomains are what an allowlist job may reach, e.g. api.github.com or *.example.com
	AllowedDomains []string `json:"allowedDomains,omitempty" maxItems:"100"`
	// AllowPrivateNetwork lets an internet or allowlist job reach private ranges such as the LAN, which are blocked otherwise
	AllowPrivateNetwork bool           `json:"allowPrivateNetwork,omitempty"`
	RunAsRoot           bool           `json:"runAsRoot,omitempty"`
	Limits              LimitOverrides `json:"limits,omitzero"`
	SelfImprove         *bool          `json:"selfImprove,omitempty"`
	Graduate            *bool          `json:"graduate,omitempty"`
	Concurrency         string         `json:"concurrency,omitempty" enum:"skip,queue,parallel"`
	Cron                *string        `json:"cron,omitempty" maxLength:"100"`
	Timezone            *string        `json:"timezone,omitempty" maxLength:"100"`
}

// jobPatch is a partial update: omitted fields keep their value, and an empty string clears an optional text field
type jobPatch struct {
	Name                *string         `json:"name,omitempty" minLength:"1" maxLength:"200"`
	Instruction         *string         `json:"instruction,omitempty" minLength:"1" maxLength:"20000"`
	Spec                *Spec           `json:"spec,omitempty"`
	RebuildSpec         *bool           `json:"rebuildSpec,omitempty" doc:"Whether a changed instruction compiles the goal, success criteria, inputs, outputs, services and side effects again, true when omitted"`
	ModelID             *string         `json:"modelId,omitempty"`
	Image               *string         `json:"image,omitempty" maxLength:"500"`
	Network             *string         `json:"network,omitempty" enum:"none,internet,allowlist,unrestricted"`
	AllowedDomains      *[]string       `json:"allowedDomains,omitempty" maxItems:"100"`
	AllowPrivateNetwork *bool           `json:"allowPrivateNetwork,omitempty"`
	RunAsRoot           *bool           `json:"runAsRoot,omitempty"`
	Limits              *LimitOverrides `json:"limits,omitempty"`
	SelfImprove         *bool           `json:"selfImprove,omitempty"`
	Graduate            *bool           `json:"graduate,omitempty"`
	Concurrency         *string         `json:"concurrency,omitempty" enum:"skip,queue,parallel"`
	Cron                *string         `json:"cron,omitempty" maxLength:"100"`
	Timezone            *string         `json:"timezone,omitempty" maxLength:"100"`
}

// fieldsOf returns a stored job's editable fields, the base that a patch is applied to
func fieldsOf(j jobsdb.Job) (jobFields, error) {
	d, err := toDto(j)
	if err != nil {
		return jobFields{}, err
	}
	return jobFields{
		Name:                d.Name,
		Instruction:         d.Instruction,
		Spec:                &d.Spec,
		ModelID:             d.ModelID,
		Image:               d.Image,
		Network:             d.Network,
		AllowedDomains:      d.AllowedDomains,
		AllowPrivateNetwork: d.AllowPrivateNetwork,
		RunAsRoot:           d.RunAsRoot,
		Limits:              d.Limits,
		SelfImprove:         &d.SelfImprove,
		Graduate:            &d.Graduate,
		Concurrency:         d.Concurrency,
		Cron:                d.Cron,
		Timezone:            d.Timezone,
	}, nil
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
		{&f.ModelID, &p.ModelID}, {&f.Image, &p.Image}, {&f.Cron, &p.Cron}, {&f.Timezone, &p.Timezone},
	} {
		if *opt.src != nil {
			*opt.dst = *opt.src
		}
	}
	if p.SelfImprove != nil {
		f.SelfImprove = p.SelfImprove
	}
	if p.Graduate != nil {
		f.Graduate = p.Graduate
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

// checkFieldNames rejects inputs or outputs that share a name, since runs pass their values by name and the job page lists them by it
func checkFieldNames(s *Spec) error {
	for _, list := range []struct {
		field  string
		fields []IOField
	}{{"spec.inputs", s.Inputs}, {"spec.outputs", s.Outputs}} {
		seen := make(map[string]bool, len(list.fields))
		for _, f := range list.fields {
			if seen[f.Name] {
				return apperror.InvalidField(list.field, "duplicate", fmt.Sprintf("has more than one field named %q", f.Name))
			}
			seen[f.Name] = true
		}
	}
	return nil
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

// checkNetwork refuses a network the active sandbox adapter doesn't offer, such as the unrestricted network an operator turned off
// An adapter that can't be reached doesn't block saving, since the run would report the problem anyway
// A busy container engine can take seconds to answer, so the check gets the same bounded wait as the system info
func (m *Module) checkNetwork(ctx context.Context, network string) error {
	if m.deps.SandboxInfo == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := m.deps.SandboxInfo(ctx)
	if err != nil {
		return nil //nolint:nilerr // an unreachable adapter doesn't block saving
	}
	if info.Caps.SupportsNetwork(sandbox.NetworkPolicy(network)) {
		return nil
	}
	return apperror.InvalidField("network", "unsupported", "the sandbox backend doesn't offer this network")
}

// createJob stores a new job and its first playbook version when the spec proposes a Dockerfile
func (m *Module) createJob(ctx context.Context, workspaceID string, userID *string, f jobFields) (jobsdb.Job, error) {
	err := f.normalize()
	if err != nil {
		return jobsdb.Job{}, err
	}
	err = checkFieldNames(f.Spec)
	if err != nil {
		return jobsdb.Job{}, err
	}
	err = m.checkModel(ctx, workspaceID, f.ModelID)
	if err != nil {
		return jobsdb.Job{}, err
	}
	err = m.checkNetwork(ctx, f.Network)
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
		ModelID:             f.ModelID,
		Image:               f.Image,
		Network:             f.Network,
		AllowedDomains:      domainsJSON(f.AllowedDomains),
		AllowPrivateNetwork: f.AllowPrivateNetwork,
		RunAsRoot:           f.RunAsRoot,
		Limits:              string(limits),
		SelfImprove:         f.SelfImprove == nil || *f.SelfImprove,
		Graduate:            f.Graduate == nil || *f.Graduate,
		Concurrency:         f.Concurrency,
		Cron:                f.Cron,
		Timezone:            f.Timezone,
		CreatedBy:           userID,
		CreatedAt:           database.Now(),
	})
	if err != nil {
		return jobsdb.Job{}, fmt.Errorf("failed to create job: %w", err)
	}

	// A job that could not be fully set up is removed again, so a retried request doesn't leave a half-created duplicate behind
	err = m.setUpJob(ctx, workspaceID, id, userID, f)
	if err != nil {
		delErr := m.queries.DeleteJob(context.WithoutCancel(ctx), jobsdb.DeleteJobParams{WorkspaceID: workspaceID, ID: id})
		if delErr != nil {
			slog.WarnContext(ctx, "Failed to remove a job whose setup failed", slog.String("job", id), slog.Any("error", delErr))
		}
		return jobsdb.Job{}, err
	}
	return m.getJob(ctx, workspaceID, id)
}

// setUpJob stores the first playbook version of a new job and arms its schedule
func (m *Module) setUpJob(ctx context.Context, workspaceID, jobID string, userID *string, f jobFields) error {
	// A Dockerfile proposed by the compile step becomes the first playbook version
	if f.Spec.Dockerfile != nil && strings.TrimSpace(*f.Spec.Dockerfile) != "" {
		_, err := m.deps.Playbooks.Save(ctx, workspaceID, jobID, playbook.Content{Dockerfile: f.Spec.Dockerfile}, playbook.VersionMeta{
			Author: playbook.AuthorCompile, UserID: userID, Summary: "Environment proposed when the job was created",
		})
		if err != nil {
			return err
		}
	}
	return m.Reschedule(ctx, jobID)
}

// StateStore implements runner.JobState for one job
type StateStore struct {
	db      *database.DB
	queries *jobsdb.Queries
	locks   *stateLocks
	jobID   string
}

// ForJob implements runner.StateStores
func (m *Module) ForJob(jobID string) *StateStore {
	return &StateStore{db: m.deps.DB, queries: m.queries, locks: &m.stateLocks, jobID: jobID}
}

func (s *StateStore) Get(ctx context.Context, key string) (string, bool, error) {
	row, err := s.queries.GetState(ctx, jobsdb.GetStateParams{JobID: s.jobID, Key: key})
	if database.IsNotFound(err) {
		return "", false, nil
	} else if err != nil {
		return "", false, err
	}
	return row.Value, true, nil
}

func (s *StateStore) Set(ctx context.Context, key, value string) error {
	if len(key) > 200 || len(value) > 1<<20 {
		return apperror.InvalidField("state", "too_large", "keys are limited to 200 characters and values to 1 MiB")
	}

	// Writes to one job wait for their turn here first, since each one waiting on the job's row would hold a database connection the rest of the app needs
	unlock, err := s.locks.lock(ctx, s.jobID)
	if err != nil {
		return err
	}
	defer unlock()

	return s.db.InTx(ctx, func(tx *database.Tx) error {
		q := jobsdb.New(tx)

		// Writes from other replicas take turns on the job's row, so the state they leave behind together stays within the limits each of them checks
		err := q.LockStateForWrite(ctx, s.jobID)
		if err != nil {
			return fmt.Errorf("failed to lock state: %w", err)
		}
		usage, err := q.StateUsage(ctx, s.jobID)
		if err != nil {
			return fmt.Errorf("failed to measure state: %w", err)
		}
		old, err := q.StateEntrySize(ctx, jobsdb.StateEntrySizeParams{JobID: s.jobID, Key: key})
		exists := !database.IsNotFound(err)
		if exists && err != nil {
			return fmt.Errorf("failed to load state: %w", err)
		}

		// A runaway script could otherwise write keys without bound, so new keys stop at a limit while existing ones stay writable
		if !exists && usage.Keys >= runner.MaxStateKeys {
			return apperror.InvalidField("state", "too_many", fmt.Sprintf("a job keeps at most %d state keys", runner.MaxStateKeys))
		}

		// Replacing a value frees the size of the old one
		size := usage.Bytes + int64(len(key)+len(value))
		if exists {
			size -= old
		}
		if size > runner.MaxStateBytes {
			return apperror.InvalidField("state", "too_large", fmt.Sprintf("keys and values of a job may total at most %d MiB", runner.MaxStateBytes>>20))
		}
		return q.SetState(ctx, jobsdb.SetStateParams{JobID: s.jobID, Key: key, Value: value, UpdatedAt: database.Now()})
	})
}

// stateLocks gives the writers of each job's state their turn on this replica
// Its zero value is ready to use
type stateLocks struct {
	mu   sync.Mutex
	jobs map[string]*stateLock
}

// stateLock is one job's turn, with the number of writers holding or waiting for it
type stateLock struct {
	turn    chan struct{}
	writers int
}

// lock waits for the job's turn, and gives up when the context ends
func (s *stateLocks) lock(ctx context.Context, jobID string) (unlock func(), err error) {
	s.mu.Lock()
	l, ok := s.jobs[jobID]
	if !ok {
		if s.jobs == nil {
			s.jobs = map[string]*stateLock{}
		}
		l = &stateLock{turn: make(chan struct{}, 1)}
		s.jobs[jobID] = l
	}
	l.writers++
	s.mu.Unlock()

	// The job's entry goes away with its last writer, so the map only holds jobs that are being written
	leave := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		l.writers--
		if l.writers == 0 {
			delete(s.jobs, jobID)
		}
	}
	select {
	case l.turn <- struct{}{}:
		return func() {
			<-l.turn
			leave()
		}, nil
	case <-ctx.Done():
		leave()
		return nil, ctx.Err()
	}
}

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
