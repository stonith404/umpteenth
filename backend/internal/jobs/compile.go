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
        {"type": "object", "additionalProperties": false, "required": ["cron", "timezone"], "properties": {
          "cron": {"type": "string", "description": "Standard 5-field cron expression"},
          "timezone": {"type": "string", "description": "IANA timezone, e.g. Europe/Berlin: the one the text names, otherwise the writer's, otherwise UTC"}
        }}
      ]
    },
    "successCriteria": {"type": "array", "items": {"type": "string"}, "description": "What a successful run leaves behind"},
    "inputs": {"type": "array", "items": {"$ref": "#/$defs/field"}},
    "outputs": {"type": "array", "items": {"$ref": "#/$defs/field"}, "description": "Small structured values each run reports"},
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
    "dockerfile": {"anyOf": [{"type": "null"}, {"type": "string"}], "description": "Only when the job needs tools the default sandbox image lacks, starting FROM it"},
    "sideEffects": {"type": "array", "items": {"type": "string"}, "description": "Each change the job makes outside the sandbox, with its condition"},
    "questions": {"type": "array", "description": "What the user must answer before the job can be set up, usually nothing", "items": {"type": "object", "additionalProperties": false, "required": ["question", "options"], "properties": {
      "question": {"type": "string", "description": "One short question"},
      "options": {"type": "array", "items": {"type": "string"}, "description": "Up to four likely answers, or none when only the user can know"}
    }}}
  },
  "$defs": {"field": {"type": "object", "additionalProperties": false, "required": ["name", "type", "description"], "properties": {
    "name": {"type": "string"}, "type": {"type": "string", "enum": ["string", "integer", "number", "boolean", "object", "array"]}, "description": {"type": "string"}
  }}}
}`)

const compileSystem = `You turn a plain-language job description into a spec for Umpteenth, which runs jobs in a disposable Linux sandbox with an LLM agent.
The agent reads the description on every run, together with the success criteria and outputs, so the spec never adds work the description doesn't ask for.

Rules:
- Include a schedule only when the text asks for recurring runs, as a 5-field cron expression in the timezone the text names, otherwise in the writer's timezone given below, otherwise in UTC.
- successCriteria describe what a successful run leaves behind, usually one to three. Keep the text's conditions, including what happens when there is nothing to do. Don't restate steps or tools and don't add requirements.
- outputs are small values worth tracking across runs (counts, IDs, flags), never a flag for whether the run succeeded.
- mcp lists the external services the job talks to, by the exact name of a configured MCP server when one fits.
- skills lists the configured skills that fit the job, by their exact name.
- secrets lists the credentials the job's own commands read from environment variables, named as the tool expects them, with the configured secret that holds each one, or empty when none does. MCP servers keep their own credentials.
- network is "none" only when the job clearly needs no internet access.
- Jobs have built-in key-value state between runs, which runs keep on their own, such as the last item handled, so no field or question asks how to remember something.
- The default sandbox image has bash, coreutils, curl, git, jq, ripgrep, python3 with pip and requests, uv, node and npm. Propose a dockerfile only when the job needs more, starting with FROM %s.
- sideEffects lists each change the job makes outside the sandbox with its condition, such as "Posts one message to #eng when there are stale pull requests". Effects only, no advice.
- questions are rare. Ask only when the job can't run correctly without the answer and no sensible default exists, such as which repository or channel it works on, or when a wrong guess could cause a harmful side effect. Never ask what the agent can find out while running. Ask at most three.
- Always ask when a condition that decides a side effect reaches beyond the job's subject, such as "if there were any notifications, send a summary email" in a job about dependency pull requests, and offer both readings.`

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
		Timezone     string `json:"timezone,omitempty" maxLength:"100" doc:"IANA time zone of the person writing the instruction, such as Europe/Zurich, which the schedule uses when the instruction names none; UTC when omitted"`
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

	// A zone that doesn't resolve is refused, since reading the writer's times as UTC instead would shift the schedule silently
	var writerZone *time.Location
	if in.Body.Timezone != "" {
		loc, err := loadTimezone(in.Body.Timezone)
		if err != nil {
			return nil, err
		}
		writerZone = loc
	}

	ask := in.Body.AskQuestions == nil || *in.Body.AskQuestions
	compiled, err := m.compileSpec(ctx, principal.WorkspaceID(ctx), in.Body.Instruction, writerZone, nil, ask)
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
// writerZone is the time zone of the person writing the instruction, which a schedule uses when the text names none, or nil when unknown
// previous is the spec of a job whose instruction changed, so the parts that still hold, such as its output names, stay as they are
func (m *Module) compileSpec(ctx context.Context, wid, instruction string, writerZone *time.Location, previous *Spec, ask bool) (compiledSpec, error) {
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

	// Times the text gives without a zone are meant in the writer's own, which also decides what today is
	now := time.Now().UTC()
	if writerZone != nil {
		now = now.In(writerZone)
	}
	user := "Job description:\n" + instruction + "\n\nToday is " + now.Format("Monday 2006-01-02") + "."
	if writerZone != nil {
		user += " The writer's timezone is " + writerZone.String() + "."
	}

	// Configured MCP servers are listed so the model can match needs to real servers
	var servers []string
	if m.deps.MCP != nil {
		servers, _ = m.deps.MCP.ServerNames(ctx, wid)
	}
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
		user += "\n\nThe description was edited, and this is the spec compiled from the one before. Keep its input and output names, keep the rest where it still matches the description and follows the rules, and change only what the edit changed or what breaks a rule:\n" + string(current)
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
	compiled, err := m.compileSpec(ctx, wid, strings.TrimSpace(*patch.Instruction), nil, stored.Spec, false)
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
