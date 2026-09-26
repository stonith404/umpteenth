package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
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
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Cron        *string  `json:"cron"`
	Timezone    *string  `json:"timezone"`
	ScheduleFmt *string  `json:"scheduleHuman"`
	NextRunAt   *int64   `json:"nextRunAt"`
	Enabled     bool     `json:"enabled"`
	ModePin     *string  `json:"modePin"`
	CreatedAt   int64    `json:"createdAt"`
	UpdatedAt   int64    `json:"updatedAt"`
	LastRun     *LastRun `json:"lastRun"`
	// LastMode is the mode of the latest run, the job's effective mode
	LastMode *string `json:"lastMode"`
	RunCount int64   `json:"runCount"`
}

const lastRunSQL = "(SELECT %s FROM runs lr WHERE lr.job_id = j.id ORDER BY lr.queued_at DESC, lr.id DESC LIMIT 1)"

var listSpec = &listquery.Spec{
	Select: "SELECT j.id, j.name, j.cron, j.timezone, j.spec, j.next_run_at, j.enabled, j.mode_pin, j.created_at, j.updated_at, " +
		fmt.Sprintf(lastRunSQL, "lr.id") + ", " + fmt.Sprintf(lastRunSQL, "lr.number") + ", " + fmt.Sprintf(lastRunSQL, "lr.status") + ", " +
		fmt.Sprintf(lastRunSQL, "lr.queued_at") + ", " + fmt.Sprintf(lastRunSQL, "lr.mode") + ", j.run_counter FROM jobs j",
	From:          "FROM jobs j",
	Sorts:         map[string]string{"name": "j.name", "createdAt": "j.created_at", "updatedAt": "j.updated_at", "nextRunAt": "j.next_run_at", "enabled": "j.enabled", "runCount": "j.run_counter"},
	NullableSorts: []string{"nextRunAt"},
	DefaultSort:   "name",
	Search:        []string{"j.name", "j.instruction"},
	TieBreaker:    "j.id",
}

type listInput struct {
	httpserver.ListParams
	Enabled string `query:"enabled" doc:"Comma-separated enabled states, true and/or false"`
}

func (m *Module) list(ctx context.Context, in *listInput) (*httpserver.PaginatedOutput[JobListDto], error) {
	q := listquery.New(listSpec).WhereEq("j.workspace_id", principal.WorkspaceID(ctx)).Where("j.archived_at IS NULL")
	enabled := listquery.SplitCSV(in.Enabled)
	for _, v := range enabled {
		if v != "true" && v != "false" {
			return nil, apperror.InvalidField("enabled", "invalid", "must be true, false or both")
		}
	}
	// Asking for both states is the same as not filtering
	if len(enabled) > 0 && (!slices.Contains(enabled, "true") || !slices.Contains(enabled, "false")) {
		q.WhereEq("j.enabled", enabled[0] == "true")
	}
	items, total, err := listquery.Run(ctx, m.db, q, in.ToQuery(), func(rows *sql.Rows) (JobListDto, error) {
		var (
			d        JobListDto
			spec     string
			lrID     *string
			lrNum    *int64
			lrStatus *string
			lrQueued *int64
		)
		err := rows.Scan(&d.ID, &d.Name, &d.Cron, &d.Timezone, &spec, &d.NextRunAt, &d.Enabled, &d.ModePin, &d.CreatedAt, &d.UpdatedAt,
			&lrID, &lrNum, &lrStatus, &lrQueued, &d.LastMode, &d.RunCount)
		if err != nil {
			return d, err
		}
		var s Spec
		if json.Unmarshal([]byte(spec), &s) == nil && s.Schedule != nil && s.Schedule.Human != "" && d.Cron != nil && s.Schedule.Cron == *d.Cron {
			d.ScheduleFmt = &s.Schedule.Human
		}
		if lrID != nil {
			d.LastRun = &LastRun{ID: *lrID, Number: deref(lrNum), Status: derefS(lrStatus), QueuedAt: deref(lrQueued)}
		}
		return d, nil
	})
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
	return &jobOutput{Body: toDto(job)}, nil
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
	out := &jobOutput{Body: toDto(job)}

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

func (m *Module) update(ctx context.Context, in *updateInput) (*jobOutput, error) {
	wid := principal.WorkspaceID(ctx)
	current, err := m.getJob(ctx, wid, in.ID)
	if err != nil {
		return nil, err
	}

	// The patch only replaces the fields it carries
	f := fieldsOf(current)
	in.Body.apply(&f)
	err = f.normalize()
	if err != nil {
		return nil, err
	}
	// Only a newly picked model is checked, so a job whose model was disabled since can still be edited
	if derefS(f.ModelID) != derefS(current.ModelID) {
		err = m.checkModel(ctx, wid, f.ModelID)
		if err != nil {
			return nil, err
		}
	}
	spec, _ := json.Marshal(f.Spec)
	limits, _ := json.Marshal(f.Limits)
	selfImprove, enabled := *f.SelfImprove, *f.Enabled

	_, err = m.queries.UpdateJob(ctx, jobsdb.UpdateJobParams{
		WorkspaceID:         wid,
		ID:                  in.ID,
		Name:                f.Name,
		Instruction:         f.Instruction,
		Spec:                string(spec),
		SpecOverrides:       current.SpecOverrides,
		ModelID:             f.ModelID,
		Image:               f.Image,
		Network:             f.Network,
		AllowedDomains:      domainsJSON(f.AllowedDomains),
		AllowPrivateNetwork: f.AllowPrivateNetwork,
		RunAsRoot:           f.RunAsRoot,
		Limits:              string(limits),
		SelfImprove:         selfImprove,
		ModePin:             f.ModePin,
		Concurrency:         f.Concurrency,
		Cron:                f.Cron,
		Timezone:            f.Timezone,
		Enabled:             enabled,
		UpdatedAt:           database.Now(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update job: %w", err)
	}

	// Any change can affect the schedule, so the actor re-arms it
	err = m.Reschedule(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	return m.get(ctx, &idInput{ID: in.ID})
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
	trigger := TriggerManual
	if p.UserID == "" {
		trigger = TriggerAPI
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
	items, total, err := listquery.Run(ctx, m.db, q, in.ToQuery(), func(rows *sql.Rows) (StateEntryDto, error) {
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
	row, err := m.queries.GetStateEntry(ctx, jobsdb.GetStateEntryParams{JobID: in.ID, Key: in.Key})
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
	return nil, m.ForJob("", in.ID).Set(ctx, in.Key, in.Body.Value)
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
	if !ok || token == "" {
		return nil, apperror.InvalidToken()
	}

	job, err := m.queries.GetJobUnscoped(ctx, in.JobID)
	if err != nil || job.ArchivedAt != nil || job.WebhookTokenHash == nil || *job.WebhookTokenHash != crypto.HashToken(strings.TrimSpace(token)) {
		return nil, apperror.InvalidToken()
	}

	// Webhooks are rate limited per job, and the limit holds across replicas because it is an actor
	if m.deps.WebhookLimiter != nil {
		allowed, retryAfter, err := m.deps.WebhookLimiter.Allow(ctx, "job:"+job.ID)
		if err == nil && !allowed {
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
	err = m.invoke(ctx, job.ID, methodTrigger, triggerInput{WorkspaceID: job.WorkspaceID, Request: runs.TriggerRequest{Trigger: TriggerWebhook, Input: input}}, &res)
	if err != nil {
		return nil, err
	}
	return &triggerOutput{Body: res}, nil
}

func deref(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func derefS(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
