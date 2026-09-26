package runs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/listquery"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runs/runsdb"
	"github.com/stonith404/umpteenth/backend/internal/storage"
)

// RunDto is a run as listed in tables
type RunDto struct {
	ID               string  `json:"id"`
	JobID            string  `json:"jobId"`
	JobName          string  `json:"jobName"`
	Number           int64   `json:"number"`
	Status           string  `json:"status"`
	Mode             string  `json:"mode"`
	Trigger          string  `json:"trigger"`
	QueuedAt         int64   `json:"queuedAt"`
	StartedAt        *int64  `json:"startedAt"`
	FinishedAt       *int64  `json:"finishedAt"`
	MsQueue          *int64  `json:"msQueue"`
	MsProvision      *int64  `json:"msProvision"`
	MsLLM            int64   `json:"msLlm"`
	MsTools          int64   `json:"msTools"`
	MsTotal          *int64  `json:"msTotal"`
	Turns            int64   `json:"turns"`
	TokIn            int64   `json:"tokIn"`
	TokOut           int64   `json:"tokOut"`
	TokCacheRead     int64   `json:"tokCacheRead"`
	TokCacheWrite    int64   `json:"tokCacheWrite"`
	Cost             int64   `json:"cost" doc:"Micro-USD"`
	ModelName        *string `json:"modelName" doc:"The model's ID at its provider, such as claude-sonnet-4-5"`
	ModelLabel       *string `json:"modelLabel" doc:"The model's display name: its label, or its ID when it has none"`
	Summary          *string `json:"summary"`
	Error            *string `json:"error"`
	SandboxIsolation *string `json:"sandboxIsolation"`
}

// RunDetailDto adds the fields only the run page needs
type RunDetailDto struct {
	RunDto
	Input           json.RawMessage `json:"input"`
	Instructions    *string         `json:"instructions"`
	Outputs         json.RawMessage `json:"outputs"`
	PlaybookVersion int64           `json:"playbookVersion"`
	ImageRef        *string         `json:"imageRef"`
	SandboxAdapter  *string         `json:"sandboxAdapter"`
	TriggeredBy     *string         `json:"triggeredBy"`
	TriggeredByName *string         `json:"triggeredByName" doc:"Name or email of the user who started the run"`
	JobDeleted      bool            `json:"jobDeleted" doc:"Whether the run's job was deleted, so the run can no longer be retried"`
	FellBack        bool            `json:"fellBack"`
	CancelRequested bool            `json:"cancelRequested"`
	Reflection      string          `json:"reflection" enum:"skipped,pending,done,failed"`
	// The outcome of reflection, shown on the run's Learned tab
	ReflectionError   *string              `json:"reflectionError"`
	ReflectionSummary *string              `json:"reflectionSummary"`
	ReflectionOps     []playbook.AppliedOp `json:"reflectionOps"`
	ReflectionVersion *int64               `json:"reflectionVersion" doc:"The playbook version reflection on this run created"`
	ReflectionCost    int64                `json:"reflectionCost" doc:"Micro-USD"`
	ReflectionTokens  int64                `json:"reflectionTokens" doc:"Input and output tokens of the reflection on this run, 0 for runs reflected on before tokens were recorded"`
	VerifyCost        int64                `json:"verifyCost" doc:"What the utility model's verification of a scripted run cost, in micro-USD"`
	VerifyTokens      int64                `json:"verifyTokens" doc:"Input and output tokens of the utility model's verification of a scripted run"`
}

// #nosec G101 -- column names such as tok_in are not credentials
const runColumns = `r.id, r.job_id, j.name, r.number, r.status, r.mode, r.trigger, r.queued_at, r.started_at, r.finished_at,
	r.ms_queue, r.ms_provision, r.ms_llm, r.ms_tools, r.ms_total, r.turns, r.tok_in, r.tok_out, r.tok_cache_read, r.tok_cache_write,
	r.cost, m.model, COALESCE(NULLIF(m.label, ''), m.model), r.summary, r.error, r.sandbox_isolation`

const runFrom = "FROM runs r JOIN jobs j ON j.id = r.job_id LEFT JOIN models m ON m.id = r.model_id"

