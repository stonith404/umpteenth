package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

var _ llm.ModelLister = (*Provider)(nil)

// notChat matches the models servers list next to their chat models, which an agent can't talk to
var notChat = regexp.MustCompile(`(?i)(embed|rerank|whisper|tts|transcri|moderation|dall-e|stable-diffusion)`)

// listedModel holds the standard model fields plus the extras compatible servers add, each named after the servers that send it
type listedModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// vLLM and SGLang report the context window they were started with
	MaxModelLen looseNumber `json:"max_model_len"`
	// OpenRouter and Together
	ContextLength looseNumber `json:"context_length"`
	// Groq
	ContextWindow looseNumber `json:"context_window"`
	// Mistral and LM Studio
	MaxContextLength looseNumber `json:"max_context_length"`
	TopProvider      struct {
		ContextLength looseNumber `json:"context_length"`
	} `json:"top_provider"`
	// llama.cpp only reports the context window the model was trained with
	Meta struct {
		NCtxTrain looseNumber `json:"n_ctx_train"`
	} `json:"meta"`
	// OpenRouter prices per token as strings, Together per 1M tokens as numbers
	Pricing map[string]looseNumber `json:"pricing"`
	// OpenRouter lists the request parameters a model accepts
	SupportedParameters []string `json:"supported_parameters"`
	Architecture        struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	// Mistral sends an object of flags, other servers a list of names
	Capabilities json.RawMessage `json:"capabilities"`
}

// ListModels reads the server's model list, keeping the metadata that compatible servers add to the standard fields
func (p *Provider) ListModels(ctx context.Context) ([]llm.ListedModel, error) {
	// The body is read raw, since some servers send JSON without saying so in the content type
	var raw []byte
	err := p.client.Get(ctx, "models", nil, &raw)
	if err != nil {
		return nil, mapError(err)
	}
	var page struct {
		Data []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &page) != nil {
		return nil, fmt.Errorf("the server's model list is not JSON, check that the base URL ends where /models is served, such as /v1")
	}
	if len(page.Data) > llm.MaxListedModels {
		return nil, fmt.Errorf("the server lists %d models, more than the %d a provider can hold", len(page.Data), llm.MaxListedModels)
	}

	out := []llm.ListedModel{}
	for _, raw := range page.Data {
		var m listedModel
		// A model whose extras don't parse is still listed by its ID
		if json.Unmarshal(raw, &m) != nil {
			var id struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(raw, &id)
			m = listedModel{ID: id.ID}
		}
		if listed, ok := m.toListed(); ok {
			out = append(out, listed)
		}
	}
	return out, nil
}

func (m listedModel) toListed() (llm.ListedModel, bool) {
	id := strings.TrimSpace(m.ID)
	if id == "" || notChat.MatchString(id) {
		return llm.ListedModel{}, false
	}
	if len(m.Architecture.OutputModalities) > 0 && !slices.Contains(m.Architecture.OutputModalities, "text") {
		return llm.ListedModel{}, false
	}

	out := llm.ListedModel{ID: id, Label: strings.TrimSpace(m.Name), Price: m.price()}
	for _, n := range []looseNumber{m.MaxModelLen, m.ContextLength, m.ContextWindow, m.MaxContextLength, m.TopProvider.ContextLength, m.Meta.NCtxTrain} {
		if n > 0 {
			out.Context = int(n)
			break
		}
	}

	// OpenRouter's parameter list says what a model can do
	if len(m.SupportedParameters) > 0 {
		out.Tools = new(slices.Contains(m.SupportedParameters, "tools"))
		out.JSONSchema = new(slices.Contains(m.SupportedParameters, "structured_outputs"))
		out.Reasoning = new(slices.Contains(m.SupportedParameters, "reasoning"))
	}
	if len(m.Architecture.InputModalities) > 0 {
		out.Vision = new(slices.Contains(m.Architecture.InputModalities, "image"))
	}
	m.applyCapabilities(&out)
	return out, true
}

// price reads per-token prices as OpenRouter sends them and per-1M prices as Together does
// A negative price marks a router whose price depends on the model it picks, which is as good as unknown
func (m listedModel) price() *llm.Price {
	perToken := func(key string) int64 { return int64(math.Round(float64(m.Pricing[key]) * 1e12)) }
	perMillion := func(key string) int64 { return int64(math.Round(float64(m.Pricing[key]) * 1e6)) }
	var p llm.Price
	switch {
	case hasKey(m.Pricing, "prompt"):
		p = llm.Price{In: perToken("prompt"), Out: perToken("completion"), CacheRead: perToken("input_cache_read"), CacheWrite: perToken("input_cache_write")}
	case hasKey(m.Pricing, "input"):
		p = llm.Price{In: perMillion("input"), Out: perMillion("output"), CacheRead: perMillion("cached_input")}
	default:
		return nil
	}
	if p.In < 0 || p.Out < 0 || p.CacheRead < 0 || p.CacheWrite < 0 {
		return nil
	}
	return &p
}

func (m listedModel) applyCapabilities(out *llm.ListedModel) {
	raw := bytes.TrimSpace(m.Capabilities)
	if len(raw) == 0 {
		return
	}
	var flags map[string]bool
	if json.Unmarshal(raw, &flags) == nil {
		if v, ok := flags["function_calling"]; ok {
			out.Tools = new(v)
		}
		if v, ok := flags["vision"]; ok {
			out.Vision = new(v)
		}
		return
	}
	var names []string
	if json.Unmarshal(raw, &names) == nil {
		out.Tools = new(slices.Contains(names, "tools") || slices.Contains(names, "tool_use"))
		out.Vision = new(slices.Contains(names, "vision"))
	}
}

func hasKey(m map[string]looseNumber, key string) bool {
	_, ok := m[key]
	return ok
}

// looseNumber accepts a JSON number or a numeric string, since servers disagree on which to send, and reads anything else as zero
type looseNumber float64

func (n *looseNumber) UnmarshalJSON(data []byte) error {
	v, err := strconv.ParseFloat(strings.Trim(string(data), `"`), 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		v = 0
	}
	*n = looseNumber(v)
	return nil
}
