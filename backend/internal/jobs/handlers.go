package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/jobs/jobsdb"
	"github.com/stonith404/umpteenth/backend/internal/listquery"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// JobListDto is a job as listed in the jobs table
type JobListDto struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Cron          *string  `json:"cron"`
	Timezone      *string  `json:"timezone"`
	ScheduleHuman *string  `json:"scheduleHuman"`
	NextRunAt     *int64   `json:"nextRunAt"`
	Graduate      bool     `json:"graduate"`
	CreatedAt     int64    `json:"createdAt"`
	UpdatedAt     int64    `json:"updatedAt"`
	LastRun       *LastRun `json:"lastRun" doc:"The latest run, the same as the first of recentRuns"`
	// LastMode is the mode of the latest run
	LastMode *string `json:"lastMode" doc:"The mode the latest run used, which can differ from nextMode once the playbook changed"`
	RunCount int64   `json:"runCount"`
	// RecentRuns feed the history strip of the jobs list
	RecentRuns []LastRun `json:"recentRuns" maxItems:"10" doc:"The job's latest runs, at most 10, newest first"`
	// NextMode is the mode the next run starts in, decided like the job page's nextMode
	NextMode string `json:"nextMode" enum:"explore,assisted,scripted" doc:"The mode the next run starts in, which is never scripted while graduation is off"`
	// Inputs let the jobs list open the run-now dialog without loading the whole job
	Inputs []IOField `json:"inputs" doc:"The run inputs the job declares, the same as spec.inputs of the job"`
}

var listSpec = &listquery.Spec{
	// The current playbook is read with the job, since the next mode depends on it
	Select: "SELECT j.id, j.name, j.cron, j.timezone, j.spec, j.next_run_at, j.graduate, j.created_at, j.updated_at, j.run_counter, j.graduated_version, " +
		"(SELECT pv.content FROM playbook_versions pv WHERE pv.job_id = j.id AND pv.version = j.playbook_version) FROM jobs j",
	From:          "FROM jobs j",
	Sorts:         map[string]string{"name": "j.name", "createdAt": "j.created_at", "updatedAt": "j.updated_at", "nextRunAt": "j.next_run_at", "runCount": "j.run_counter"},
	NullableSorts: []string{"nextRunAt"},
	DefaultSort:   "name",
	Search:        []string{"j.name", "j.instruction"},
	TieBreaker:    "j.id",
}

type listInput struct {
	httpserver.ListParams
}

