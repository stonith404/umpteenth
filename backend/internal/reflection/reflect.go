package reflection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/reflection/reflectiondb"
	"github.com/stonith404/umpteenth/backend/internal/runner"
)

const (
	// maxPreviousRuns bounds the run history reflection compares against
	maxPreviousRuns = 5
	// maxCandidates and maxCandidateChars bound the candidate scripts included in the prompt
	maxCandidates     = 10
	maxCandidateChars = 16_000
	// callTimeout bounds one model call, which can take minutes for a long structured answer on a local model
	callTimeout = 5 * time.Minute
	maxTokens   = 16_000
	// verifyTimeout bounds waiting for a proposed Dockerfile to build
	verifyTimeout = 20 * time.Minute
)

// reflect asks the model what the run taught the job and applies its answer as a new playbook version
func (m *Module) reflect(ctx context.Context, run reflectiondb.GetRunRow) (result, error) {
	res := result{Status: StatusDone}

	// The job's configuration comes with the playbook version the run used, but changes apply to the current playbook
	job, err := m.deps.Jobs.LoadForRun(ctx, run.WorkspaceID, run.JobID, run.PlaybookVersion)
	if err != nil {
		return res, fmt.Errorf("failed to load the job: %w", err)
	}
	baseVersion, current, err := m.deps.Playbook.Current(ctx, run.JobID)
	if err != nil {
		return res, err
	}

	// Resolve the reflection model, which defaults to the job's own model
	ws, err := m.deps.Settings.Get(ctx, run.WorkspaceID)
	if err != nil {
		return res, err
	}
	modelID := job.ModelID
	if ws.ReflectionModelID != nil && *ws.ReflectionModelID != "" {
		modelID = *ws.ReflectionModelID
	}
	if modelID == "" {
		return res, errors.New("no model is configured for reflection")
	}
	provider, model, err := m.deps.Models.Resolve(ctx, run.WorkspaceID, modelID)
	if err != nil {
		return res, fmt.Errorf("failed to load the reflection model: %w", err)
	}

	// Learning counts against the workspace's daily spend limit like runs do
	if ws.DailySpendLimit > 0 {
		spent, err := m.queries.SumSpentSince(ctx, reflectiondb.SumSpentSinceParams{WorkspaceID: run.WorkspaceID, Since: startOfDay(time.Now())})
		if err != nil {
			return res, fmt.Errorf("failed to check the daily spend limit: %w", err)
		}
		if spent >= int64(ws.DailySpendLimit*1e6) {
			return res, fmt.Errorf("the workspace reached its daily spend limit of $%.2f", ws.DailySpendLimit)
		}
	}

	// The run's events are loaded once, for the graduation check, the transcript and the shadow run
	runEvents, err := m.queries.RunEvents(ctx, run.ID)
	if err != nil {
		return res, fmt.Errorf("failed to load the run's events: %w", err)
	}

	// Whether the job may graduate is decided from its runs, so the model can't promote a job that isn't ready
	// A job with graduation turned off never runs a main script, so reflection doesn't spend tokens writing or repairing one
	grad := graduation{Off: true, Reason: "graduation is turned off for this job"}
	if job.Graduate {
		demoted := false
		if current.Main != nil {
			demoted, err = m.deps.Demotion.Demoted(ctx, run.WorkspaceID, run.JobID)
			if err != nil {
				return res, err
			}
		}
		grad, err = m.checkGraduation(ctx, run, runEvents, current, demoted)
		if err != nil {
			return res, err
		}
	}

	in, err := m.buildInput(ctx, run, runEvents, job, current)
	if err != nil {
		return res, err
	}
	in.Graduation = grad
	actx := playbook.ApplyContext{SourceRunID: run.ID, BaseImage: job.BaseImage, Network: job.Network, Secrets: slices.Collect(maps.Values(job.Env)), Graduation: grad.Eligible, NotGraduated: grad.Reason, GraduationOff: grad.Off, Inputs: job.InputNames}

	// Ask once, and once more with the problems when some operations didn't apply or the playbook grew over its budget
	messages := []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart(in.userMessage())}}}
	var (
		ans     answer
		next    playbook.Content
		results []playbook.AppliedOp
	)
	for attempt := range 2 {
		ans, err = m.ask(ctx, provider, model, messages, &res)
		if err != nil {
			return res, err
		}
		next, results = playbook.Apply(current, ans.Ops, actx)
		err = m.verifyDockerfile(ctx, run.JobID, current, &next, results)
		if err != nil {
			return res, err
		}
		err = m.verifyMain(ctx, run, runEvents, job, current, &next, results, &res)
		if err != nil {
			return res, err
		}
		over := next.OverBudget()
		if over == 0 && !slices.ContainsFunc(results, func(r playbook.AppliedOp) bool { return r.Status == playbook.OpRejected }) {
			break
		}
		if attempt == 1 && over > 0 {
			return res, fmt.Errorf("the playbook would be %d tokens over its budget", over)
		}
		previous, _ := json.Marshal(ans)
		messages = append(messages,
			llm.Message{Role: llm.RoleAssistant, Parts: []llm.Part{llm.TextPart(string(previous))}},
			llm.Message{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart(feedback(results, over))}},
		)
	}
	res.Summary, res.Ops = strings.TrimSpace(ans.Summary), results

	// Hits count how often runs relied on a learning, which reflection weighs when the list grows
	hits := knownLearnings(current, ans.UsedLearnings)
	if len(hits) > 0 {
		err = m.deps.Playbook.RecordStats(ctx, run.JobID, hits, nil)
		if err != nil {
			slog.WarnContext(ctx, "Failed to record learning hits", slog.String("run", run.ID), slog.Any("error", err))
		}
	}

	if !anyApplied(results) {
		return res, nil
	}

	// A reflection that lost its claim, e.g. to the reconciler or to a retry on another replica, must not write a version its run would never record
	claimed, err := m.stillClaimed(ctx, run)
	if err != nil {
		return res, err
	}
	if !claimed {
		return res, errors.New("the reflection ended or was taken over before its changes were saved")
	}
	res.Version, res.Ops, err = m.save(ctx, run, baseVersion, next, results, ans.Summary, actx)
	return res, err
}

