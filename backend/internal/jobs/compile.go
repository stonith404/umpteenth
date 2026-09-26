package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

// specSchema is the structured output the utility model must produce
var specSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["title", "goal", "schedule", "successCriteria", "inputs", "outputs", "mcp", "network", "dockerfile", "sideEffects", "warnings"],
  "properties": {
    "title": {"type": "string", "description": "Short job name, at most 60 characters"},
    "goal": {"type": "string", "description": "One sentence describing the outcome"},
    "schedule": {
      "anyOf": [
        {"type": "null"},
        {"type": "object", "additionalProperties": false, "required": ["cron", "timezone", "human"], "properties": {
          "cron": {"type": "string", "description": "Standard 5-field cron expression"},
          "timezone": {"type": "string", "description": "IANA timezone, e.g. Europe/Berlin; UTC when the text names none"},
          "human": {"type": "string", "description": "Plain words, e.g. Weekdays at 08:00"}
        }}
      ]
    },
    "successCriteria": {"type": "array", "items": {"type": "string"}, "description": "Verifiable criteria for a successful run"},
    "inputs": {"type": "array", "items": {"$ref": "#/$defs/field"}},
    "outputs": {"type": "array", "items": {"$ref": "#/$defs/field"}, "description": "Structured values each run should report"},
    "mcp": {"type": "array", "items": {"type": "object", "additionalProperties": false, "required": ["server", "why"], "properties": {
      "server": {"type": "string", "description": "Name of a configured MCP server when one matches, otherwise the service name"},
      "why": {"type": "string"}
    }}},
    "network": {"type": "string", "enum": ["none", "internet"]},
    "dockerfile": {"anyOf": [{"type": "null"}, {"type": "string"}], "description": "Only when the job needs tools beyond bash, curl, git, jq, ripgrep, python3, uv and node; FROM the default sandbox image"},
    "sideEffects": {"type": "array", "items": {"type": "string"}, "description": "External effects such as posting messages"},
    "warnings": {"type": "array", "items": {"type": "string"}, "description": "Ambiguities the user should resolve"}
  },
  "$defs": {"field": {"type": "object", "additionalProperties": false, "required": ["name", "type", "description"], "properties": {
    "name": {"type": "string"}, "type": {"type": "string", "enum": ["string", "integer", "number", "boolean", "object", "array"]}, "description": {"type": "string"}
  }}}
}`)

const compileSystem = `You turn a plain-language job description into a structured spec for Umpteenth, which runs jobs in a disposable Linux sandbox with an LLM agent.

Rules:
- Only include a schedule when the text asks for recurring runs. Convert it to a 5-field cron expression in the timezone the text names (UTC if none).
- Success criteria must be concrete and checkable from what the run did.
- Outputs are small structured values worth tracking across runs (counts, IDs, flags), not long text.
- mcp lists the external services the job needs to talk to. Use the exact name of a configured MCP server when one fits.
- network is "none" only when the job clearly needs no internet access.
- Each job has built-in key-value state that persists between its runs, so remembering values such as the last price or the last seen ID needs no external storage, MCP server or warning.
- The default sandbox image already has bash, coreutils, curl, git, jq, ripgrep, python3, uv, node and npm. Only propose a dockerfile when the job needs more, starting with FROM %s.
- sideEffects lists everything the job changes outside the sandbox.
- warnings lists ambiguities or missing information the user should resolve before saving.`

type compileInput struct {
	Body struct {
		Instruction string `json:"instruction" minLength:"1" maxLength:"20000"`
	}
}

type compileOutput struct {
	Body Spec
}

// compile turns an instruction into a spec with the workspace utility model, without saving anything
func (m *Module) compile(ctx context.Context, in *compileInput) (*compileOutput, error) {
	// A description made only of spaces would spend a model call on nothing
	if strings.TrimSpace(in.Body.Instruction) == "" {
		return nil, apperror.InvalidField("instruction", "required", "must not be empty")
	}
	wid := principal.WorkspaceID(ctx)
	ws, err := m.deps.Settings.Get(ctx, wid)
	if err != nil {
		return nil, err
	}
	modelID := ws.UtilityModelID
	if modelID == nil {
		modelID = ws.AgentModelID
	}
	if modelID == nil {
		return nil, apperror.Unsupported("Set a utility model in Settings to compile jobs")
	}
	provider, model, err := m.deps.Models.ResolveModel(ctx, wid, *modelID)
	if err != nil {
		return nil, err
	}

	// Configured MCP servers are listed so the model can match needs to real servers
	var servers []string
	if m.deps.MCP != nil {
		servers, _ = m.deps.MCP.ServerNames(ctx, wid)
	}
	user := "Job description:\n" + in.Body.Instruction + "\n\nToday is " + time.Now().UTC().Format("Monday 2006-01-02") + "."
	if len(servers) > 0 {
		user += "\nConfigured MCP servers: " + strings.Join(servers, ", ")
	} else {
		user += "\nNo MCP servers are configured yet."
	}

	// Local models can take minutes for a structured spec, so the limit is generous
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var spec Spec
	_, err = llm.Structured(callCtx, provider, llm.Request{
		Model:        model,
		System:       []llm.Block{{Text: fmt.Sprintf(compileSystem, ws.DefaultImage), CacheBreakpoint: true}},
		Messages:     []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart(user)}}},
		MaxTokens:    4096,
		Effort:       llm.EffortLow,
		OutputSchema: specSchema,
		OutputName:   "job_spec",
	}, &spec)
	if err != nil {
		return nil, apperror.ProviderError(err, "The model could not compile the job")
	}

	normalizeSpec(&spec)
	spec.Warnings = append(spec.Warnings, m.specWarnings(ctx, spec, servers)...)
	if spec.Schedule != nil {
		if err := validateSchedule(&spec.Schedule.Cron, &spec.Schedule.Timezone); err != nil {
			spec.Warnings = append(spec.Warnings, "The proposed schedule is invalid: "+err.Error())
			spec.Schedule = nil
		}
	}
	return &compileOutput{Body: spec}, nil
}

// specWarnings checks the spec against the workspace and the active sandbox adapter
func (m *Module) specWarnings(ctx context.Context, spec Spec, servers []string) []string {
	var warnings []string
	for _, need := range spec.MCP {
		if !slices.ContainsFunc(servers, func(s string) bool { return strings.EqualFold(s, need.Server) }) {
			warnings = append(warnings, fmt.Sprintf("Mentions %s, but no MCP server with that name is configured.", need.Server))
		}
	}
	if m.deps.SandboxInfo != nil {
		info, err := m.deps.SandboxInfo(ctx)
		if err == nil {
			if !info.Caps.SupportsNetwork(sandbox.NetworkPolicy(spec.Network)) {
				warnings = append(warnings, fmt.Sprintf("network: %s is not supported by the active sandbox adapter.", spec.Network))
			}
			if spec.Dockerfile != nil && !info.ImageBuilds {
				warnings = append(warnings, "Dockerfiles need a sandbox adapter that can build images.")
			}
		}
	}
	return warnings
}
