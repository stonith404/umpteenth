package reflection

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/reflection/reflectiondb"
)

// Suggestion statuses
const (
	SuggestionOpen      = "open"
	SuggestionDone      = "done"
	SuggestionDismissed = "dismissed"
)

// dismissedFor is how long a dismissed suggestion keeps reflection from raising its subject again
const dismissedFor = 30 * 24 * time.Hour

// maxOpenSuggestions bounds the suggestions waiting on one job, so a confused reflection can't bury the owner
const maxOpenSuggestions = 10

// suggestion is a change to the job that a reflection asks the owner to make
type suggestion struct {
	Key  string
	Kind string
	Text string
	// Limits are the values a limits suggestion sets, and Before the job's values for the same limits when it was raised
	Limits *playbook.LimitChange
	Before *playbook.LimitChange
}

// screenSuggestions turns the held suggest operations into suggestions to store, rejecting subjects the owner dismissed lately and new ones beyond the cap
func screenSuggestions(results []playbook.AppliedOp, known []reflectiondb.SuggestionsForReflectionRow, current playbook.LimitChange) []suggestion {
	var dismissed, open []string
	for _, k := range known {
		if k.Status == SuggestionDismissed {
			dismissed = append(dismissed, k.Key)
		} else {
			open = append(open, k.Key)
		}
	}

	var list []suggestion
	added := 0
	for i := range results {
		r := &results[i]
		if r.Op.Op != playbook.OpSuggest || r.Status != playbook.OpHeld {
			continue
		}

		// The owner already said no to this subject, so it isn't raised again until the dismissal runs out
		if slices.Contains(dismissed, r.Target) {
			r.Status, r.Reason = playbook.OpRejected, "the owner dismissed this suggestion lately, so it isn't raised again"
			continue
		}

		// A new subject only counts against the cap once, however often one answer raises it
		listed := slices.IndexFunc(list, func(s suggestion) bool { return s.Key == r.Target })
		isNew := listed < 0 && !slices.Contains(open, r.Target)
		if isNew && len(open)+added >= maxOpenSuggestions {
			r.Status, r.Reason = playbook.OpRejected, fmt.Sprintf("the job already has %d open suggestions, update one of them by its name instead", maxOpenSuggestions)
			continue
		}

		// A limits suggestion keeps the job's values of the limits it changes, so the owner sees both sides of the change
		s := suggestion{Key: r.Target, Kind: strings.ToLower(strings.TrimSpace(derefString(r.Kind))), Text: strings.TrimSpace(derefString(r.Text))}
		if s.Kind == playbook.SuggestLimits {
			change, err := playbook.ParseLimitChange(derefString(r.Content))
			if err != nil {
				continue
			}
			before := change.Pick(current)
			s.Limits, s.Before = &change, &before
		}

		// When one answer raises the same subject twice, the later one wins
		if listed >= 0 {
			list[listed] = s
			continue
		}
		list = append(list, s)
		if isNew {
			added++
		}
	}
	return list
}

