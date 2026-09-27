package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs/runsdb"
)

// store implements runner.RunStore on the runs table
type store struct {
	queries *runsdb.Queries
}

func (s *store) Load(ctx context.Context, runID string) (runner.Run, error) {
	row, err := s.queries.GetRunUnscoped(ctx, runID)
	if err != nil {
		return runner.Run{}, err
	}
	return toRunnerRun(row), nil
}

func toRunnerRun(row runsdb.Run) runner.Run {
	r := runner.Run{
		ID:              row.ID,
		WorkspaceID:     row.WorkspaceID,
		JobID:           row.JobID,
		Status:          row.Status,
		Mode:            row.Mode,
		Trigger:         row.Trigger,
		PlaybookVersion: row.PlaybookVersion,
	}
	if row.Input != nil {
		r.Input = json.RawMessage(*row.Input)
	}
	if row.Instructions != nil {
		r.Instructions = *row.Instructions
	}
	return r
}

func (s *store) Claim(ctx context.Context, runID, hostID string) (bool, error) {
	n, err := s.queries.ClaimRun(ctx, runsdb.ClaimRunParams{ID: runID, HostID: &hostID, StartedAt: new(database.Now())})
	return n == 1, err
}

func (s *store) SetStatus(ctx context.Context, runID, status string) error {
	n, err := s.queries.SetRunStatus(ctx, runsdb.SetRunStatusParams{ID: runID, Status: status})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("run %s is no longer live", runID)
	}
	return nil
}

func (s *store) SetSandbox(ctx context.Context, runID string, rec runner.SandboxRecord) error {
	return s.queries.SetRunSandbox(ctx, runsdb.SetRunSandboxParams{
		ID:               runID,
		SandboxAdapter:   &rec.Adapter,
		SandboxID:        &rec.SandboxID,
		SandboxIsolation: &rec.Isolation,
		ImageRef:         nilIfEmpty(rec.ImageRef),
		ImageID:          nilIfEmpty(rec.ImageID),
		ModelID:          nilIfEmpty(rec.ModelID),
		MsProvision:      &rec.MsProvision,
	})
}

func (s *store) SetBrokerToken(ctx context.Context, runID, tokenHash string) error {
	return s.queries.SetRunBrokerToken(ctx, runsdb.SetRunBrokerTokenParams{ID: runID, BrokerTokenHash: &tokenHash})
}

func (s *store) Heartbeat(ctx context.Context, runID string) (bool, error) {
	return s.queries.HeartbeatRun(ctx, runsdb.HeartbeatRunParams{ID: runID, HeartbeatAt: new(database.Now())})
}

func (s *store) Finish(ctx context.Context, runID string, f runner.Final) (bool, error) {
	var outputs *string
	if len(f.Outputs) > 0 {
		outputs = new(validText(string(f.Outputs)))
	}
	reflection := f.Reflection
	if reflection == "" {
		reflection = runner.ReflectionSkipped
	}
	now := database.Now()
	var requestedAt *int64
	if reflection == runner.ReflectionPending {
		requestedAt = &now
	}
	n, err := s.queries.FinishRun(ctx, runsdb.FinishRunParams{
		ID:                    runID,
		Status:                f.Status,
		FinishedAt:            &now,
		MsTotal:               nilIfZero(f.MsTotal),
		MsLlm:                 f.MsLLM,
		MsTools:               f.MsTools,
		Turns:                 int64(f.Turns),
		TokIn:                 f.Usage.Input,
		TokOut:                f.Usage.Output,
		TokCacheRead:          f.Usage.CacheRead,
		TokCacheWrite:         f.Usage.CacheWrite,
		Cost:                  f.Cost,
		Summary:               nilIfEmpty(validText(f.Summary)),
		Outputs:               outputs,
		Error:                 nilIfEmpty(validText(f.Error)),
		Reflection:            reflection,
		ReflectionRequestedAt: requestedAt,
		FellBack:              f.FellBack,
		VerifyCost:            f.VerifyCost,
		VerifyTokens:          f.VerifyTokens,
	})
	return n == 1, err
}

func (s *store) SetFellBack(ctx context.Context, runID string) error {
	return s.queries.SetRunFellBack(ctx, runID)
}

func (s *store) SpentSince(ctx context.Context, workspaceID string, since int64) (int64, error) {
	return s.queries.SumCostSince(ctx, runsdb.SumCostSinceParams{WorkspaceID: workspaceID, Since: since})
}

// validText drops what Postgres refuses in text columns, since summaries and outputs can carry any bytes a script printed
// A refused write would leave the run without a result until the reconciler failed it as interrupted
func validText(s string) string {
	return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "\uFFFD")
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nilIfZero(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}