// stillClaimed tells whether the run still waits for this reflection's result, which it does while it carries this claim's request time
func (m *Module) stillClaimed(ctx context.Context, run reflectiondb.GetRunRow) (bool, error) {
	current, err := m.queries.GetRun(ctx, reflectiondb.GetRunParams{WorkspaceID: run.WorkspaceID, ID: run.ID})
	if err != nil {
		return false, fmt.Errorf("failed to load the run: %w", err)
	}
	if current.Reflection != StatusPending {
		return false, nil
	}
	return run.ReflectionRequestedAt != nil && current.ReflectionRequestedAt != nil && *current.ReflectionRequestedAt == *run.ReflectionRequestedAt, nil
}

// askBackoff is the wait before each repeat of a transiently failed reflection call; reflection runs in the background, so it can wait out a provider outage
var askBackoff = []time.Duration{5 * time.Second, 20 * time.Second, time.Minute}

// ask makes one structured model call, repeating it after transient provider failures, and adds its cost and tokens to the result
func (m *Module) ask(ctx context.Context, provider llm.Provider, model runner.Model, messages []llm.Message, res *result) (answer, error) {
	for attempt := 0; ; attempt++ {
		ans, err := m.askOnce(ctx, provider, model, messages, res)
		if err == nil || attempt >= len(askBackoff) || !llm.IsTransient(err) || ctx.Err() != nil {
			return ans, err
		}
		slog.WarnContext(ctx, "Reflection call failed, retrying", slog.Int("attempt", attempt+1), slog.Any("error", err))
		select {
		case <-ctx.Done():
			return ans, ctx.Err()
		case <-time.After(askBackoff[attempt]):
		}
	}
}

