//go:build unit

package broker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// llmHarness serves the broker for one live run whose models are a fake provider priced at one micro-USD per token, so costs read as token counts
type llmHarness struct {
	provider *fake.Provider
	live     *runner.LiveRun
	url      string
}

func newLLMHarness(t *testing.T, maxCost int64) *llmHarness {
	t.Helper()
	p := fake.New()
	model := runner.Model{Name: "fake-model", Price: llm.Price{In: 1_000_000, Out: 1_000_000}}
	live := &runner.LiveRun{Run: runner.Run{ID: "run-1"}, Job: runner.JobConfig{Limits: runner.Limits{MaxCost: maxCost}}, Provider: p, Model: model, Utility: p, UtilModel: model}
	reg := runner.NewRegistry()
	reg.Register("run-1", live)
	b := New(Dependencies{Runs: tokenRuns{crypto.HashToken("run-token"): "run-1"}, Live: reg})
	server := httptest.NewServer(b.Handler())
	t.Cleanup(server.Close)
	return &llmHarness{provider: p, live: live, url: server.URL}
}

// call sends one ump llm request the way the sandbox does and returns the status code
func (h *llmHarness) call(ctx context.Context, body string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url+"/v1/llm", strings.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer run-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode, nil
}

func (h *llmHarness) cost() int64 {
	_, cost := h.live.Spend()
	return cost
}

// hangUp sends one ump llm request and closes the connection as soon as the provider has it, like a sandbox giving up on the call
func (h *llmHarness) hangUp(t *testing.T, body string) {
	t.Helper()
	seen := len(h.provider.Requests())
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for len(h.provider.Requests()) == seen {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	_, err := h.call(ctx, body)
	require.ErrorIs(t, err, context.Canceled)
}

func TestLLMCallOutlivesTheSandboxHangingUp(t *testing.T) {
	h := newLLMHarness(t, 0)
	h.provider.Enqueue(fake.ScriptedResponse{Text: "The build passed.", Usage: llm.Usage{Input: 300, Output: 200}, Delay: 300 * time.Millisecond})

	// The sandbox gives up on the call as soon as the provider has it
	h.hangUp(t, `{"prompt":"Summarize the build"}`)

	// The provider still answers and bills the call, so the run is charged what it actually cost
	require.Eventually(t, func() bool { return h.cost() == 500 }, 5*time.Second, 5*time.Millisecond)
	usage, _ := h.live.Spend()
	assert.Equal(t, llm.Usage{Input: 300, Output: 200}, usage)
}

func TestLLMCallStillRunningWhenTheRunEndsCountsWithItsReservation(t *testing.T) {
	// A 2000-byte prompt with 1000 output tokens reserves 1000 input and 1000 output tokens
	h := newLLMHarness(t, 0)
	body := fmt.Sprintf(`{"prompt":%q,"maxTokens":1000}`, strings.Repeat("p", 2000))
	h.provider.Enqueue(fake.ScriptedResponse{Text: "The build passed.", Usage: llm.Usage{Input: 1000, Output: 400}, Delay: time.Second})

	// The sandbox hangs up and exits, so the run's cost is recorded while the provider is still answering
	h.hangUp(t, body)
	_, recorded := h.live.FinalSpend()
	assert.Equal(t, int64(2000), recorded)

	// The provider bills the call only after that, and its reservation covered what it cost
	require.Eventually(t, func() bool { return h.cost() == 1400 }, 5*time.Second, 5*time.Millisecond)

	// A call starting after the run's cost was recorded would count nowhere, so the broker refuses it
	status, err := h.call(context.Background(), body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Len(t, h.provider.Requests(), 1)
}

func TestLLMCallChargesCallsThatFailAfterTheProviderStarted(t *testing.T) {
	// A 2000-byte prompt with 1000 output tokens reserves 1000 input and 1000 output tokens
	h := newLLMHarness(t, 7000)
	body := fmt.Sprintf(`{"prompt":%q,"maxTokens":1000}`, strings.Repeat("p", 2000))

	// A request the provider turned away with a client error did no billable work, so it costs nothing
	h.provider.Enqueue(fake.ScriptedResponse{Error: "rate limited", ErrorStatus: http.StatusTooManyRequests})
	status, err := h.call(context.Background(), body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadGateway, status)
	assert.Zero(t, h.cost())

	// Calls that fail like a dropped stream report no usage, so each is charged its reservation until the limit stops them
	for range 10 {
		h.provider.Enqueue(fake.ScriptedResponse{Error: "stream ended before the message was complete"})
	}
	var statuses []int
	for range 10 {
		status, err := h.call(context.Background(), body)
		require.NoError(t, err)
		statuses = append(statuses, status)
		if status == http.StatusForbidden {
			break
		}
	}
	assert.Equal(t, []int{http.StatusBadGateway, http.StatusBadGateway, http.StatusBadGateway, http.StatusBadGateway, http.StatusForbidden}, statuses)
	assert.Equal(t, int64(4*2000), h.cost())
	assert.Len(t, h.provider.Requests(), 5)
}

func TestLLMCallDoesNotChargeCallsTheProviderNeverAnswered(t *testing.T) {
	h := newLLMHarness(t, 7000)
	body := fmt.Sprintf(`{"prompt":%q,"maxTokens":1000}`, strings.Repeat("p", 2000))

	// An overloaded provider answers with an error status before generating anything
	h.provider.Enqueue(fake.ScriptedResponse{Error: "overloaded", ErrorStatus: 529})
	status, err := h.call(context.Background(), body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadGateway, status)
	assert.Zero(t, h.cost())

	// A schema that isn't an object is refused before anything is reserved or sent
	status, err = h.call(context.Background(), `{"prompt":"hi","schema":[1,2]}`)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Zero(t, h.cost())
	assert.Len(t, h.provider.Requests(), 1)
}

func TestLLMCallChargesAStructuredRetryThatFails(t *testing.T) {
	// The prompt and the 17-byte schema come to 1000 estimated tokens, so both attempts together reserve 3000 input and 2000 output tokens
	body := fmt.Sprintf(`{"prompt":%q,"schema":{"type":"object"},"maxTokens":1000}`, strings.Repeat("p", 1983))
	firstAttempt := fake.ScriptedResponse{Text: "Here is the object you asked for.", Usage: llm.Usage{Input: 1000, Output: 1000}}

	t.Run("dropped retry", func(t *testing.T) {
		// The first attempt is paid for and the failed retry may have been, so the call is charged the whole reservation
		h := newLLMHarness(t, 0)
		h.provider.Enqueue(firstAttempt, fake.ScriptedResponse{Error: "stream ended before the message was complete"})
		status, err := h.call(context.Background(), body)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadGateway, status)
		assert.Equal(t, int64(5000), h.cost())
		usage, _ := h.live.Spend()
		assert.Equal(t, llm.Usage{Input: 1000, Output: 1000}, usage)
	})

	t.Run("rejected retry", func(t *testing.T) {
		// A retry the provider turned away cost nothing, so only the first attempt is charged
		h := newLLMHarness(t, 0)
		h.provider.Enqueue(firstAttempt, fake.ScriptedResponse{Error: "rate limited", ErrorStatus: http.StatusTooManyRequests})
		status, err := h.call(context.Background(), body)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadGateway, status)
		assert.Equal(t, int64(2000), h.cost())
	})
}

