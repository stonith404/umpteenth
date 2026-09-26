//go:build unit

package llm

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBundledCatalogIsValid(t *testing.T) {
	c, err := DecodeCatalog(bundledCatalog)
	require.NoError(t, err, "regenerate it with go generate ./internal/llm")
	assert.Positive(t, c.FetchedAt)
	assert.NotEmpty(t, c.Families)
}

func TestDefaultModelsAreInTheCatalog(t *testing.T) {
	for _, model := range []string{DefaultAgentModel, DefaultUtilityModel} {
		_, ok := LookupModel(KindAnthropic, model)
		assert.True(t, ok, model)
	}
}

func TestLookupModel(t *testing.T) {
	e, ok := LookupModel(KindAnthropic, "claude-opus-5-5")
	require.True(t, ok)
	assert.Equal(t, "Claude Opus 5.5", e.Label)
	assert.Equal(t, Price{In: 4_000_000, Out: 20_000_000, CacheRead: 200_000, CacheWrite: 5_000_000}, e.Price)
	assert.Equal(t, 1_000_000, e.Caps.Context)

	// Pinned snapshot IDs resolve to their alias
	e, ok = LookupModel(KindAnthropic, "claude-haiku-4-5-20251001")
	require.True(t, ok)
	assert.Equal(t, "claude-haiku-4-5", e.Model)
	assert.Equal(t, 200_000, e.Caps.Context)
	e, ok = LookupModel(KindOpenAI, "gpt-4.1-2025-04-14")
	require.True(t, ok)
	assert.Equal(t, "gpt-4.1", e.Model)

	// claude-opus-5 must not swallow claude-opus-5-5 and vice versa
	e, ok = LookupModel(KindAnthropic, "claude-opus-5")
	require.True(t, ok)
	assert.Equal(t, int64(5_000_000), e.Price.In)

	_, ok = LookupModel(KindOpenAI, "claude-opus-5-5")
	assert.False(t, ok, "lookups are scoped to the provider kind")
	_, ok = LookupModel(KindOpenAI, "llama3.3:70b")
	assert.False(t, ok)
}

func TestCatalogIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Catalog() {
		key := e.Kind + "/" + e.Model
		assert.False(t, seen[key], "duplicate %s", key)
		seen[key] = true
		assert.NotEmpty(t, e.Label, key)
		assert.True(t, e.Caps.Tools, key)
		assert.Positive(t, e.Caps.Context, key)
		assert.Positive(t, e.Price.In, key)
		assert.Greater(t, e.Price.Out, e.Price.In, key)
		assert.Less(t, e.Price.CacheRead, e.Price.In, key)
	}
	assert.NotEmpty(t, CatalogFor(KindAnthropic))
	assert.NotEmpty(t, CatalogFor(KindOpenAI))
}

// modelsDevFixture has one model for every rule the catalog applies
const modelsDevFixture = `{
  "anthropic": {"models": {
    "claude-x-1": {"id": "claude-x-1", "name": "Claude X 1 (latest)", "tool_call": true, "reasoning": true, "structured_output": true,
      "modalities": {"input": ["text", "image"], "output": ["text"]}, "limit": {"context": 200000, "output": 64000},
      "cost": {"input": 3, "output": 15, "cache_read": 0.3, "cache_write": 3.75}},
    "claude-x-1-20260101": {"id": "claude-x-1-20260101", "name": "Claude X 1", "tool_call": true,
      "modalities": {"input": ["text"], "output": ["text"]}, "limit": {"context": 200000}, "cost": {"input": 3, "output": 15}},
    "claude-old": {"id": "claude-old", "name": "Claude Old", "tool_call": true, "status": "deprecated",
      "modalities": {"input": ["text"], "output": ["text"]}, "limit": {"context": 100000}, "cost": {"input": 8, "output": 24}}
  }},
  "openai": {"models": {
    "gpt-x": {"id": "gpt-x", "name": "GPT-X", "tool_call": true, "modalities": {"input": ["text"], "output": ["text"]},
      "limit": {"context": 400000, "input": 272000}, "cost": {"input": 1.25, "output": 10, "cache_read": 0.125}},
    "gpt-x-pro": {"id": "gpt-x-pro", "name": "GPT-X Pro", "tool_call": true, "modalities": {"input": ["text"], "output": ["text"]},
      "limit": {"context": 400000}, "cost": {"input": 15, "output": 120}},
    "gpt-image-x": {"id": "gpt-image-x", "name": "GPT Image", "modalities": {"input": ["text"], "output": ["image"]}, "limit": {"context": 0}},
    "text-embedding-x": {"id": "text-embedding-x", "name": "Embedding", "modalities": {"input": ["text"], "output": ["text"]},
      "limit": {"context": 8191}, "cost": {"input": 0.02, "output": 0}}
  }},
  "host-a": {"models": {
    "Qwen/Qwen3-32B": {"id": "Qwen/Qwen3-32B", "tool_call": true, "reasoning": true, "modalities": {"input": ["text"], "output": ["text"]}, "limit": {"context": 40960}}
  }},
  "host-b": {"models": {
    "qwen3-32b": {"id": "qwen3-32b", "tool_call": true, "modalities": {"input": ["text"], "output": ["text"]}, "limit": {"context": 131072}},
    "qwen3-32b-fp8": {"id": "qwen3-32b-fp8", "tool_call": false, "reasoning": true, "modalities": {"input": ["text"], "output": ["text"]}, "limit": {"context": 131072}}
  }}
}`