func (m *Module) askOnce(ctx context.Context, provider llm.Provider, model runner.Model, messages []llm.Message, res *result) (answer, error) {
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var ans answer
	resp, err := llm.Structured(callCtx, provider, llm.Request{
		Model:        model.Name,
		System:       []llm.Block{{Text: systemPrompt, CacheBreakpoint: true}},
		Messages:     messages,
		MaxTokens:    maxTokens,
		Effort:       llm.EffortMedium,
		OutputSchema: outputSchema,
		OutputName:   "reflection",
	}, &ans)
	if resp != nil {
		res.Cost += llm.Cost(resp.Usage, model.Price)
		res.Tokens += resp.Usage.Tokens()
	}
	if err != nil {
		return ans, fmt.Errorf("the reflection model failed: %w", err)
	}
	return ans, nil
}

// save stores the new content as a reflection version and returns it with the final outcome of each operation
// A version written meanwhile, e.g. by a manual edit or a parallel run's reflection, is kept, and the operations are applied on top of it once more
func (m *Module) save(ctx context.Context, run reflectiondb.GetRunRow, baseVersion int64, next playbook.Content, results []playbook.AppliedOp, summary string, actx playbook.ApplyContext) (*int64, []playbook.AppliedOp, error) {
	for attempt := 0; ; attempt++ {
		ops, _ := json.Marshal(results)
		version, err := m.deps.Playbook.Save(ctx, run.WorkspaceID, run.JobID, next, playbook.VersionMeta{
			Author:      playbook.AuthorReflection,
			SourceRunID: &run.ID,
			Summary:     summaryOrDefault(summary, run.Number),
			Ops:         ops,
			BaseVersion: &baseVersion,
			Graduated: slices.ContainsFunc(results, func(r playbook.AppliedOp) bool {
				return r.Op.Op == playbook.OpProposeMain && r.Status == playbook.OpApplied
			}),
		})
		if err == nil {
			return &version, results, nil
		}
		if attempt > 0 || !apperror.IsCode(err, apperror.CodeConflict) {
			return nil, results, err
		}

		// Only the operations that were accepted are applied again, so a rejected or held one can't slip in on the retry
		var current playbook.Content
		baseVersion, current, err = m.deps.Playbook.Current(ctx, run.JobID)
		if err != nil {
			return nil, results, err
		}
		var accepted []int
		var acceptedOps []playbook.Op
		for i, r := range results {
			if r.Status == playbook.OpApplied {
				accepted, acceptedOps = append(accepted, i), append(acceptedOps, r.Op)
			}
		}
		var reapplied []playbook.AppliedOp
		next, reapplied = playbook.Apply(current, acceptedOps, actx)
		for j, r := range reapplied {
			r.Test = results[accepted[j]].Test
			results[accepted[j]] = r
		}
		if !anyApplied(results) {
			return nil, results, nil
		}
	}
}

// verifyDockerfile builds a Dockerfile reflection changed, and rejects the change when the image doesn't build
// A job whose environment can't be built can't run at all, so a broken Dockerfile must never become its playbook
func (m *Module) verifyDockerfile(ctx context.Context, jobID string, current playbook.Content, next *playbook.Content, results []playbook.AppliedOp) error {
	if next.Dockerfile == nil || (current.Dockerfile != nil && *current.Dockerfile == *next.Dockerfile) {
		return nil
	}
	err := errors.New("the sandbox adapter cannot build images")
	if m.deps.Images != nil {
		buildCtx, cancel := context.WithTimeout(ctx, verifyTimeout)
		err = m.deps.Images.Verify(buildCtx, jobID, *next.Dockerfile)
		cancel()
	}
	if err == nil {
		return nil
	}

	// A shutdown says nothing about the Dockerfile, so the reflection is retried instead
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for i := len(results) - 1; i >= 0; i-- {
		if results[i].Op.Op == playbook.OpSetDockerfile && results[i].Status == playbook.OpApplied {
			results[i].Status, results[i].Reason = playbook.OpRejected, "the image did not build: "+truncated(err.Error(), 2500)
			break
		}
	}
	next.Dockerfile = current.Dockerfile
	return nil
}

func anyApplied(results []playbook.AppliedOp) bool {
	return slices.ContainsFunc(results, func(r playbook.AppliedOp) bool { return r.Status == playbook.OpApplied })
}

