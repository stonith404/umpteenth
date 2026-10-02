package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/middleware"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
)

// specSchema is the structured output the utility model must produce
var specSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["title", "goal", "schedule", "successCriteria", "inputs", "outputs", "mcp", "skills", "secrets", "network", "dockerfile", "sideEffects", "questions"],
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
    "skills": {"type": "array", "items": {"type": "string"}, "description": "Exact names of configured skills that fit the job"},
    "secrets": {"type": "array", "items": {"type": "object", "additionalProperties": false, "required": ["envName", "secret", "why"], "properties": {
      "envName": {"type": "string", "description": "Environment variable the job's commands read the credential from, e.g. GITHUB_TOKEN"},
      "secret": {"type": "string", "description": "Exact name of the configured secret that holds the value, or empty when none does"},
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
- skills lists the configured skills whose description fits the job, by their exact name. Leave it empty when none fit.
- secrets lists the credentials the job's own commands read from environment variables, such as an API token for a CLI, named as the tool expects them. Set secret to the exact name of the configured secret that holds the value, or leave it empty when none does. MCP servers keep their own credentials, so a job needs no secret for them.
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

// maxSecretNeeds caps the suggested secrets, in case the model lists more than any job needs
const maxSecretNeeds = 20

// envNameRe mirrors the pattern the job secrets endpoint takes for environment variable names
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,99}$`)

// SecretNeed is an environment variable the job's commands read a credential from, and the configured secret that holds it
type SecretNeed struct {
	EnvName string `json:"envName"`
	Secret  string `json:"secret" doc:"Name of the configured secret that holds the value, empty when none does"`
	Why     string `json:"why"`
}

// maxCompileSkills and maxSkillDescription bound the skill catalog the compile step sees, which grows with the workspace
const (
	maxCompileSkills    = 50
	maxSkillDescription = 300
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
		Spec      Spec         `json:"spec"`
		Questions []Question   `json:"questions"`
		Skills    []string     `json:"skills" doc:"Names of configured skills that fit the job"`
		Secrets   []SecretNeed `json:"secrets" doc:"Credentials the job's commands need as environment variables"`
	}
}

// compiledSpec is the utility model's answer, a spec plus the questions it could not settle on its own and the skills that fit
type compiledSpec struct {
	Spec
	Questions []Question   `json:"questions"`
	Skills    []string     `json:"skills"`
	Secrets   []SecretNeed `json:"secrets"`
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
	compiled, err := m.compileSpec(ctx, principal.WorkspaceID(ctx), in.Body.Instruction, nil, ask)
	if err != nil {
		return nil, err
	}
	out := &compileOutput{}
	out.Body.Spec = compiled.Spec
	out.Body.Questions = compiled.Questions
	out.Body.Skills = compiled.Skills
	out.Body.Secrets = compiled.Secrets
	return out, nil
}

// compileSpec turns an instruction into a spec, and lists what the user has to answer when ask is true
// previous is the spec of a job whose instruction changed, so the parts that still hold, such as its output names, stay as they are
func (m *Module) compileSpec(ctx context.Context, wid, instruction string, previous *Spec, ask bool) (compiledSpec, error) {
	ws, err := m.deps.Settings.Get(ctx, wid)
	if err != nil {
		return compiledSpec{}, err
	}
	modelID := ws.UtilityModelID
	if modelID == nil {
		modelID = ws.AgentModelID
	}
	if modelID == nil {
		return compiledSpec{}, errNoCompileModel
	}
	provider, model, err := m.deps.Models.ResolveModel(ctx, wid, *modelID)
	if err != nil {
		return compiledSpec{}, err
	}

	// No run or spend limit counts the compile step's model calls, so each person gets only a few of them a minute in a workspace
	err = middleware.CheckRateLimit(ctx, m.deps.CompileLimiter, "compile:"+wid+":"+principal.CallerID(ctx))
	if err != nil {
		return compiledSpec{}, err
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

	// Configured skills are listed with their descriptions, since a name alone rarely says when a skill fits
	var skills []runner.Skill
	if m.deps.Skills != nil {
		skills, _ = m.deps.Skills.Skills(ctx, wid)
	}
	user += skillCatalog(skills)

	// Only secret names are listed, so the model can match a credential to the secret that holds it without ever seeing a value
	var secrets []string
	if m.deps.Secrets != nil {
		secrets, _ = m.deps.Secrets.SecretNames(ctx, wid)
	}
	if len(secrets) > 0 {
		user += "\nConfigured secrets: " + strings.Join(secrets, ", ")
	} else {
		user += "\nNo secrets are configured yet."
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
		return compiledSpec{}, apperror.ProviderError(err, "The model could not compile the job")
	}

	normalizeSpec(&answer.Spec)
	answer.Questions = cleanQuestions(answer.Questions, ask)
	answer.Skills = cleanSkills(answer.Skills, skills)
	answer.Secrets = cleanSecrets(answer.Secrets, secrets)
	return answer, nil
}

// cleanSecrets keeps the needs with a valid environment variable name, once each, and drops a secret name that doesn't exist, so the UI never preselects a secret it can't map
func cleanSecrets(needs []SecretNeed, secrets []string) []SecretNeed {
	out := []SecretNeed{}
	seen := map[string]bool{}
	for _, n := range needs {
		n.EnvName = strings.TrimSpace(n.EnvName)
		if !envNameRe.MatchString(n.EnvName) || seen[n.EnvName] {
			continue
		}
		seen[n.EnvName] = true

		// Secret names are matched ignoring case, since the model may copy a name in another case, and the stored spelling is kept
		name := strings.TrimSpace(n.Secret)
		n.Secret = ""
		for _, s := range secrets {
			if strings.EqualFold(s, name) {
				n.Secret = s
				break
			}
		}
		n.Why = strings.TrimSpace(n.Why)
		out = append(out, n)
		if len(out) == maxSecretNeeds {
			break
		}
	}
	return out
}

// skillCatalog renders the configured skills for the compile prompt
func skillCatalog(skills []runner.Skill) string {
	if len(skills) == 0 {
		return "\nNo skills are configured."
	}
	var b strings.Builder
	b.WriteString("\nConfigured skills:")
	for i, s := range skills {
		if i == maxCompileSkills {
			break
		}
		description := strings.Join(strings.Fields(s.Description), " ")
		if r := []rune(description); len(r) > maxSkillDescription {
			description = string(r[:maxSkillDescription]) + "…"
		}
		fmt.Fprintf(&b, "\n- %s: %s", s.Name, description)
	}
	return b.String()
}

// cleanSkills keeps the suggested skills that exist, once each, so the UI never preselects a skill it can't attach
func cleanSkills(suggested []string, skills []runner.Skill) []string {
	out := []string{}
	for _, name := range suggested {
		name = strings.TrimSpace(name)
		exists := slices.ContainsFunc(skills, func(s runner.Skill) bool { return s.Name == name })
		if exists && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
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
	compiled, err := m.compileSpec(ctx, wid, strings.TrimSpace(*patch.Instruction), stored.Spec, false)
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
