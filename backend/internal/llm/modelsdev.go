package llm

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
)

// ModelsDevURL is the public model database the catalog is built from (https://models.dev, MIT licensed)
const ModelsDevURL = "https://models.dev/api.json"

// modelsDevModel holds the fields of a models.dev model the catalog uses
type modelsDevModel struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ToolCall         bool   `json:"tool_call"`
	Reasoning        bool   `json:"reasoning"`
	StructuredOutput bool   `json:"structured_output"`
	Status           string `json:"status"`
	Modalities       struct {
		Input  []string `json:"input"`
		Output []string `json:"output"`
	} `json:"modalities"`
	Limit struct {
		Context int `json:"context"`
		Input   int `json:"input"`
	} `json:"limit"`
	Cost *struct {
		Input      float64 `json:"input"`
		Output     float64 `json:"output"`
		CacheRead  float64 `json:"cache_read"`
		CacheWrite float64 `json:"cache_write"`
		// Tiers are the higher prices a provider bills for the whole call once the prompt passes a size
		Tiers []struct {
			Tier struct {
				Type string `json:"type"`
				Size int    `json:"size"`
			} `json:"tier"`
		} `json:"tiers"`
	} `json:"cost"`
	Provider struct {
		Shape string `json:"shape"`
	} `json:"provider"`
}

type modelsDevProvider struct {
	Models map[string]modelsDevModel `json:"models"`
}

// catalogProviders maps our provider kinds to their models.dev provider IDs
var catalogProviders = map[string]string{KindAnthropic: "anthropic", KindOpenAI: "openai"}

// responsesOnly matches OpenAI models that only the Responses API serves, which the Chat Completions adapter can't call
var responsesOnly = regexp.MustCompile(`(-pro$|codex|realtime|-audio|-search|-transcribe|-tts)`)

// noNativeJSONSchema corrects models.dev for Claude models it credits with structured outputs, which Anthropic doesn't document output_config.format for
// They get the submit-tool fallback instead of a request the API may reject
var noNativeJSONSchema = map[string]bool{"claude-opus-4-7": true, "claude-opus-4-6": true, "claude-sonnet-4-6": true}

// ParseModelsDev builds a catalog from the models.dev API document
func ParseModelsDev(data []byte, fetchedAt time.Time) (*ModelCatalog, error) {
	var doc map[string]modelsDevProvider
	err := json.Unmarshal(data, &doc)
	if err != nil {
		return nil, fmt.Errorf("invalid models.dev document: %w", err)
	}

	c := &ModelCatalog{FetchedAt: fetchedAt.Unix(), Models: []CatalogEntry{}, Families: map[string]Caps{}}

	// The first-party providers become the catalog agents can pick from
	for kind, id := range catalogProviders {
		provider, ok := doc[id]
		if !ok {
			return nil, fmt.Errorf("the models.dev document has no %s provider", id)
		}
		c.Models = append(c.Models, catalogEntries(kind, provider.Models)...)
	}
	slices.SortFunc(c.Models, func(a, b CatalogEntry) int {
		return cmp.Or(strings.Compare(a.Kind, b.Kind), strings.Compare(a.Model, b.Model))
	})
	if len(c.Models) == 0 {
		return nil, fmt.Errorf("the models.dev document lists no usable models")
	}

	// Every provider's models vote on the traits of a model family, which fills in what OpenAI-compatible servers leave out
	votes := map[string][]modelsDevModel{}
	for _, provider := range doc {
		for _, m := range provider.Models {
			if key := FamilyKey(m.ID); key != "" && textOutput(m) {
				votes[key] = append(votes[key], m)
			}
		}
	}
	for key, models := range votes {
		c.Families[key] = familyCaps(models)
	}
	return c, nil
}

// catalogEntries keeps the models an agent can run on: text in and out, tool calling, a known context window and a price
func catalogEntries(kind string, models map[string]modelsDevModel) []CatalogEntry {
	keep := map[string]modelsDevModel{}
	for id, m := range models {
		if m.ID == "" {
			m.ID = id
		}
		if !m.ToolCall || !textOutput(m) || m.Status == "deprecated" || m.Limit.Context <= 0 || m.Cost == nil || m.Cost.Input <= 0 {
			continue
		}
		if kind == KindOpenAI && (m.Provider.Shape == "responses" || responsesOnly.MatchString(m.ID)) {
			continue
		}
		keep[m.ID] = m
	}

	var out []CatalogEntry
	for id, m := range keep {
		// A dated snapshot is left out when its alias is listed, since both name the same model
		if alias := dateSuffix.ReplaceAllString(id, ""); alias != id {
			if _, ok := keep[alias]; ok {
				continue
			}
		}
		out = append(out, CatalogEntry{Kind: kind, Model: id, Label: modelLabel(m), Caps: modelCaps(kind, m), Price: modelPrice(m)})
	}
	return out
}