func (m *Module) list(ctx context.Context, in *listInput) (*httpserver.PaginatedOutput[JobListDto], error) {
	wid := principal.WorkspaceID(ctx)
	q := listquery.New(listSpec).WhereEq("j.workspace_id", wid).Where("j.archived_at IS NULL")

	// The scan keeps what the next mode depends on next to each job, in the order of the page
	var modeInputs []listModeInput
	items, total, err := listquery.Run(ctx, m.deps.DB, q, in.ToQuery(), func(rows *sql.Rows) (JobListDto, error) {
		var (
			d    JobListDto
			spec string
			mi   listModeInput
		)
		err := rows.Scan(&d.ID, &d.Name, &d.Cron, &d.Timezone, &spec, &d.NextRunAt, &d.Graduate, &d.CreatedAt, &d.UpdatedAt,
			&d.RunCount, &mi.graduatedVersion, &mi.content)
		if err != nil {
			return d, err
		}
		// The spec adds the schedule in words while it still describes the job's cron, and the inputs the run-now dialog prefills
		var s Spec
		if json.Unmarshal([]byte(spec), &s) == nil {
			if s.Schedule != nil && s.Schedule.Human != "" && d.Cron != nil && s.Schedule.Cron == *d.Cron {
				d.ScheduleHuman = &s.Schedule.Human
			}
			d.Inputs = s.Inputs
		}
		if d.Inputs == nil {
			d.Inputs = []IOField{}
		}
		modeInputs = append(modeInputs, mi)
		return d, nil
	})
	if err != nil {
		return nil, err
	}

	// The run history and the next modes are loaded for the whole page at once, so the query count doesn't grow with the page size
	err = m.addRecentRuns(ctx, wid, items)
	if err != nil {
		return nil, err
	}
	err = m.addNextModes(ctx, wid, items, modeInputs)
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

type jobOutput struct {
	Body JobDto
}

type createInput struct {
	Body jobFields
}

func (m *Module) create(ctx context.Context, in *createInput) (*jobOutput, error) {
	job, err := m.createJob(ctx, principal.WorkspaceID(ctx), principal.UserIDPtr(ctx), in.Body)
	if err != nil {
		return nil, err
	}
	dto, err := toDto(job)
	if err != nil {
		return nil, err
	}
	return &jobOutput{Body: dto}, nil
}

type idInput struct {
	ID string `path:"id"`
}

func (m *Module) get(ctx context.Context, in *idInput) (*jobOutput, error) {
	wid := principal.WorkspaceID(ctx)
	job, err := m.getJob(ctx, wid, in.ID)
	if err != nil {
		return nil, err
	}
	dto, err := toDto(job)
	if err != nil {
		return nil, err
	}
	out := &jobOutput{Body: dto}

	// The header shows which mode the next run starts in, since graduation and demotion happen between runs
	out.Body.NextMode, out.Body.Demoted, err = m.nextMode(ctx, job)
	if err != nil {
		return nil, err
	}

	// The header shows the latest run, which a job without runs does not have
	last, err := m.queries.GetLastRun(ctx, jobsdb.GetLastRunParams{WorkspaceID: wid, JobID: in.ID})
	if err == nil {
		out.Body.LastRun = &LastRun{ID: last.ID, Number: last.Number, Status: last.Status, QueuedAt: last.QueuedAt}
	} else if !database.IsNotFound(err) {
		return nil, fmt.Errorf("failed to load the last run: %w", err)
	}
	return out, nil
}

type updateInput struct {
	ID   string `path:"id"`
	Body jobPatch
}

// maxUpdateAttempts bounds how often a patch is reapplied when concurrent writes keep changing the job
const maxUpdateAttempts = 3

func (m *Module) update(ctx context.Context, in *updateInput) (*jobOutput, error) {
	wid := principal.WorkspaceID(ctx)

	// Compiling takes a while, so it happens once before the attempts rather than in each of them
	patch, err := m.withRebuiltSpec(ctx, wid, in.ID, in.Body)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		applied, err := m.applyPatch(ctx, wid, in.ID, patch)
		if err != nil {
			return nil, err
		}
		if applied {
			break
		}
		if attempt == maxUpdateAttempts {
			return nil, apperror.Conflict("The job was changed at the same time, try again")
		}
	}

	// Any change can affect the schedule, so the actor re-arms it
	err = m.Reschedule(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return m.get(ctx, &idInput{ID: in.ID})
}

// applyPatch writes the patch over the stored job, and reports false when another write changed the job since it was read
// Comparing updated_at keeps concurrent edits of different fields from silently undoing each other
func (m *Module) applyPatch(ctx context.Context, wid, jobID string, patch jobPatch) (bool, error) {
	current, err := m.getJob(ctx, wid, jobID)
	if err != nil {
		return false, err
	}

	// The patch only replaces the fields it carries
	f, err := fieldsOf(current)
	if err != nil {
		return false, err
	}
	patch.apply(&f)
	err = f.normalize()
	if err != nil {
		return false, err
	}

	// Inputs or outputs that share a name are refused
	err = checkFieldNames(f.Spec)
	if err != nil {
		return false, err
	}

	// Only a newly picked model or network is checked, so a job whose model or network was disabled since can still be edited
	if deref(f.ModelID) != deref(current.ModelID) {
		err = m.checkModel(ctx, wid, f.ModelID)
		if err != nil {
			return false, err
		}
	}
	if f.Network != current.Network {
		err = m.checkNetwork(ctx, f.Network)
		if err != nil {
			return false, err
		}
	}

	spec, _ := json.Marshal(f.Spec)
	limits, _ := json.Marshal(f.Limits)
	n, err := m.queries.UpdateJob(ctx, jobsdb.UpdateJobParams{
		WorkspaceID:         wid,
		ID:                  jobID,
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
		SelfImprove:         *f.SelfImprove,
		Graduate:            *f.Graduate,
		Concurrency:         f.Concurrency,
		Cron:                f.Cron,
		Timezone:            f.Timezone,
		// The new timestamp always differs from the one compared against, even within the same millisecond or on a replica whose clock lags
		UpdatedAt:     max(database.Now(), current.UpdatedAt+1),
		ReadUpdatedAt: current.UpdatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("failed to update job: %w", err)
	}
	return n == 1, nil
}

func (m *Module) archive(ctx context.Context, in *idInput) (*struct{}, error) {
	n, err := m.queries.ArchiveJob(ctx, jobsdb.ArchiveJobParams{WorkspaceID: principal.WorkspaceID(ctx), ID: in.ID, ArchivedAt: new(database.Now())})
	if err != nil {
		return nil, fmt.Errorf("failed to delete job: %w", err)
	}
	if n == 0 {
		return nil, apperror.NotFound("Job")
	}

	// Runs still waiting for their turn would never start once the job is gone, so they are cancelled
	wid := principal.WorkspaceID(ctx)
	live, err := m.queries.ListActiveRunsForJob(ctx, jobsdb.ListActiveRunsForJobParams{WorkspaceID: wid, JobID: in.ID})
	if err != nil {
		return nil, fmt.Errorf("failed to load the job's runs: %w", err)
	}
	for _, r := range live {
		if r.Status != runner.StatusQueued {
			continue
		}
		err = m.deps.Runs.Cancel(ctx, wid, r.ID)
		if err != nil && !apperror.IsCode(err, apperror.CodeConflict) {
			slog.WarnContext(ctx, "Failed to cancel a queued run of an archived job", slog.String("run", r.ID), slog.Any("error", err))
		}
	}
	return nil, m.Reschedule(ctx, in.ID)
}

type runNowInput struct {
	ID   string `path:"id"`
	Body struct {
		Input        json.RawMessage `json:"input,omitempty" doc:"Run input, available as /ump/input.json"`
		Instructions string          `json:"instructions,omitempty" maxLength:"10000" doc:"Additional instructions for this run only"`
	}
}

type triggerOutput struct {
	Body runs.TriggerResult
}

func (m *Module) runNow(ctx context.Context, in *runNowInput) (*triggerOutput, error) {
	p, _ := principal.From(ctx)
	trigger := runs.TriggerManual
	if p.UserID == "" {
		trigger = runs.TriggerAPI
	}
	res, err := m.Trigger(ctx, p.WorkspaceID, in.ID, runs.TriggerRequest{
		Trigger:      trigger,
		TriggeredBy:  principal.UserIDPtr(ctx),
		Input:        in.Body.Input,
		Instructions: in.Body.Instructions,
	})
	if err != nil {
		return nil, err
	}
	return &triggerOutput{Body: res}, nil
}

type webhookTokenOutput struct {
	Body struct {
		Token string `json:"token" doc:"Shown once"`
		URL   string `json:"url"`
	}
}

func (m *Module) rotateWebhookToken(ctx context.Context, in *idInput) (*webhookTokenOutput, error) {
	wid := principal.WorkspaceID(ctx)
	_, err := m.getJob(ctx, wid, in.ID)
	if err != nil {
		return nil, err
	}
	token := "umh_" + crypto.RandomToken(32)
	err = m.queries.SetWebhookTokenHash(ctx, jobsdb.SetWebhookTokenHashParams{WorkspaceID: wid, ID: in.ID, WebhookTokenHash: new(crypto.HashToken(token)), UpdatedAt: database.Now()})
	if err != nil {
		return nil, fmt.Errorf("failed to set webhook token: %w", err)
	}
	out := &webhookTokenOutput{}
	out.Body.Token = token
	out.Body.URL = "/hooks/" + in.ID
	return out, nil
}

// StateEntryDto is one key of a job's persistent state
type StateEntryDto struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt int64  `json:"updatedAt"`
}

var stateSpec = &listquery.Spec{
	Select:      "SELECT s.key, s.value, s.updated_at FROM job_state s",
	From:        "FROM job_state s",
	Sorts:       map[string]string{"key": "s.key", "updatedAt": "s.updated_at"},
	DefaultSort: "key",
	Search:      []string{"s.key", "s.value"},
	TieBreaker:  "s.key",
}

type listStateInput struct {
	httpserver.ListParams
	ID string `path:"id"`
}

func (m *Module) listState(ctx context.Context, in *listStateInput) (*httpserver.PaginatedOutput[StateEntryDto], error) {
	// State rows carry no workspace, so the job is checked first
	_, err := m.getJob(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}

	q := listquery.New(stateSpec).WhereEq("s.job_id", in.ID)
	items, total, err := listquery.Run(ctx, m.deps.DB, q, in.ToQuery(), func(rows *sql.Rows) (StateEntryDto, error) {
		var e StateEntryDto
		err := rows.Scan(&e.Key, &e.Value, &e.UpdatedAt)
		return e, err
	})
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

type putStateInput struct {
	ID   string `path:"id"`
	Key  string `path:"key" maxLength:"200"`
	Body struct {
		Value string `json:"value"`
		// BaseUpdatedAt guards against overwriting a value a run or another person wrote meanwhile, and is optional so scripts can still write unconditionally
		BaseUpdatedAt *int64 `json:"baseUpdatedAt,omitempty" doc:"The updatedAt of the entry the edit started from, or 0 to only add a new key; a key changed or added meanwhile makes the save fail with 409"`
	}
}

type getStateOutput struct {
	Body StateEntryDto
}

// getState reads one key exactly, which a substring search over the list cannot guarantee
func (m *Module) getState(ctx context.Context, in *stateKeyInput) (*getStateOutput, error) {
	_, err := m.getJob(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	row, err := m.queries.GetState(ctx, jobsdb.GetStateParams{JobID: in.ID, Key: in.Key})
	if database.IsNotFound(err) {
		return nil, apperror.NotFound("State key")
	} else if err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}
	return &getStateOutput{Body: StateEntryDto{Key: in.Key, Value: row.Value, UpdatedAt: row.UpdatedAt}}, nil
}

func (m *Module) putState(ctx context.Context, in *putStateInput) (*struct{}, error) {
	_, err := m.getJob(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	return nil, m.ForJob(in.ID).set(ctx, in.Key, in.Body.Value, in.Body.BaseUpdatedAt)
}

type stateKeyInput struct {
	ID  string `path:"id"`
	Key string `path:"key"`
}

func (m *Module) deleteState(ctx context.Context, in *stateKeyInput) (*struct{}, error) {
	_, err := m.getJob(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	n, err := m.queries.DeleteState(ctx, jobsdb.DeleteStateParams{JobID: in.ID, Key: in.Key})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, apperror.NotFound("State key")
	}
	return nil, nil
}

type webhookInput struct {
	JobID         string `path:"jobId"`
	Authorization string `header:"Authorization"`
	RawBody       []byte
}

// webhook triggers a run from an external system, authenticated with the job's own webhook token
func (m *Module) webhook(ctx context.Context, in *webhookInput) (*triggerOutput, error) {
	token, ok := strings.CutPrefix(in.Authorization, "Bearer ")
	token = strings.TrimSpace(token)
	if !ok || token == "" {
		return nil, apperror.InvalidToken()
	}

	// The job ID is globally unique, and the token then proves the caller may act within the job's workspace
	job, err := m.queries.GetJobUnscoped(ctx, in.JobID)
	if err != nil || job.ArchivedAt != nil || job.WebhookTokenHash == nil || *job.WebhookTokenHash != crypto.HashToken(token) {
		return nil, apperror.InvalidToken()
	}

	// Webhooks are rate limited per job, and the limit holds across replicas because it is an actor
	// An unreachable limiter lets the call through rather than failing every webhook of the job
	if m.deps.WebhookLimiter != nil {
		allowed, retryAfter, err := m.deps.WebhookLimiter.Allow(ctx, "job:"+job.ID)
		if err != nil {
			slog.WarnContext(ctx, "Failed to check the webhook rate limit", slog.String("job", job.ID), slog.Any("error", err))
		} else if !allowed {
			return nil, apperror.RateLimited(retryAfter)
		}
	}

	var input json.RawMessage
	if len(in.RawBody) > 0 {
		if !json.Valid(in.RawBody) {
			// Non-JSON payloads are passed through as a JSON string
			input, _ = json.Marshal(string(in.RawBody))
		} else {
			input = json.RawMessage(in.RawBody)
		}
	}

	var res runs.TriggerResult
	err = m.invoke(ctx, job.ID, methodTrigger, runs.TriggerRequest{Trigger: runs.TriggerWebhook, Input: input}, &res)
	if err != nil {
		return nil, err
	}
	return &triggerOutput{Body: res}, nil
}

// deref returns the value p points to, or the zero value for nil
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
