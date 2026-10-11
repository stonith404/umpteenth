//go:build unit

package playbook_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

func TestApplyHoldsSuggestionsWithoutChangingThePlaybook(t *testing.T) {
	actx := playbook.ApplyContext{
		SourceRunID: "run-1", BaseImage: baseImage, Network: sandbox.NetworkInternet, Secrets: []string{"hunter2-secret"},
		Limits: playbook.LimitChange{TimeoutSeconds: new(600), MaxTurns: new(40), CPUs: new(2.0), MemoryMB: new(1024)},
	}
	c := playbook.Content{Learnings: []playbook.Learning{{ID: "L1", Kind: "fact", Text: "The API pages at 100", Status: "active"}}}
	c.Normalize()
	suggest := func(kind, name, text, content string) playbook.Op {
		op := playbook.Op{Op: playbook.OpSuggest, Kind: ptr(kind), Text: ptr(text)}
		if name != "" {
			op.Name = ptr(name)
		}
		if content != "" {
			op.Content = ptr(content)
		}
		return op
	}

	// Suggestions wait for the owner under their subject, and the playbook stays as it was
	next, res := playbook.Apply(c, []playbook.Op{
		suggest("limits", "", "The build ran out of memory at 1024 MB.", `{"memoryMb":4096}`),
		suggest("Instruction", "Email Skip-Rule", "The template says to skip the email on weekends, the instruction says to always send it.", ""),
		suggest("credentials", "github_token_workflow_scope", "GITHUB_TOKEN lacks the workflow scope the push needs.", ""),
	}, actx)
	require.Len(t, res, 3)
	for _, r := range res {
		assert.Equal(t, playbook.OpHeld, r.Status, r.Reason)
		assert.Empty(t, r.Flags)
	}
	assert.Equal(t, []string{"limits", "email_skip_rule", "github_token_workflow_scope"}, []string{res[0].Target, res[1].Target, res[2].Target})
	assert.Equal(t, c, next)

	// Every invalid suggestion is rejected with what to change, so the second attempt can fix it
	for _, tc := range []struct {
		op     playbook.Op
		reason string
	}{
		{suggest("budget", "more_money", "Raise the budget.", ""), "suggest needs kind limits, credentials or instruction"},
		{suggest("credentials", "github_token", "  ", ""), "suggest needs text"},
		{suggest("instruction", "long", strings.Repeat("x", 601), ""), "keep a suggestion under 600"},
		{suggest("credentials", "", "GITHUB_TOKEN lacks a scope.", ""), "short snake_case name"},
		{suggest("credentials", "token/scope", "GITHUB_TOKEN lacks a scope.", ""), "short snake_case name"},
		{suggest("instruction", "limits", "Which limits apply?", ""), "short snake_case name"},
		{suggest("limits", "", "More memory.", `{"memory": 4096}`), `such as {"memoryMb": 4096}`},
		{suggest("limits", "", "More memory.", ""), `such as {"memoryMb": 4096}`},
		{suggest("limits", "", "More memory.", `{"memoryMb": 10}`), "memoryMb must be between 64 and 262144"},
		{suggest("limits", "", "More memory.", `{"memoryMb": 1024}`), "the job already has memoryMb 1024, suggest only limits that change"},
		{suggest("credentials", "api_key", "Use hunter2-secret as the key.", ""), "name the secret's environment variable instead"},
		{suggest("credentials", "github_token", "Use ghp_abcdefghijklmnopqrstuvwxyz0123456789 instead.", ""), "name the secret's environment variable instead"},
	} {
		_, res := playbook.Apply(c, []playbook.Op{tc.op}, actx)
		require.Len(t, res, 1)
		assert.Equal(t, playbook.OpRejected, res[0].Status, tc.reason)
		assert.Contains(t, res[0].Reason, tc.reason)
	}
}
