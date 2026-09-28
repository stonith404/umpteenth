package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/middleware"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

// specSchema is the structured output the utility model must produce
var specSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["title", "goal", "schedule", "successCriteria", "inputs", "outputs", "mcp", "network", "dockerfile", "sideEffects", "questions"],
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
    "dockerfile": {"anyOf": [{"type": "null"}, {"type": "string"}], "description": "Only when the job needs tools beyond bash, curl, git, jq, ripgrep, python3, pip, uv and node; FROM the default sandbox image"},
    "sideEffects": {"type": "array", "items": {"type": "string"}, "description": "External effects such as posting messages"},
    "questions": {"type": "array", "description": "What the user must answer before the job can be set up, usually nothing", "items": {"type": "object", "additionalProperties": false, "required": ["question", "options"], "properties": {
      "question": {"type": "string", "description": "One short question"},
      "options": {"type": "array", "items": {"type": "string"}, "description": "Up to four likely answers, or none when only the user can know"}
    }}}
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
- The default sandbox image already has bash, coreutils, curl, git, jq, ripgrep, python3, pip, uv, node and npm. Only propose a dockerfile when the job needs more, starting with FROM %s.
- sideEffects lists everything the job changes outside the sandbox.
- questions are rare, and most descriptions need none. Only ask when the job cannot run correctly without the answer and there is no sensible default, such as which repository or channel it works on, or when a wrong guess could cause a harmful side effect. Never ask about anything the agent can find out while running, about wording or formatting, or about details a reasonable default settles. Ask at most three.`

// maxQuestions and maxOptions cap what the user is asked, in case the model asks more than it was told to
const (
	maxQuestions = 3
	maxOptions   = 4
)

// Question is something the description leaves open that the job can't be set up without
type Question struct {
	Question string   `json:"question"`
	Options  []string `json:"options" doc:"Likely answers the user can pick, empty when only the user can know"`
}

type compileInput struct {
	Body struct {
		Instruction  string `json:"instruction" minLength:"1" maxLength:"20000"`
		AskQuestions *bool  `json:"askQuestions,omitempty" doc:"Whether the compile step may ask questions, true when omitted; false once the user answered them, so answers never lead to another round"`
	}
}

type compileOutput struct {
	Body struct {
		Spec      Spec       `json:"spec"`
		Questions []Question `json:"questions"`
	}
}

// compiledSpec is the utility model's answer, a spec plus the questions it could not settle on its own
type compiledSpec struct {
	Spec
	Questions []Question `json:"questions"`
}

// errNoCompileModel means the workspace has neither a utility nor an agent model to compile with
var errNoCompileModel = apperror.Unsupported("Set a utility model in Settings to compile jobs")

// compile turns an instruction into a spec with the workspace utility model, without saving anything
func (m *Module) compile(ctx context.Context, in *compileInput) (*compileOutput, error) {
	// A description made only of spaces would spend a model call on nothing
	if strings.TrimSpace(in.Body.Instruction) == "" {
		return nil, apperror.InvalidField("instruction", "required", "must not be empty")
	}
	ask := in.Body.AskQuestions == nil || *in.Body.AskQuestions
	spec, questions, err := m.compileSpec(ctx, principal.WorkspaceID(ctx), in.Body.Instruction, nil, ask)
	if err != nil {
		return nil, err
	}
	out := &compileOutput{}
	out.Body.Spec = spec
	out.Body.Questions = questions
	return out, nil
}

// compileSpec turns an instruction into a spec, and lists what the user has to answer when ask is true
// previous is the spec of a job whose instruction changed, so the parts that still hold, such as its output names, stay as they are
func (m *Module) compileSpec(ctx context.Context, wid, instruction string, previous *Spec, ask bool) (Spec, []Question, error) {
	ws, err := m.deps.Settings.Get(ctx, wid)
	if err != nil {
		return Spec{}, nil, err
	}
	modelID := ws.UtilityModelID
	if modelID == nil {
		modelID = ws.AgentModelID
	}
	if modelID == nil {
		return Spec{}, nil, errNoCompileModel
	}
	provider, model, err := m.deps.Models.ResolveModel(ctx, wid, *modelID)
	if err != nil {
		return Spec{}, nil, err
	}

	// No run or spend limit counts the compile step's model calls, so each person gets only a few of them a minute in a workspace
	err = middleware.CheckRateLimit(ctx, m.deps.CompileLimiter, "compile:"+wid+":"+principal.CallerID(ctx))
	if err != nil {
		return Spec{}, nil, err
	}

	// Configured MCP servers are listed so the model can match needs to real servers
	var servers []string
	if m.deps.MCP != nil {
		servers, _ = m.deps.MCP.ServerNames(ctx, wid)
	}
	user := "Job description:\n" + instruction + "\n\nToday is " + time.Now().UTC().Format("Monday 2006-01-02") + "."
	if len(servers) > 0 {
		user += "\nConfigured MCP servers: " + strings.Join(servers, ", ")
	} else {
		user += "\nNo MCP servers are configured yet."
	}
	if previous != nil {
		current, _ := json.Marshal(previous)
		user += "\n\nThe description was edited, and this is the spec compiled from the one before. Keep the input and output names and everything that still matches the description, and change only what the edit changed:\n" + string(current)
	}
	if !ask {
		user += "\n\nAsk no questions this time: return an empty questions list and settle anything still open with the most sensible choice."
	}

	// Local models can take minutes for a structured spec, so the limit is generous
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var answer compiledSpec
	_, err = llm.Structured(callCtx, provider, llm.Request{
		Model:        model,
		System:       []llm.Block{{Text: fmt.Sprintf(compileSystem, ws.DefaultImage), CacheBreakpoint: true}},
		Messages:     []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart(user)}}},
		MaxTokens:    4096,
		Effort:       llm.EffortLow,
		OutputSchema: specSchema,
		OutputName:   "job_spec",
	}, &answer)
	if err != nil {
		return Spec{}, nil, apperror.ProviderError(err, "The model could not compile the job")
	}

	spec := answer.Spec
	normalizeSpec(&spec)
	return spec, cleanQuestions(answer.Questions, ask), nil
}

// cleanQuestions drops empty questions and answers, and every question when none were allowed
func cleanQuestions(in []Question, ask bool) []Question {
	out := []Question{}
	if !ask {
		return out
	}
	for _, q := range in {
		q.Question = strings.TrimSpace(q.Question)
		if q.Question == "" {
			continue
		}
		options := []string{}
		for _, o := range q.Options {
			o = strings.TrimSpace(o)
			if o != "" && !slices.Contains(options, o) && len(options) < maxOptions {
				options = append(options, o)
			}
		}
		q.Options = options
		out = append(out, q)
		if len(out) == maxQuestions {
			break
		}
	}
	return out
}

// description is the part of a spec compiled from the instruction, as opposed to the name, schedule, network and Dockerfile, which mirror the job's settings
type description struct {
	Goal            string
	SuccessCriteria []string
	Inputs          []IOField
	Outputs         []IOField
	MCP             []MCPNeed
	SideEffects     []string
}

func (s Spec) description() description {
	normalizeSpec(&s)
	return description{Goal: s.Goal, SuccessCriteria: s.SuccessCriteria, Inputs: s.Inputs, Outputs: s.Outputs, MCP: s.MCP, SideEffects: s.SideEffects}
}

func (s *Spec) setDescription(d description) {
	s.Goal = d.Goal
	s.SuccessCriteria = d.SuccessCriteria
	s.Inputs = d.Inputs
	s.Outputs = d.Outputs
	s.MCP = d.MCP
	s.SideEffects = d.SideEffects
}

// withRebuiltSpec compiles the spec again when a patch changes the instruction, since the agent works to the success criteria, inputs and outputs next to the instruction
// A patch that describes the job differently than the stored spec brings its own description and keeps it
// A workspace without a model to compile with keeps the stored spec too, since its jobs were described by hand, and so does a patch that opts out with rebuildSpec
func (m *Module) withRebuiltSpec(ctx context.Context, wid, jobID string, patch jobPatch) (jobPatch, error) {
	if patch.Instruction == nil || strings.TrimSpace(*patch.Instruction) == "" {
		return patch, nil
	}
	if patch.RebuildSpec != nil && !*patch.RebuildSpec {
		return patch, nil
	}
	current, err := m.getJob(ctx, wid, jobID)
	if err != nil {
		return patch, err
	}
	stored, err := fieldsOf(current)
	if err != nil {
		return patch, err
	}
	if strings.TrimSpace(*patch.Instruction) == stored.Instruction {
		return patch, nil
	}

	// The settings pages send the stored spec along with every change, which leaves its description for the compile step to replace
	spec := *stored.Spec
	if patch.Spec != nil {
		if !reflect.DeepEqual(patch.Spec.description(), spec.description()) {
			return patch, nil
		}
		spec = *patch.Spec
	}

	// Only the description is taken from the compile step, since the name, schedule, network and Dockerfile are the settings' to change
	compiled, _, err := m.compileSpec(ctx, wid, strings.TrimSpace(*patch.Instruction), stored.Spec, false)
	if errors.Is(err, errNoCompileModel) {
		return patch, nil
	}
	if err != nil {
		return patch, err
	}
	spec.setDescription(compiled.description())
	patch.Spec = &spec
	return patch, nil
}