func summaryOrDefault(summary string, number int64) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return fmt.Sprintf("Learned from run #%d", number)
	}
	return summary
}

// buildInput gathers the run, its transcript, its candidate scripts and the runs before it
// Everything the run produced is redacted before it is shortened, since a cut through a secret would leave the part before the cut unredacted
func (m *Module) buildInput(ctx context.Context, run reflectiondb.GetRunRow, runEvents []reflectiondb.RunEventsRow, job runner.JobConfig, current playbook.Content) (input, error) {
	redact := newRedactor(job.Env)
	tr := buildTranscript(runEvents, redact)

	in := input{
		Instruction:     job.Instruction,
		SuccessCriteria: job.SuccessCriteria,
		Inputs:          job.Inputs,
		Outputs:         job.Outputs,
		BaseImage:       job.BaseImage,
		Skills:          skillNames(job.Skills),
		Playbook:        current,
		Transcript:      tr,
		InstallHistory:  []Timings{tr.Timings},
		Run: runFacts{
			Number: run.Number, Status: run.Status, Mode: run.Mode, Turns: run.Turns, CostMicro: run.Cost,
			DurationMs: deref(run.MsTotal), Input: truncated(redact(derefString(run.Input)), 3000), Error: redact(derefString(run.Error)),
			Summary: redact(derefString(run.Summary)), Outputs: truncated(redact(derefString(run.Outputs)), 3000),
		},
	}
	in.Candidates = m.candidates(ctx, run.ID, redact)

	// Earlier runs show the trend, and their installs decide whether the environment rule applies
	previous, err := m.queries.RecentRuns(ctx, reflectiondb.RecentRunsParams{WorkspaceID: run.WorkspaceID, JobID: run.JobID, Number: run.Number, MaxRuns: maxPreviousRuns})
	if err != nil {
		return input{}, fmt.Errorf("failed to load earlier runs: %w", err)
	}
	for i, p := range previous {
		in.Previous = append(in.Previous, runFacts{Number: p.Number, Status: p.Status, Mode: p.Mode, Turns: p.Turns, CostMicro: p.Cost, DurationMs: deref(p.MsTotal)})
		if i < installRuleRuns-1 {
			rows, err := m.queries.RunEvents(ctx, p.ID)
			if err == nil {
				in.InstallHistory = append(in.InstallHistory, buildTranscript(rows, redact).Timings)
			}
		}
	}
	return in, nil
}

// candidates reads the scripts the run left in /ump/candidates
func (m *Module) candidates(ctx context.Context, runID string, redact func(string) string) []candidate {
	prefix := "runs/" + runID + "/candidates/"
	objects, err := m.deps.Storage.List(ctx, prefix)
	if err != nil {
		return nil
	}
	var out []candidate
	for _, o := range objects {
		if len(out) >= maxCandidates {
			break
		}
		r, _, err := m.deps.Storage.Open(ctx, o.Key)
		if err != nil {
			continue
		}
		// Reading past the limit lets a secret that crosses it still be recognized
		raw, err := io.ReadAll(io.LimitReader(r, 2*maxCandidateChars))
		_ = r.Close()
		if err != nil {
			continue
		}
		out = append(out, candidate{Name: strings.TrimPrefix(o.Key, prefix), Content: truncated(redact(string(raw)), maxCandidateChars)})
	}
	return out
}

// knownLearnings keeps the IDs that name existing learnings, since the model may cite one that doesn't exist
func knownLearnings(c playbook.Content, ids []string) []string {
	var out []string
	for _, id := range ids {
		if slices.ContainsFunc(c.Learnings, func(l playbook.Learning) bool { return l.ID == id }) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func startOfDay(t time.Time) int64 {
	y, mo, d := t.UTC().Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC).UnixMilli()
}

func deref(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// skillNames lists the names of the job's skills
func skillNames(skills []runner.Skill) []string {
	names := make([]string, len(skills))
	for i, s := range skills {
		names[i] = s.Name
	}
	return names
}