func TestParseModelsDev(t *testing.T) {
	fetched := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	c, err := ParseModelsDev([]byte(modelsDevFixture), fetched)
	require.NoError(t, err)
	assert.Equal(t, fetched.Unix(), c.FetchedAt)

	// Deprecated, image, embedding, Responses-only and dated snapshot models are left out
	var ids []string
	for _, e := range c.Models {
		ids = append(ids, e.Kind+"/"+e.Model)
	}
	assert.Equal(t, []string{"anthropic/claude-x-1", "openai/gpt-x"}, ids)

	claude := c.Models[0]
	assert.Equal(t, "Claude X 1", claude.Label)
	assert.Equal(t, Caps{Tools: true, ParallelTools: true, Reasoning: true, JSONSchema: true, PromptCache: true, Vision: true, Context: 200_000}, claude.Caps)
	assert.Equal(t, Price{In: 3_000_000, Out: 15_000_000, CacheRead: 300_000, CacheWrite: 3_750_000}, claude.Price)

	// The input limit wins over the shared context window, and a cache read price means OpenAI caches the prompt
	gpt := c.Models[1]
	assert.Equal(t, 272_000, gpt.Caps.Context)
	assert.True(t, gpt.Caps.PromptCache)
	assert.Zero(t, gpt.Price.CacheWrite)

	// Hosts vote on a family's traits, and the median context window wins
	family := c.Families["qwen332b"]
	assert.True(t, family.Tools, "two of three hosts call tools")
	assert.True(t, family.Reasoning, "two of three hosts reason")
	assert.Equal(t, 131_072, family.Context)
}

func TestParseModelsDevRejectsDocumentsWithoutTheProviders(t *testing.T) {
	_, err := ParseModelsDev([]byte(`{"anthropic": {"models": {}}}`), time.Now())
	require.Error(t, err)
	_, err = ParseModelsDev([]byte(`not json`), time.Now())
	require.Error(t, err)
}

func TestFamilyKey(t *testing.T) {
	cases := map[string]string{
		"qwen3:32b":                         "qwen332b",
		"Qwen/Qwen3-32B":                    "qwen332b",
		"qwen3-32b-instruct-fp8":            "qwen332b",
		"qwen3:32b-q4_K_M":                  "qwen332b",
		"llama3.3:70b":                      "llama3.370b",
		"meta-llama/Llama-3.3-70B-Instruct": "llama3.370b",
		"gpt-oss:20b":                       "gptoss20b",
		"openai/gpt-oss-20b":                "gptoss20b",
		"gemma3:27b-it-qat":                 "gemma327bqat",
		"qwen3:latest":                      "qwen3",
		"":                                  "",
	}
	for id, want := range cases {
		assert.Equal(t, want, FamilyKey(id), id)
	}
}

func TestLookupFamilyMatchesHostNames(t *testing.T) {
	caps, ok := LookupFamily("gpt-oss:20b")
	require.True(t, ok)
	assert.True(t, caps.Tools)
	assert.Positive(t, caps.Context)

	_, ok = LookupFamily("my-own-finetune:7b")
	assert.False(t, ok)
}

func TestSetCatalogKeepsTheNewerCatalog(t *testing.T) {
	original := CurrentCatalog()
	t.Cleanup(func() { current.Store(original) })

	older := &ModelCatalog{FetchedAt: original.FetchedAt - 1, Models: []CatalogEntry{{Kind: KindOpenAI, Model: "older"}}}
	assert.False(t, SetCatalog(older))
	assert.Same(t, original, CurrentCatalog())

	newer := &ModelCatalog{FetchedAt: original.FetchedAt + 1, Models: []CatalogEntry{{Kind: KindOpenAI, Model: "newer"}}}
	assert.True(t, SetCatalog(newer))
	_, ok := LookupModel(KindOpenAI, "newer")
	assert.True(t, ok)
}

func TestEncodeCatalogRoundTrips(t *testing.T) {
	data, err := EncodeCatalog(CurrentCatalog())
	require.NoError(t, err)
	c, err := DecodeCatalog(data)
	require.NoError(t, err)
	assert.Equal(t, CurrentCatalog(), c)
}

func TestCost(t *testing.T) {
	e, _ := LookupModel(KindAnthropic, "claude-opus-5-5")
	u := Usage{Input: 1_000, Output: 500, CacheRead: 100_000, CacheWrite: 2_000}

	// 1k input at $4, 500 output at $20, 100k cache reads at $0.20 and 2k cache writes at $5 per 1M tokens
	assert.Equal(t, int64(4_000+10_000+20_000+10_000), Cost(u, e.Price))
}
