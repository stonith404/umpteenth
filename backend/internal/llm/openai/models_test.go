//go:build unit

package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// modelsList mixes the model entries of the servers ListModels reads
const modelsList = `{"object": "list", "data": [
  {"id": "qwen3:32b", "object": "model", "created": 1, "owned_by": "library"},
  {"id": "nomic-embed-text:latest", "object": "model", "owned_by": "library"},
  {"id": "Qwen/Qwen3-32B", "object": "model", "owned_by": "vllm", "max_model_len": 40960},
  {"id": "llama-3.3-70b-versatile", "object": "model", "owned_by": "Meta", "context_window": 131072},
  {"id": "gemma-3-27b", "object": "model", "meta": {"n_ctx_train": 131072, "n_params": 27000000000}},
  {"id": "anthropic/claude-opus-5.5", "name": "Anthropic: Claude Opus 5.5", "context_length": 1000000,
    "pricing": {"prompt": "0.000004", "completion": "0.00002", "input_cache_read": "0.0000002", "input_cache_write": "0.000005"},
    "supported_parameters": ["max_tokens", "tools", "structured_outputs", "reasoning"],
    "architecture": {"input_modalities": ["text", "image"], "output_modalities": ["text"]}},
  {"id": "openrouter/auto", "context_length": "2000000", "pricing": {"prompt": "-1", "completion": "-1"}},
  {"id": "black-forest-labs/flux", "architecture": {"output_modalities": ["image"]}},
  {"id": "mistral-small-latest", "max_context_length": 131072, "capabilities": {"completion_chat": true, "function_calling": true, "vision": false}},
  {"id": "meta-llama/Llama-3.3-70B-Instruct-Turbo", "context_length": 131072, "pricing": {"input": 0.88, "output": 0.88}},
  {"id": "odd", "max_model_len": {"not": "a number"}},
  {"id": ""}
]}`

func TestListModelsReadsCompatibleServers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer local-key" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(modelsList))
	}))
	t.Cleanup(srv.Close)
	p, err := New(llm.Config{Kind: llm.KindOpenAI, APIKey: "local-key", BaseURL: srv.URL + "/v1"})
	require.NoError(t, err)

	models, err := p.ListModels(context.Background())
	require.NoError(t, err)
	byID := map[string]llm.ListedModel{}
	var ids []string
	for _, m := range models {
		byID[m.ID] = m
		ids = append(ids, m.ID)
	}

	// Embedding and image models are left out, and so are entries without an ID
	assert.Equal(t, []string{
		"qwen3:32b", "Qwen/Qwen3-32B", "llama-3.3-70b-versatile", "gemma-3-27b", "anthropic/claude-opus-5.5",
		"openrouter/auto", "mistral-small-latest", "meta-llama/Llama-3.3-70B-Instruct-Turbo", "odd",
	}, ids)

	// Ollama reports nothing beyond the ID
	assert.Equal(t, llm.ListedModel{ID: "qwen3:32b"}, byID["qwen3:32b"])

	// Each server's context field is read
	assert.Equal(t, 40960, byID["Qwen/Qwen3-32B"].Context)
	assert.Equal(t, 131072, byID["llama-3.3-70b-versatile"].Context)
	assert.Equal(t, 131072, byID["gemma-3-27b"].Context)
	assert.Equal(t, 2_000_000, byID["openrouter/auto"].Context, "numeric strings count")
	assert.Zero(t, byID["odd"].Context)

	// OpenRouter's per-token prices become micro-USD per 1M tokens, and its parameter list the capabilities
	opus := byID["anthropic/claude-opus-5.5"]
	assert.Equal(t, "Anthropic: Claude Opus 5.5", opus.Label)
	assert.Equal(t, &llm.Price{In: 4_000_000, Out: 20_000_000, CacheRead: 200_000, CacheWrite: 5_000_000}, opus.Price)
	assert.Equal(t, []bool{true, true, true, true}, []bool{*opus.Tools, *opus.JSONSchema, *opus.Reasoning, *opus.Vision})
	assert.Nil(t, byID["openrouter/auto"].Price, "a router's variable price is unknown")

	// Together prices per 1M tokens, Mistral flags its capabilities
	assert.Equal(t, &llm.Price{In: 880_000, Out: 880_000}, byID["meta-llama/Llama-3.3-70B-Instruct-Turbo"].Price)
	mistral := byID["mistral-small-latest"]
	assert.True(t, *mistral.Tools)
	assert.False(t, *mistral.Vision)
	assert.Nil(t, mistral.Reasoning)
}

func TestListModelsReportsServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error": {"message": "invalid api key"}}`, http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	p, err := New(llm.Config{Kind: llm.KindOpenAI, BaseURL: srv.URL + "/v1"})
	require.NoError(t, err)

	_, err = p.ListModels(context.Background())
	var apiErr *llm.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
}