// runKeyFrom is what sorting by job name needs, without the model lookup that only adds a display column
// The redundant workspace condition lets the planner walk the workspace's jobs by name and stop after one page
const runKeyFrom = "FROM runs r JOIN jobs j ON j.id = r.job_id AND j.workspace_id = r.workspace_id"

// #nosec G101 -- sort keys such as tokens are column names, not credentials
var listSpec = &listquery.Spec{
	Select: "SELECT " + runColumns + " " + runFrom,
	From:   runKeyFrom,
	// Every run has a job, and search reaches job names through a subquery, so counting never needs the join
	CountFrom: "FROM runs r",
	Key:       "r.id",
	Sorts: map[string]string{
		"queuedAt": "r.queued_at", "startedAt": "r.started_at", "finishedAt": "r.finished_at", "duration": "r.ms_total",
		"cost": "r.cost", "turns": "r.turns", "status": "r.status", "job": "j.name", "number": "r.number", "tokens": "(r.tok_in + r.tok_out)",
	},
	// Runs that have not started or finished yet sort last on both engines
	NullableSorts: []string{"startedAt", "finishedAt", "duration"},
	JoinSorts:     []string{"job"},
	DefaultSort:   "-queuedAt",
	Search:        []string{"r.summary", "r.error"},
	// The subquery is uncorrelated so it runs once per request, and the outer workspace filter keeps other tenants' jobs out
	SearchConds: []string{"r.job_id IN (SELECT id FROM jobs WHERE lower(name) LIKE {} ESCAPE '\\')"},
	TieBreaker:  "r.id",
}

func scanRun(rows interface{ Scan(...any) error }) (RunDto, error) {
	var d RunDto
	err := rows.Scan(&d.ID, &d.JobID, &d.JobName, &d.Number, &d.Status, &d.Mode, &d.Trigger, &d.QueuedAt, &d.StartedAt, &d.FinishedAt,
		&d.MsQueue, &d.MsProvision, &d.MsLLM, &d.MsTools, &d.MsTotal, &d.Turns, &d.TokIn, &d.TokOut, &d.TokCacheRead, &d.TokCacheWrite,
		&d.Cost, &d.ModelName, &d.ModelLabel, &d.Summary, &d.Error, &d.SandboxIsolation)
	return d, err
}

type listInput struct {
	httpserver.ListParams
	Status  string `query:"status" doc:"Comma-separated statuses"`
	Job     string `query:"job" doc:"Comma-separated job IDs"`
	Mode    string `query:"mode" doc:"Comma-separated modes"`
	Trigger string `query:"trigger" doc:"Comma-separated triggers"`
	From    int64  `query:"from" doc:"Queued at or after, unix ms"`
	To      int64  `query:"to" doc:"Queued before, unix ms"`
}

