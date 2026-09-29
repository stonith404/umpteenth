//go:build unit

package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/runner"
)

func TestSetOutputBoundsWhatARunHoldsInMemory(t *testing.T) {
	live := &runner.LiveRun{}
	big := json.RawMessage(`"` + strings.Repeat("a", 600_000) + `"`)

	// One large value fits, a second one would push the total past the limit
	require.NoError(t, live.SetOutput("a", big))
	require.ErrorIs(t, live.SetOutput("b", big), runner.ErrRecordLimit)

	// Replacing a value only counts its growth, so overwriting the same key keeps working
	require.NoError(t, live.SetOutput("a", big))
	require.NoError(t, live.SetOutput("a", json.RawMessage(`1`)))
	require.NoError(t, live.SetOutput("b", big))

	// The number of distinct keys is bounded too
	many := &runner.LiveRun{}
	for i := range 100 {
		require.NoError(t, many.SetOutput(fmt.Sprint(i), json.RawMessage(`1`)))
	}
	require.ErrorIs(t, many.SetOutput("one more", json.RawMessage(`1`)), runner.ErrRecordLimit)
	require.NoError(t, many.SetOutput("0", json.RawMessage(`2`)))
}

func TestAddStepBoundsWhatARunHoldsInMemory(t *testing.T) {
	live := &runner.LiveRun{}
	require.ErrorIs(t, live.AddStep(strings.Repeat("a", 201)), runner.ErrRecordLimit)
	for range 1000 {
		require.NoError(t, live.AddStep("step"))
	}
	require.ErrorIs(t, live.AddStep("step"), runner.ErrRecordLimit)
}

func TestAcquireConnBoundsOpenProxyConnections(t *testing.T) {
	grant := &runner.ProxyGrant{}
	var releases []func()
	for {
		release, ok := grant.AcquireConn()
		if !ok {
			break
		}
		releases = append(releases, release)
		require.Less(t, len(releases), 10_000, "the proxy connections of a sandbox are bounded")
	}

	// A closed connection frees its slot for the next one
	releases[0]()
	_, ok := grant.AcquireConn()
	require.True(t, ok)
}

func TestProxyGrantsLastUntilRevoked(t *testing.T) {
	registry := runner.NewRegistry()
	grant := &runner.ProxyGrant{}
	revoke := registry.GrantProxy("token", grant)

	got, ok := registry.ProxyGrant("token")
	require.True(t, ok)
	assert.Same(t, grant, got)
	_, ok = registry.ProxyGrant("other-token")
	assert.False(t, ok)

	revoke()
	_, ok = registry.ProxyGrant("token")
	assert.False(t, ok)
}

func TestBrokerEventBoundsTheTimelineOfARun(t *testing.T) {
	live := &runner.LiveRun{}
	var recorded, markers int
	for range runner.MaxBrokerEvents + 500 {
		record, marker := live.BrokerEvent()
		require.False(t, record && marker)
		if record {
			recorded++
		}
		if marker {
			require.Equal(t, runner.MaxBrokerEvents, recorded, "the note follows the last recorded call")
			markers++
		}
	}
	require.Equal(t, runner.MaxBrokerEvents, recorded)
	require.Equal(t, 1, markers)
}

func TestAcquireRequestBoundsRequestsInFlight(t *testing.T) {
	live := &runner.LiveRun{}
	ctx := context.Background()
	var releases []func()
	for {
		waiting, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		release, err := live.AcquireRequest(waiting)
		cancel()
		if err != nil {
			require.ErrorIs(t, err, context.DeadlineExceeded)
			break
		}
		releases = append(releases, release)
		require.Less(t, len(releases), 1000, "the requests a run has in flight are bounded")
	}

	// A waiting request goes ahead once another one is done
	acquired := make(chan func())
	go func() {
		release, err := live.AcquireRequest(ctx)
		assert.NoError(t, err)
		acquired <- release
	}()
	releases[0]()
	select {
	case release := <-acquired:
		release()
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting request never got its turn")
	}
}

func TestAcquireRequestTurnsAwayRequestsOnceTooManyWait(t *testing.T) {
	live := &runner.LiveRun{}

	// fill sends requests that hold on until ctx ends, each in its own goroutine like a broker handler, and returns how many the run took before it turned one away
	fill := func(ctx context.Context, wg *sync.WaitGroup) int {
		for accepted := 0; ; accepted++ {
			require.Less(t, accepted, 1000, "the requests a run holds or queues are bounded")
			refused := make(chan error, 1)
			wg.Go(func() {
				release, err := live.AcquireRequest(ctx)
				if err != nil {
					refused <- err
					return
				}
				<-ctx.Done()
				release()
			})
			select {
			case err := <-refused:
				require.ErrorIs(t, err, runner.ErrTooManyRequests)
				return accepted
			case <-time.After(10 * time.Millisecond):
			}
		}
	}

	// Requests queue up beyond the ones in flight, and the run turns the next one away at once instead of letting it wait too
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	first := fill(ctx, &wg)
	cancel()
	wg.Wait()

	// Requests that gave up waiting leave the queue, so the next batch gets as far as the first
	ctx, cancel = context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	assert.Equal(t, first, fill(ctx, &wg))
}

func TestRecordActionReportsWhetherTheActionWasNoted(t *testing.T) {
	live := &runner.LiveRun{}
	noted := 0
	for i := range 500 {
		if live.RecordAction(fmt.Sprint(i)) {
			noted++
		}
	}
	require.Equal(t, len(live.Actions()), noted)
	require.Less(t, noted, 500)
}

func TestFirstStateWriteIsReportedOnce(t *testing.T) {
	live := &runner.LiveRun{}
	require.True(t, live.FirstStateWrite())
	require.False(t, live.FirstStateWrite())
}