func TestLLMCallReservesBothAttemptsOfAStructuredAnswer(t *testing.T) {
	// The prompt and schema come to 100 estimated tokens, so one attempt reserves 1100 tokens and both together 3200
	h := newLLMHarness(t, 7000)
	body := fmt.Sprintf(`{"prompt":%q,"schema":{"type":"object"},"maxTokens":1000}`, strings.Repeat("p", 183))

	// Every answer runs into the token limit mid-object, so each call makes both attempts and costs 3000
	for range 16 {
		h.provider.Enqueue(fake.ScriptedResponse{Text: `{"keys":["a","b",`, Stop: llm.StopMaxTokens, Usage: llm.Usage{Input: 500, Output: 1000}, Delay: 500 * time.Millisecond})
	}

	// Parallel calls are admitted only while the reservations of both attempts fit the limit
	var mu sync.Mutex
	statuses := map[int]int{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			status, err := h.call(context.Background(), body)
			assert.NoError(t, err)
			mu.Lock()
			statuses[status]++
			mu.Unlock()
		})
	}
	wg.Wait()
	assert.Equal(t, map[int]int{http.StatusBadGateway: 2, http.StatusForbidden: 6}, statuses)
	assert.Len(t, h.provider.Requests(), 4)
	assert.Equal(t, int64(6000), h.cost())
	assert.LessOrEqual(t, h.cost(), h.live.Job.Limits.MaxCost)
}