func (m *Module) list(ctx context.Context, in *listInput) (*httpserver.PaginatedOutput[RunDto], error) {
	q := listquery.New(listSpec).WhereEq("r.workspace_id", principal.WorkspaceID(ctx))
	q.WhereIn("r.status", listquery.SplitCSV(in.Status))
	q.WhereIn("r.job_id", listquery.SplitCSV(in.Job))
	q.WhereIn("r.mode", listquery.SplitCSV(in.Mode))
	q.WhereIn("r.trigger", listquery.SplitCSV(in.Trigger))
	if in.From > 0 {
		q.Where("r.queued_at >= " + q.Arg(in.From))
	}
	if in.To > 0 {
		q.Where("r.queued_at < " + q.Arg(in.To))
	}

	items, total, err := listquery.Run(ctx, m.deps.DB, q, in.ToQuery(), func(rows *sql.Rows) (RunDto, error) { return scanRun(rows) })
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

type idInput struct {
	ID string `path:"id"`
}

// getRun loads a run of the workspace, which also scopes the events and files stored under the run
func (m *Module) getRun(ctx context.Context, workspaceID, runID string) (runsdb.Run, error) {
	run, err := m.queries.GetRun(ctx, runsdb.GetRunParams{WorkspaceID: workspaceID, ID: runID})
	if database.IsNotFound(err) {
		return runsdb.Run{}, apperror.NotFound("Run")
	} else if err != nil {
		return runsdb.Run{}, fmt.Errorf("failed to load run: %w", err)
	}
	return run, nil
}

type getOutput struct {
	Body RunDetailDto
}

func (m *Module) get(ctx context.Context, in *idInput) (*getOutput, error) {
	wid := principal.WorkspaceID(ctx)
	row := m.deps.DB.QueryRowContext(ctx, "SELECT "+runColumns+" "+runFrom+" WHERE r.workspace_id = $1 AND r.id = $2", wid, in.ID)
	d, err := scanRun(row)
	if database.IsNotFound(err) {
		return nil, apperror.NotFound("Run")
	} else if err != nil {
		return nil, fmt.Errorf("failed to load run: %w", err)
	}

	run, err := m.getRun(ctx, wid, in.ID)
	if err != nil {
		return nil, err
	}
	out := &getOutput{Body: RunDetailDto{
		RunDto:            d,
		Instructions:      run.Instructions,
		PlaybookVersion:   run.PlaybookVersion,
		ImageRef:          run.ImageRef,
		SandboxAdapter:    run.SandboxAdapter,
		TriggeredBy:       run.TriggeredBy,
		FellBack:          run.FellBack,
		CancelRequested:   run.CancelRequested,
		Reflection:        run.Reflection,
		ReflectionError:   run.ReflectionError,
		ReflectionSummary: run.ReflectionSummary,
		ReflectionOps:     []playbook.AppliedOp{},
		ReflectionVersion: run.ReflectionVersion,
		ReflectionCost:    run.ReflectionCost,
		ReflectionTokens:  run.ReflectionTokens,
		VerifyCost:        run.VerifyCost,
		VerifyTokens:      run.VerifyTokens,
	}}
	if run.ReflectionOps != nil {
		_ = json.Unmarshal([]byte(*run.ReflectionOps), &out.Body.ReflectionOps)
	}
	// Deleting a job keeps its runs, but a retry would find no job to start
	err = m.deps.DB.QueryRowContext(ctx, "SELECT archived_at IS NOT NULL FROM jobs WHERE workspace_id = $1 AND id = $2", wid, d.JobID).Scan(&out.Body.JobDeleted)
	if err != nil {
		return nil, fmt.Errorf("failed to load the run's job: %w", err)
	}
	if run.TriggeredBy != nil {
		// A deleted user leaves the name empty, which the UI shows as an unknown user
		var name *string
		err = m.deps.DB.QueryRowContext(ctx, "SELECT COALESCE(name, email) FROM users WHERE id = $1", *run.TriggeredBy).Scan(&name)
		if err != nil && !database.IsNotFound(err) {
			return nil, fmt.Errorf("failed to load the run's user: %w", err)
		}
		out.Body.TriggeredByName = name
	}
	if run.Input != nil {
		out.Body.Input = json.RawMessage(*run.Input)
	}
	if run.Outputs != nil {
		out.Body.Outputs = json.RawMessage(*run.Outputs)
	}
	return out, nil
}

// EventDto is one timeline entry
type EventDto struct {
	Seq          int64           `json:"seq"`
	TS           int64           `json:"ts"`
	Type         string          `json:"type"`
	SpanID       *string         `json:"spanId"`
	ParentSpanID *string         `json:"parentSpanId"`
	Ms           *int64          `json:"ms"`
	Payload      json.RawMessage `json:"payload"`
}

type listEventsInput struct {
	ID    string `path:"id"`
	After int64  `query:"after" doc:"Return events with a sequence number greater than this"`
	Limit int64  `query:"limit" default:"500" minimum:"1" maximum:"2000"`
}

type listEventsOutput struct {
	Body []EventDto
}

func (m *Module) listEvents(ctx context.Context, in *listEventsInput) (*listEventsOutput, error) {
	// Events are scoped through their run, so the run is checked against the workspace first
	_, err := m.getRun(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}
	evs, err := m.loadEvents(ctx, in.ID, in.After, in.Limit)
	if err != nil {
		return nil, err
	}
	return &listEventsOutput{Body: evs}, nil
}

func (m *Module) loadEvents(ctx context.Context, runID string, after, limit int64) ([]EventDto, error) {
	rows, err := m.queries.ListRunEvents(ctx, runsdb.ListRunEventsParams{RunID: runID, AfterSeq: after, MaxRows: int32(min(limit, 5000))}) // #nosec G115 -- clamped to 5000
	if err != nil {
		return nil, fmt.Errorf("failed to load events: %w", err)
	}
	out := make([]EventDto, len(rows))
	for i, r := range rows {
		out[i] = EventDto{Seq: r.Seq, TS: r.Ts, Type: r.Type, SpanID: r.SpanID, ParentSpanID: r.ParentSpanID, Ms: r.Ms, Payload: json.RawMessage(r.Payload)}
	}
	return out, nil
}

func (m *Module) cancel(ctx context.Context, in *idInput) (*struct{}, error) {
	return nil, m.Cancel(ctx, principal.WorkspaceID(ctx), in.ID)
}

func (m *Module) deleteOne(ctx context.Context, in *idInput) (*struct{}, error) {
	return nil, m.Delete(ctx, principal.WorkspaceID(ctx), in.ID)
}

type deleteRunsInput struct {
	Body struct {
		IDs []string `json:"ids" minItems:"1" maxItems:"100" doc:"The runs to delete"`
	}
}

type deleteRunsOutput struct {
	Body struct {
		Deleted []string `json:"deleted" doc:"The runs that were deleted"`
		Skipped []string `json:"skipped" doc:"The runs that were left alone because they don't exist or haven't finished"`
	}
}

func (m *Module) deleteMany(ctx context.Context, in *deleteRunsInput) (*deleteRunsOutput, error) {
	deleted, skipped, err := m.DeleteMany(ctx, principal.WorkspaceID(ctx), in.Body.IDs)
	if err != nil {
		return nil, err
	}
	out := &deleteRunsOutput{}
	out.Body.Deleted, out.Body.Skipped = deleted, skipped
	return out, nil
}

type retryOutput struct {
	Body TriggerResult
}

func (m *Module) retry(ctx context.Context, in *idInput) (*retryOutput, error) {
	wid := principal.WorkspaceID(ctx)
	run, err := m.getRun(ctx, wid, in.ID)
	if err != nil {
		return nil, err
	}

	req := TriggerRequest{Trigger: TriggerRetry, TriggeredBy: principal.UserIDPtr(ctx)}
	if run.Input != nil {
		req.Input = json.RawMessage(*run.Input)
	}
	if run.Instructions != nil {
		req.Instructions = *run.Instructions
	}
	res, err := m.jobs.Trigger(ctx, wid, run.JobID, req)
	if err != nil {
		return nil, err
	}
	return &retryOutput{Body: res}, nil
}

type artifactDto struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type listArtifactsOutput struct {
	Body []artifactDto
}

func (m *Module) listArtifacts(ctx context.Context, in *idInput) (*listArtifactsOutput, error) {
	_, err := m.getRun(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}

	prefix := "runs/" + in.ID + "/artifacts/"
	objects, err := m.deps.Storage.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := &listArtifactsOutput{Body: []artifactDto{}}
	for _, o := range objects {
		out.Body = append(out.Body, artifactDto{Name: strings.TrimPrefix(o.Key, prefix), Size: o.Size})
	}
	return out, nil
}

type getArtifactInput struct {
	ID   string `path:"id"`
	Path string `query:"path" required:"true"`
}

func (m *Module) getArtifact(ctx context.Context, in *getArtifactInput) (*huma.StreamResponse, error) {
	_, err := m.getRun(ctx, principal.WorkspaceID(ctx), in.ID)
	if err != nil {
		return nil, err
	}

	name := strings.TrimPrefix(path.Clean("/"+in.Path), "/")
	r, size, err := m.deps.Storage.Open(ctx, "runs/"+in.ID+"/artifacts/"+name)
	if errors.Is(err, storage.ErrNotExist) {
		return nil, apperror.NotFound("Artifact")
	} else if err != nil {
		return nil, err
	}

	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		defer r.Close()
		ct := mime.TypeByExtension(path.Ext(name))
		if ct == "" {
			ct = "application/octet-stream"
		}
		hctx.SetHeader("Content-Type", ct)
		hctx.SetHeader("Content-Length", strconv.FormatInt(size, 10))
		hctx.SetHeader("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(name)}))
		_, _ = io.Copy(hctx.BodyWriter(), r)
	}}, nil
}
