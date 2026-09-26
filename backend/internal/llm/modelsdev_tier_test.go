//go:build unit

package llm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tieredModelsDevFixture is gpt-6-sol as models.dev lists it, with the higher price OpenAI charges for prompts above 272K tokens
const tieredModelsDevFixture = `{
  "anthropic": {"models": {
    "claude-x-1": {"id": "claude-x-1", "name": "Claude X 1", "tool_call": true, "modalities": {"input": ["text"], "output": ["text"]},
      "limit": {"context": 200000}, "cost": {"input": 3, "output": 15}}
  }},
  "openai": {"models": {
    "gpt-6-sol": {"id": "gpt-6-sol", "name": "GPT-6 Sol", "reasoning": true, "tool_call": true, "structured_output": true,
      "modalities": {"input": ["text", "image", "pdf"], "output": ["text"]}, "limit": {"context": 1050000, "input": 922000, "output": 128000},
      "cost": {"input": 2, "output": 10, "cache_read": 0.2, "cache_write": 2.5,
        "tiers": [{"input": 4, "output": 15, "cache_read": 0.4, "cache_write": 5, "tier": {"type": "context", "size": 272000}}],
        "context_over_200k": {"input": 4, "output": 15, "cache_read": 0.4, "cache_write": 5}}}
  }}
}`

// longContextTier is one model's OpenAI prices below and above its long context threshold
type longContextTier struct {
	base, over Price
	size       int64
}

// bill prices one call the way OpenAI does, where a prompt above the threshold costs the higher price for the whole call
func (p longContextTier) bill(u Usage) int64 {
	if u.Input+u.CacheRead+u.CacheWrite > p.size {
		return Cost(u, p.over)
	}
	return Cost(u, p.base)
}

// assertBilledLikeOpenAI checks that every prompt the model's context window lets the agent send is recorded at what OpenAI bills for it
func assertBilledLikeOpenAI(t *testing.T, e CatalogEntry, tier longContextTier) {
	t.Helper()
	for _, prompt := range []int64{100_000, 272_000, 400_000, 600_000, int64(e.Caps.Context)} {
		if prompt > int64(e.Caps.Context) {
			continue
		}

		// A long agent run resends most of its prompt from the cache and adds a few thousand fresh tokens per turn
		u := Usage{Input: prompt / 20, CacheRead: prompt - prompt/20, Output: 3_000}
		assert.Equal(t, tier.bill(u), Cost(u, e.Price), "%s with a %d-token prompt in a %d-token context window", e.Model, prompt, e.Caps.Context)
	}
}

func TestParseModelsDevBillsTheLongContextTier(t *testing.T) {
	c, err := ParseModelsDev([]byte(tieredModelsDevFixture), time.Now())
	require.NoError(t, err)
	var gpt CatalogEntry
	for _, e := range c.Models {
		if e.Model == "gpt-6-sol" {
			gpt = e
		}
	}
	require.Equal(t, "gpt-6-sol", gpt.Model)

	assertBilledLikeOpenAI(t, gpt, longContextTier{
		base: Price{In: 2_000_000, Out: 10_000_000, CacheRead: 200_000, CacheWrite: 2_500_000},
		over: Price{In: 4_000_000, Out: 15_000_000, CacheRead: 400_000, CacheWrite: 5_000_000},
		size: 272_000,
	})

	// The agent's window ends where the higher price starts, while the family traits keep the whole window
	assert.Equal(t, 272_000, gpt.Caps.Context)
	assert.Equal(t, 922_000, c.Families["gpt6sol"].Context)
}

func TestBundledCatalogBillsTheLongContextTier(t *testing.T) {
	tiers := map[string]longContextTier{
		"gpt-5.4": {
			base: Price{In: 2_500_000, Out: 15_000_000, CacheRead: 250_000},
			over: Price{In: 5_000_000, Out: 22_500_000, CacheRead: 500_000},
			size: 272_000,
		},
		"gpt-6-sol": {
			base: Price{In: 2_000_000, Out: 10_000_000, CacheRead: 200_000, CacheWrite: 2_500_000},
			over: Price{In: 4_000_000, Out: 15_000_000, CacheRead: 400_000, CacheWrite: 5_000_000},
			size: 272_000,
		},
	}
	for model, tier := range tiers {
		e, ok := LookupModel(KindOpenAI, model)
		require.True(t, ok, model)
		assertBilledLikeOpenAI(t, e, tier)
	}
}