// modelLabel drops the "(latest)" marker models.dev puts on aliases
func modelLabel(m modelsDevModel) string {
	label := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(m.Name), "(latest)"))
	if label == "" {
		return m.ID
	}
	return label
}

func modelCaps(kind string, m modelsDevModel) Caps {
	return Caps{
		Tools:         m.ToolCall,
		ParallelTools: m.ToolCall,
		Reasoning:     m.Reasoning,
		JSONSchema:    m.StructuredOutput && (kind != KindAnthropic || !noNativeJSONSchema[m.ID]),
		// Anthropic caches only on request, OpenAI caches every long prompt and bills the cached part at the cache read price
		PromptCache: kind == KindAnthropic || (m.Cost != nil && m.Cost.CacheRead > 0),
		Vision:      slices.Contains(m.Modalities.Input, "image"),
		Context:     untieredContext(m),
	}
}

// untieredContext ends a catalog model's context window where its first long context price tier starts
// Price holds only the base price, so the agent has to compact the conversation before a prompt reaches the tier the provider bills at a higher price for the whole call
// Family traits keep the full window, since no catalog Price goes with them
func untieredContext(m modelsDevModel) int {
	window := contextWindow(m)
	if m.Cost == nil {
		return window
	}
	for _, t := range m.Cost.Tiers {
		if t.Tier.Type == "context" && t.Tier.Size > 0 {
			window = min(window, t.Tier.Size)
		}
	}
	return window
}

// contextWindow prefers the input limit, since some models share their context window between the prompt and the answer
func contextWindow(m modelsDevModel) int {
	if m.Limit.Input > 0 {
		return m.Limit.Input
	}
	return m.Limit.Context
}

func modelPrice(m modelsDevModel) Price {
	if m.Cost == nil {
		return Price{}
	}
	return Price{In: usd(m.Cost.Input), Out: usd(m.Cost.Output), CacheRead: usd(m.Cost.CacheRead), CacheWrite: usd(m.Cost.CacheWrite)}
}

func textOutput(m modelsDevModel) bool {
	return len(m.Modalities.Output) == 1 && m.Modalities.Output[0] == "text"
}

// familyCaps settles the family's traits by majority, and its context window by the median, so a single odd host doesn't decide them
func familyCaps(models []modelsDevModel) Caps {
	var tools, reasoning, jsonSchema, vision int
	var contexts []int
	for _, m := range models {
		tools += boolInt(m.ToolCall)
		reasoning += boolInt(m.Reasoning)
		jsonSchema += boolInt(m.StructuredOutput)
		vision += boolInt(slices.Contains(m.Modalities.Input, "image"))
		if c := contextWindow(m); c > 0 {
			contexts = append(contexts, c)
		}
	}
	majority := func(n int) bool { return n*2 > len(models) }
	caps := Caps{Tools: majority(tools), ParallelTools: majority(tools), Reasoning: majority(reasoning), JSONSchema: majority(jsonSchema), Vision: majority(vision)}
	if len(contexts) > 0 {
		slices.Sort(contexts)
		caps.Context = contexts[len(contexts)/2]
	}
	return caps
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// familyNoise are name parts that say how a model is packaged rather than which model it is
var familyNoise = regexp.MustCompile(`^(instruct|it|latest|gguf|mlx|awq|gptq|exl2|fp\d+|bf16|int\d+|\d+bit|q\d.*|iq\d.*|mxfp\d+)$`)

// FamilyKey normalizes a model ID so the names hosts give the same open model compare equal
// Ollama's qwen3:32b, Hugging Face's Qwen/Qwen3-32B and a hosted qwen3-32b-instruct-fp8 all become qwen332b
// Underscores don't separate parts, so a quantization tag such as q4_K_M stays one part and is dropped as a whole
func FamilyKey(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	var parts []string
	for _, part := range strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == ':' || r == ' ' }) {
		if !familyNoise.MatchString(part) {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "")
}

// usd converts a price in dollars per 1M tokens to micro-USD per 1M tokens
func usd(dollars float64) int64 {
	return int64(math.Round(dollars * 1_000_000))
}
