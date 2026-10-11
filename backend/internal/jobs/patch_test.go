//go:build unit

package jobs

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/jobs/jobsdb"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
)

func TestPatchKeepsOmittedFieldsAndClearsEmptyOnes(t *testing.T) {
	current := jobsdb.Job{
		Name:           "Old",
		Instruction:    "Do it",
		Spec:           `{"title":"Kept","goal":"g"}`,
		ModelID:        new("model-1"),
		Image:          new("alpine"),
		Network:        "none",
		AllowedDomains: "[]",
		Limits:         `{"maxTurns":5}`,
		SelfImprove:    false,
		Concurrency:    ConcurrencyQueue,
		Cron:           new("*/5 * * * *"),
		Timezone:       new("Europe/Berlin"),
		RunAsRoot:      true,
	}
	fields := func() jobFields {
		t.Helper()
		f, err := fieldsOf(current)
		require.NoError(t, err)
		return f
	}

	// Only the name changes, everything else survives the update
	f := fields()
	jobPatch{Name: new("New")}.apply(&f)
	require.NoError(t, f.normalize())
	require.Equal(t, "New", f.Name)
	require.Equal(t, "Do it", f.Instruction)
	require.Equal(t, "Kept", f.Spec.Title)
	require.Equal(t, "model-1", *f.ModelID)
	require.Equal(t, "alpine", *f.Image)
	require.Equal(t, "none", f.Network)
	require.Equal(t, 5, *f.Limits.MaxTurns)
	require.False(t, *f.SelfImprove)
	require.Equal(t, ConcurrencyQueue, f.Concurrency)
	require.Equal(t, "*/5 * * * *", *f.Cron)
	require.True(t, f.RunAsRoot)

	// Empty strings clear optional fields, and false is applied rather than ignored
	f = fields()
	jobPatch{Cron: new(""), Timezone: new(""), ModelID: new(""), Image: new(""), RunAsRoot: new(false)}.apply(&f)
	require.NoError(t, f.normalize())
	require.Nil(t, f.Cron)
	require.Nil(t, f.Timezone)
	require.Nil(t, f.ModelID)
	require.Nil(t, f.Image)
	require.False(t, f.RunAsRoot)

	// Whitespace-only text is empty once trimmed and is rejected
	f = fields()
	jobPatch{Name: new("   ")}.apply(&f)
	require.Error(t, f.normalize())
}

func TestDamagedJSONColumnsAreReportedRatherThanDropped(t *testing.T) {
	// Silently dropping unparsable limits would run the job with the workspace defaults instead of its own
	_, err := toDto(jobsdb.Job{ID: "j", Spec: "{}", Limits: `{"maxCostUsd":`, AllowedDomains: "[]"})
	require.ErrorContains(t, err, "limits")
}

func TestUSDToMicroRounds(t *testing.T) {
	require.EqualValues(t, 290_000, usdToMicro(0.29))
	require.EqualValues(t, 2_000_000, usdToMicro(2))
	require.EqualValues(t, 0, usdToMicro(0))
}

func TestAllowlistDomains(t *testing.T) {
	domains, err := normalizeDomains([]string{" API.Example.com. ", "*.cdn.example.net", "api.example.com", ""})
	require.NoError(t, err)
	require.Equal(t, []string{"api.example.com", "*.cdn.example.net"}, domains)
	for _, bad := range []string{"https://example.com", "example.com:443", "10.0.0.1", "exa mple.com", "*.", "a/b"} {
		_, err := normalizeDomains([]string{bad})
		require.Error(t, err, bad)
	}

	// An allow-list job without domains could reach nothing, which is a mistake rather than a policy
	f := jobFields{Name: "n", Instruction: "i", Network: "allowlist"}
	require.Error(t, f.normalize())
	f.AllowedDomains = []string{"api.example.com"}
	require.NoError(t, f.normalize())
}

func TestApplyLimitsKeepsTheJobsOtherLimits(t *testing.T) {
	f := newCompileFixture(t)
	ctx := t.Context()

	// The owner gave the job a longer timeout before reflection suggested more memory
	applied, err := f.m.applyPatch(ctx, f.wid, f.jobID, jobPatch{Limits: &LimitOverrides{TimeoutSeconds: new(1200)}}.apply)
	require.NoError(t, err)
	require.True(t, applied)

	// Applying the suggestion sets the memory and keeps the timeout
	limits := func() LimitOverrides {
		job, err := f.m.getJob(ctx, f.wid, f.jobID)
		require.NoError(t, err)
		d, err := toDto(job)
		require.NoError(t, err)
		return d.Limits
	}
	require.NoError(t, f.m.ApplyLimits(ctx, f.wid, f.jobID, playbook.LimitChange{MemoryMB: new(4096)}, playbook.LimitChange{MemoryMB: new(1024)}))
	require.Equal(t, LimitOverrides{TimeoutSeconds: new(1200), MemoryMB: new(4096)}, limits())

	// A limit the owner changed by hand since the suggestion was raised stays, while the others it names still apply
	require.NoError(t, f.m.ApplyLimits(ctx, f.wid, f.jobID, playbook.LimitChange{MemoryMB: new(2048), MaxTurns: new(80)}, playbook.LimitChange{MemoryMB: new(1024), MaxTurns: new(40)}))
	require.Equal(t, LimitOverrides{TimeoutSeconds: new(1200), MemoryMB: new(4096), MaxTurns: new(80)}, limits())

	// A job that is gone can't take the limits
	require.Error(t, f.m.ApplyLimits(ctx, f.wid, "missing", playbook.LimitChange{MemoryMB: new(4096)}, playbook.LimitChange{}))
}

func TestAChangedInstructionIsStamped(t *testing.T) {
	f := newCompileFixture(t)
	ctx := t.Context()
	stamp := func() *int64 {
		job, err := f.m.getJob(ctx, f.wid, f.jobID)
		require.NoError(t, err)
		return job.InstructionChangedAt
	}

	// Other edits leave the stamp alone, while a new instruction sets it, so reflection can bring a main script in line
	applied, err := f.m.applyPatch(ctx, f.wid, f.jobID, jobPatch{Name: new("Renamed")}.apply)
	require.NoError(t, err)
	require.True(t, applied)
	require.Nil(t, stamp())
	applied, err = f.m.applyPatch(ctx, f.wid, f.jobID, jobPatch{Instruction: new("Post stale PRs of acme/api to #eng, and email the owners")}.apply)
	require.NoError(t, err)
	require.True(t, applied)
	require.NotNil(t, stamp())
}
