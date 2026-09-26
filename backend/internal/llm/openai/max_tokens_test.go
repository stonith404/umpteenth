//go:build unit

package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// redirectTransport sends the requests a provider addresses to OpenAI's API to a local fake instead
type redirectTransport struct {
	target *url.URL
}

func (r redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	req.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// strictOpenAI answers like OpenAI's API does for reasoning models, which reject the legacy max_tokens field
type strictOpenAI struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (s *strictOpenAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	s.mu.Unlock()

	if gjson.GetBytes(body, "max_tokens").Exists() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.","type":"invalid_request_error","param":"max_tokens","code":"unsupported_parameter"}}`)
		return
	}
	data, _ := os.ReadFile(filepath.Join("testdata", "structured_native.sse"))
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write(data)
}

func (s *strictOpenAI) lastBody() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies[len(s.bodies)-1]
}

// storedCaps answers Caps from a model row the way providers.modelCaps does
type storedCaps struct {
	llm.Provider
	caps llm.Caps
}

func (s storedCaps) Caps(string) llm.Caps { return s.caps }

// OpenAI's own API must get max_completion_tokens for every model, not only for the ones the catalog lists right now
func TestOpenAIAPIGetsMaxCompletionTokensForModelsOutsideTheCatalog(t *testing.T) {
	withoutGPT5 := *llm.CurrentCatalog()
	withoutGPT5.Models = nil
	for _, e := range llm.CurrentCatalog().Models {
		if e.Model != "gpt-5" {
			withoutGPT5.Models = append(withoutGPT5.Models, e)
		}
	}

	tests := []struct {
		name    string
		model   string
		catalog *llm.ModelCatalog
	}{
		// An admin adds a model by hand that the catalog doesn't list
		{name: "model added by hand", model: "o4-mini", catalog: llm.CurrentCatalog()},
		// A refresh drops a model once models.dev marks it deprecated, while OpenAI still serves it and workspaces still use it
		{name: "model dropped from the catalog", model: "gpt-5", catalog: &withoutGPT5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := llm.SwapCatalog(tt.catalog)
			t.Cleanup(restore)

			// The provider has no base URL, which is OpenAI's API, and its requests reach the fake
			api := &strictOpenAI{}
			srv := httptest.NewServer(api)
			t.Cleanup(srv.Close)
			target, err := url.Parse(srv.URL)
			require.NoError(t, err)
			p, err := New(llm.Config{APIKey: "sk-test", HTTPClient: &http.Client{Transport: redirectTransport{target: target}}})
			require.NoError(t, err)

			// Compile asks for structured output with a token limit, and the model row says it reasons and supports JSON schemas
			var out criteria
			_, err = llm.Structured(context.Background(), storedCaps{Provider: p, caps: llm.Caps{Tools: true, ParallelTools: true, Reasoning: true, JSONSchema: true, Context: 200_000}}, llm.Request{
				Model:        tt.model,
				Messages:     []llm.Message{userText("Compile the success criteria.")},
				MaxTokens:    4096,
				Effort:       llm.EffortLow,
				OutputSchema: criteriaSchema,
				OutputName:   "job_spec",
			}, &out)

			body := api.lastBody()
			assert.False(t, gjson.GetBytes(body, "max_tokens").Exists(), "OpenAI rejects max_tokens for reasoning models")
			assert.Equal(t, int64(4096), gjson.GetBytes(body, "max_completion_tokens").Int())
			require.NoError(t, err)
			assert.Equal(t, criteria{Criteria: []string{"PR list is posted"}, Confidence: 0.9}, out)
		})
	}
}
