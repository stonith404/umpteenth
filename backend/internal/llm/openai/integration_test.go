//go:build integration

package openai

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// These tests run the adapter against a real Ollama server
// They are skipped unless OLLAMA_URL is set, for example OLLAMA_URL=http://localhost:11434/v1

const defaultOllamaModel = "qwen3.8:27b-q4_K_M"

// callTimeout is generous because a local model may need to load and think before answering
const callTimeout = 5 * time.Minute

func ollama(t *testing.T) (*Provider, string) {
	t.Helper()
	url := os.Getenv("OLLAMA_URL")
	if url == "" {
		t.Skip("OLLAMA_URL is not set")
	}
	model := os.Getenv("OLLAMA_MODEL")
	if model == "" {
		model = defaultOllamaModel
	}
	p, err := New(llm.Config{Kind: llm.KindOpenAI, BaseURL: url})
	require.NoError(t, err)
	return p, model
}

func TestOllamaPlainAnswer(t *testing.T) {
	p, model := ollama(t)
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	var streamed strings.Builder
	resp, err := p.Stream(ctx, llm.Request{
		Model:     model,
		System:    []llm.Block{{Text: "You answer with a single word and nothing else.", CacheBreakpoint: true}},
		Messages:  []llm.Message{userText("Reply with the word PONG.")},
		MaxTokens: 4096,
	}, func(d llm.Delta) {
		if d.Type == llm.DeltaText {
			streamed.WriteString(d.Text)
		}
	})
	require.NoError(t, err)

	assert.Contains(t, strings.ToUpper(resp.Message.Text()), "PONG")
	assert.Equal(t, resp.Message.Text(), streamed.String(), "the deltas add up to the final text")
	assert.Equal(t, llm.StopEndTurn, resp.Stop)
	assert.Positive(t, resp.Usage.Input)
	assert.Positive(t, resp.Usage.Output)
	assert.Equal(t, model, resp.Model)
}

func TestOllamaToolCallRoundTrip(t *testing.T) {
	p, model := ollama(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*callTimeout)
	defer cancel()

	req := llm.Request{
		Model:     model,
		System:    []llm.Block{{Text: "Use the get_weather tool to answer weather questions. Never guess the weather."}},
		Messages:  []llm.Message{userText("What is the weather in Paris right now?")},
		Tools:     []llm.ToolDef{weatherTool},
		MaxTokens: 4096,
	}

	// The model asks for the tool
	var toolStarts []string
	resp, err := p.Stream(ctx, req, func(d llm.Delta) {
		if d.Type == llm.DeltaToolCall {
			toolStarts = append(toolStarts, d.ToolName)
		}
	})
	require.NoError(t, err)
	require.Equal(t, llm.StopToolUse, resp.Stop)
	calls := resp.Message.ToolCalls()
	require.NotEmpty(t, calls)
	assert.Contains(t, toolStarts, "get_weather")

	var results []llm.Part
	for _, call := range calls {
		require.Equal(t, "get_weather", call.Name)
		require.NotEmpty(t, call.ID)
		var args struct {
			City string `json:"city"`
		}
		require.NoError(t, json.Unmarshal(call.Args, &args))
		assert.Contains(t, strings.ToLower(args.City), "paris")
		results = append(results, llm.Part{Type: llm.PartToolResult, ToolResult: &llm.ToolResult{CallID: call.ID, Content: `{"city":"Paris","temperatureC":17,"conditions":"light rain"}`}})
	}

	// The result goes back and the model answers from it
	req.Messages = append(req.Messages, resp.Message, llm.Message{Role: llm.RoleUser, Parts: results})
	resp, err = p.Stream(ctx, req, nil)
	require.NoError(t, err)
	assert.Equal(t, llm.StopEndTurn, resp.Stop)
	assert.Contains(t, resp.Message.Text(), "17")
}

type extraction struct {
	City         string  `json:"city"`
	TemperatureC float64 `json:"temperatureC"`
}

var extractionSchema = json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"},"temperatureC":{"type":"number"}},"required":["city","temperatureC"]}`)

// nativeJSON reports native JSON-schema support, which Ollama has but the adapter cannot assume for unknown models
type nativeJSON struct{ *Provider }

func (n nativeJSON) Caps(model string) llm.Caps {
	caps := n.Provider.Caps(model)
	caps.JSONSchema = true
	return caps
}

func TestOllamaStructured(t *testing.T) {
	p, model := ollama(t)
	req := llm.Request{
		Model:        model,
		Messages:     []llm.Message{userText("Extract the city and the temperature in Celsius: It is 21 degrees in Lisbon today.")},
		OutputSchema: extractionSchema,
		OutputName:   "weather",
		MaxTokens:    4096,
	}

	// Unknown models go through the forced submit tool
	t.Run("submit tool", func(t *testing.T) {
		require.False(t, p.Caps(model).JSONSchema)
		ctx, cancel := context.WithTimeout(context.Background(), 2*callTimeout)
		defer cancel()

		var out extraction
		resp, err := llm.Structured(ctx, p, req, &out)
		require.NoError(t, err)
		assert.Equal(t, extraction{City: "Lisbon", TemperatureC: 21}, out)
		assert.Equal(t, llm.StopToolUse, resp.Stop)
	})

	// Models configured with JSON-schema support use response_format instead
	t.Run("native", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*callTimeout)
		defer cancel()

		var out extraction
		resp, err := llm.Structured(ctx, nativeJSON{p}, req, &out)
		require.NoError(t, err)
		assert.Equal(t, extraction{City: "Lisbon", TemperatureC: 21}, out)
		assert.Empty(t, resp.Message.ToolCalls())
	})
}