// storeSuggestions adds or updates the run's suggestions, inside the transaction that records the reflection's result
func storeSuggestions(ctx context.Context, q *reflectiondb.Queries, workspaceID, jobID, runID string, list []suggestion) error {
	now := database.Now()
	for _, s := range list {
		limits, before := limitsJSON(s.Limits), limitsJSON(s.Before)
		row, err := q.OpenSuggestionByKey(ctx, reflectiondb.OpenSuggestionByKeyParams{WorkspaceID: workspaceID, JobID: jobID, Key: s.Key})

		// A new subject becomes a new suggestion
		if database.IsNotFound(err) {
			err = q.InsertSuggestion(ctx, reflectiondb.InsertSuggestionParams{
				ID: database.NewID(), WorkspaceID: workspaceID, JobID: jobID, Key: s.Key, Kind: s.Kind, Text: s.Text,
				Limits: limits, LimitsBefore: before, RunID: &runID, CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				return fmt.Errorf("failed to add a suggestion: %w", err)
			}
			continue
		} else if err != nil {
			return fmt.Errorf("failed to load a suggestion: %w", err)
		}

		// A known subject takes the new wording, and counts the run only once, so learning from the same run again doesn't inflate it
		seen := row.Seen
		if row.RunID == nil || *row.RunID != runID {
			seen++
		}
		err = q.RefreshSuggestion(ctx, reflectiondb.RefreshSuggestionParams{
			Kind: s.Kind, Text: s.Text, Limits: limits, LimitsBefore: before, RunID: &runID, Seen: seen, UpdatedAt: now,
			WorkspaceID: workspaceID, ID: row.ID,
		})
		if err != nil {
			return fmt.Errorf("failed to update a suggestion: %w", err)
		}
	}
	return nil
}

func limitsJSON(l *playbook.LimitChange) *string {
	if l == nil {
		return nil
	}
	raw, _ := json.Marshal(l)
	return new(string(raw))
}

// parseLimits reads limits stored as JSON, where a missing or damaged value is left out
func parseLimits(raw *string) *playbook.LimitChange {
	if raw == nil {
		return nil
	}
	var l playbook.LimitChange
	if json.Unmarshal([]byte(*raw), &l) != nil {
		return nil
	}
	return &l
}

// SuggestionDto is a change reflection suggested to the job's owner
type SuggestionDto struct {
	ID           string                `json:"id"`
	Kind         string                `json:"kind" enum:"limits,credentials,instruction"`
	Key          string                `json:"key" doc:"The suggestion's subject, which later runs that raise it again update"`
	Text         string                `json:"text" doc:"What to change and what in the run showed it, written by reflection"`
	Limits       *playbook.LimitChange `json:"limits,omitempty" doc:"For kind limits: the limits applying the suggestion sets"`
	LimitsBefore *playbook.LimitChange `json:"limitsBefore,omitempty" doc:"For kind limits: the job's limits when the suggestion was raised"`
	RunID        *string               `json:"runId" doc:"The latest run whose reflection raised it"`
	RunNumber    *int64                `json:"runNumber" doc:"The number of that run, null once the run was deleted"`
	Seen         int64                 `json:"seen" doc:"How many runs' reflections raised it"`
	CreatedAt    int64                 `json:"createdAt"`
	UpdatedAt    int64                 `json:"updatedAt"`
}

type jobIDInput struct {
	ID string `path:"id"`
}

type suggestionsOutput struct {
	Body []SuggestionDto
}

// listSuggestions lists the job's open suggestions, newest first
func (m *Module) listSuggestions(ctx context.Context, in *jobIDInput) (*suggestionsOutput, error) {
	wid := principal.WorkspaceID(ctx)
	err := m.deps.JobEditor.JobExists(ctx, wid, in.ID)
	if err != nil {
		return nil, err
	}
	rows, err := m.queries.ListOpenSuggestions(ctx, reflectiondb.ListOpenSuggestionsParams{WorkspaceID: wid, JobID: in.ID})
	if err != nil {
		return nil, fmt.Errorf("failed to list suggestions: %w", err)
	}
	out := &suggestionsOutput{Body: make([]SuggestionDto, len(rows))}
	for i, r := range rows {
		out.Body[i] = SuggestionDto{
			ID: r.ID, Kind: r.Kind, Key: r.Key, Text: r.Text, Limits: parseLimits(r.Limits), LimitsBefore: parseLimits(r.LimitsBefore),
			RunID: r.RunID, RunNumber: r.RunNumber, Seen: r.Seen, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}
	}
	return out, nil
}

type suggestionInput struct {
	ID           string `path:"id"`
	SuggestionID string `path:"suggestionId"`
}

// openSuggestion loads a suggestion of a live job that is still waiting for a decision
func (m *Module) openSuggestion(ctx context.Context, wid string, in *suggestionInput) (reflectiondb.GetSuggestionRow, error) {
	err := m.deps.JobEditor.JobExists(ctx, wid, in.ID)
	if err != nil {
		return reflectiondb.GetSuggestionRow{}, err
	}
	row, err := m.queries.GetSuggestion(ctx, reflectiondb.GetSuggestionParams{WorkspaceID: wid, JobID: in.ID, ID: in.SuggestionID})
	if database.IsNotFound(err) {
		return row, apperror.NotFound("Suggestion")
	} else if err != nil {
		return row, fmt.Errorf("failed to load the suggestion: %w", err)
	}
	if row.Status != SuggestionOpen {
		return row, apperror.Conflict("This suggestion was already applied or dismissed")
	}
	return row, nil
}

// resolveSuggestion closes an open suggestion and reports whether this request was the one that closed it
func (m *Module) resolveSuggestion(ctx context.Context, wid string, in *suggestionInput, status string) (bool, error) {
	now := database.Now()
	n, err := m.queries.ResolveSuggestion(ctx, reflectiondb.ResolveSuggestionParams{Status: status, ResolvedAt: &now, UpdatedAt: now, WorkspaceID: wid, JobID: in.ID, ID: in.SuggestionID})
	if err != nil {
		return false, fmt.Errorf("failed to resolve the suggestion: %w", err)
	}
	return n > 0, nil
}

// applySuggestion marks a suggestion as done, after setting the limits a limits suggestion names
func (m *Module) applySuggestion(ctx context.Context, in *suggestionInput) (*struct{}, error) {
	wid := principal.WorkspaceID(ctx)

	// Only a suggestion that still waits for a decision can be applied
	row, err := m.openSuggestion(ctx, wid, in)
	if err != nil {
		return nil, err
	}

	// Limits are absolute values, so changing the job before resolving the suggestion leaves it open on failure and is harmless when two clicks race
	// A limit someone changed by hand since the suggestion was raised is kept, so applying an old suggestion never lowers it
	if row.Kind == playbook.SuggestLimits {
		change, err := playbook.ParseLimitChange(derefString(row.Limits))
		if err != nil {
			return nil, fmt.Errorf("failed to read the suggested limits: %w", err)
		}
		var before playbook.LimitChange
		if seen := parseLimits(row.LimitsBefore); seen != nil {
			before = *seen
		}
		err = m.deps.JobEditor.ApplyLimits(ctx, wid, in.ID, change, before)
		if err != nil {
			return nil, err
		}
	}

	// Someone who applied it meanwhile left the job with the same limits, so losing that race is no error
	resolved, err := m.resolveSuggestion(ctx, wid, in, SuggestionDone)
	if err != nil || resolved || row.Kind != playbook.SuggestLimits {
		return nil, err
	}

	// A dismissal that won the race came after this apply changed the job, so the suggestion is recorded as done like the job shows
	err = m.queries.OverruleDismissal(ctx, reflectiondb.OverruleDismissalParams{UpdatedAt: database.Now(), WorkspaceID: wid, JobID: in.ID, ID: in.SuggestionID})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve the suggestion: %w", err)
	}
	return nil, nil
}

// dismissSuggestion closes a suggestion without changing the job, and keeps reflection from raising its subject again for a while
func (m *Module) dismissSuggestion(ctx context.Context, in *suggestionInput) (*struct{}, error) {
	wid := principal.WorkspaceID(ctx)

	// Only a suggestion that still waits for a decision can be dismissed
	_, err := m.openSuggestion(ctx, wid, in)
	if err != nil {
		return nil, err
	}

	// Someone who applied it meanwhile already changed the job, which a dismissal must not hide
	resolved, err := m.resolveSuggestion(ctx, wid, in, SuggestionDismissed)
	if err != nil {
		return nil, err
	}
	if !resolved {
		return nil, apperror.Conflict("This suggestion was already applied or dismissed")
	}
	return nil, nil
}
