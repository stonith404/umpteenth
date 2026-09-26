package playbook

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Operations reflection can apply to a playbook (PLAN.md §9.2, §10.3)
const (
	OpAddLearning    = "add_learning"
	OpUpdateLearning = "update_learning"
	OpRetireLearning = "retire_learning"
	OpUpsertScript   = "upsert_script"
	OpDeleteScript   = "delete_script"
	OpSetSetup       = "set_setup"
	OpSetDockerfile  = "set_dockerfile"
	OpProposeMain    = "propose_main"
	OpUpdateMain     = "update_main"
	OpSetVerify      = "set_verify"
)

// OpKinds lists every operation, in the order the reflection schema offers them
var OpKinds = []string{OpAddLearning, OpUpdateLearning, OpRetireLearning, OpUpsertScript, OpDeleteScript, OpSetSetup, OpSetDockerfile, OpProposeMain, OpUpdateMain, OpSetVerify}

// What happened to an operation
const (
	OpApplied  = "applied"
	OpHeld     = "held"
	OpRejected = "rejected"
)

// Limits that keep a playbook small enough for the prompt and the sandbox
const (
	// RenderBudgetTokens is the size budget of the rendered playbook in the prompt
	RenderBudgetTokens = 3000
	maxLearningText    = 600
	maxLearningKind    = 32
	maxLearningWhen    = 200
	maxLearningSources = 5
	maxScripts         = 20
	maxScriptBytes     = 64 << 10
	maxSetupBytes      = 16 << 10
	maxDockerfileBytes = 16 << 10
	maxMainBytes       = 64 << 10
	maxChecks          = 20
	// inputPath is where a run's input is, which a main script reads instead of carrying input values
	inputPath = "/ump/input.json"
)

// Op is one change to a playbook, as proposed by reflection
// Every field is present in the JSON so strict structured output can describe it, and fields an operation doesn't use are null
type Op struct {
	Op        string `json:"op" enum:"add_learning,update_learning,retire_learning,upsert_script,delete_script,set_setup,set_dockerfile,propose_main,update_main,set_verify"`
	Rationale string `json:"rationale"`
	// ID names the learning for update_learning and retire_learning
	ID   *string `json:"id"`
	Kind *string `json:"kind"`
	Text *string `json:"text"`
	When *string `json:"when"`
	// Name names the script for upsert_script and delete_script
	Name *string `json:"name"`
	// Content is the script for upsert_script, propose_main and update_main, the new text for set_setup and set_dockerfile, where null removes it, and the verify JSON for set_verify
	Content *string `json:"content"`
}

// AppliedOp records the outcome of one operation, stored with the version so the UI can explain every change
type AppliedOp struct {
	Op
	Status string `json:"status" enum:"applied,held,rejected"`
	// Target is the learning ID or script name the operation changed, which a new learning only gets while it is applied
	Target string `json:"target,omitempty"`
	// Reason explains a rejected operation
	Reason string `json:"reason,omitempty"`
	// Flags are safety concerns; a held operation is not applied until someone applies it by hand
	Flags []string `json:"flags,omitempty"`
	// Test is how the main script did in a shadow run before the operation was applied
	Test *OpTest `json:"test,omitempty"`
}

// Outcomes of the shadow run that tries a main script before it is applied (PLAN.md §10.4)
const (
	TestPassed  = "passed"
	TestFailed  = "failed"
	TestSkipped = "skipped"
)

// OpTest is what a shadow run showed about a main script, or why there was none
type OpTest struct {
	Status string `json:"status" enum:"passed,failed,skipped"`
	Detail string `json:"detail"`
	// Output is the end of what main printed when it failed
	Output string `json:"output,omitempty"`
}

// ApplyContext is what validation needs to know beyond the playbook
type ApplyContext struct {
	SourceRunID string
	// BaseImage is the image the job uses without a Dockerfile, which a Dockerfile may build on without review
	BaseImage string
	// Secrets are values that must never be written into the playbook
	Secrets []string
	// Graduation tells whether the job qualifies for a main script, and NotGraduated explains why not (PLAN.md §10.3)
	Graduation   bool
	NotGraduated string
	// Inputs are the names of the inputs the job declares, which a main script must read from the run input
	Inputs []string
}

// Apply applies operations in order to a copy of the content
// Invalid operations are rejected and risky ones are held, so one bad operation doesn't block the others
func Apply(c Content, ops []Op, actx ApplyContext) (Content, []AppliedOp) {
	next := c.clone()
	results := make([]AppliedOp, 0, len(ops))
	for _, op := range ops {
		// Each operation is tried on a scratch copy, which only replaces the result once the operation is accepted
		scratch := next.clone()
		target, flags, err := scratch.applyOp(op, actx)
		res := AppliedOp{Op: op, Target: target}
		switch {
		case err != nil:
			res.Status, res.Reason = OpRejected, err.Error()
		case hasBlockingFlag(flags):
			res.Status, res.Flags = OpHeld, flags
		default:
			res.Status, res.Flags = OpApplied, flags
			next = scratch
		}
		results = append(results, res)
	}
	return next, results
}

// OverBudget reports how many tokens the rendered playbook is over its prompt budget, or zero when it fits
func (c Content) OverBudget() int {
	tokens := EstimateTokens(c.Render())
	if tokens <= RenderBudgetTokens {
		return 0
	}
	return tokens - RenderBudgetTokens
}

// EstimateTokens approximates a token count from text length, which is close enough for a size budget
func EstimateTokens(s string) int {
	return (len(s) + 3) / 4
}

func (c Content) clone() Content {
	out := c
	out.Learnings = make([]Learning, len(c.Learnings))
	for i, l := range c.Learnings {
		l.Sources = slices.Clone(l.Sources)
		out.Learnings[i] = l
	}
	out.Toolkit = make([]Script, len(c.Toolkit))
	for i, s := range c.Toolkit {
		s.Sources = slices.Clone(s.Sources)
		s.Args = slices.Clone(s.Args)
		out.Toolkit[i] = s
	}
	out.Normalize()
	return out
}

func (c *Content) applyOp(op Op, actx ApplyContext) (string, []string, error) {
	switch op.Op {
	case OpAddLearning:
		return c.addLearning(op, actx)
	case OpUpdateLearning:
		return c.updateLearning(op, actx)
	case OpRetireLearning:
		return c.retireLearning(op)
	case OpUpsertScript:
		return c.upsertScript(op, actx)
	case OpDeleteScript:
		return c.deleteScript(op)
	case OpSetSetup:
		return c.setSetup(op, actx)
	case OpSetDockerfile:
		return c.setDockerfile(op, actx)
	case OpProposeMain:
		return c.proposeMain(op, actx)
	case OpUpdateMain:
		return c.updateMain(op, actx)
	case OpSetVerify:
		return c.setVerify(op)
	}
	return "", nil, fmt.Errorf("unknown operation %q, use one of %s", op.Op, strings.Join(OpKinds, ", "))
}

func (c *Content) addLearning(op Op, actx ApplyContext) (string, []string, error) {
	text := trimmed(op.Text)
	if text == "" {
		return "", nil, fmt.Errorf("add_learning needs text")
	}
	if len(text) > maxLearningText {
		return "", nil, fmt.Errorf("text is %d characters, keep a learning under %d", len(text), maxLearningText)
	}
	for _, l := range c.Learnings {
		if l.Status != "retired" && strings.EqualFold(l.Text, text) {
			return "", nil, fmt.Errorf("the same learning exists as %s, use update_learning instead", l.ID)
		}
	}
	kind, err := learningKind(op.Kind, "fact")
	if err != nil {
		return "", nil, err
	}
	when, err := learningWhen(op.When)
	if err != nil {
		return "", nil, err
	}

	id := c.nextLearningID()
	l := Learning{ID: id, Kind: kind, Text: text, When: when, Status: "active"}
	if actx.SourceRunID != "" {
		l.Sources = []string{actx.SourceRunID}
	}
	c.Learnings = append(c.Learnings, l)
	return id, textFlags(text, actx), nil
}

func (c *Content) updateLearning(op Op, actx ApplyContext) (string, []string, error) {
	i, err := c.learningIndex(op.ID)
	if err != nil {
		return "", nil, err
	}
	l := &c.Learnings[i]
	if text := trimmed(op.Text); text != "" {
		if len(text) > maxLearningText {
			return l.ID, nil, fmt.Errorf("text is %d characters, keep a learning under %d", len(text), maxLearningText)
		}
		l.Text = text
	}
	if op.Kind != nil {
		kind, err := learningKind(op.Kind, l.Kind)
		if err != nil {
			return l.ID, nil, err
		}
		l.Kind = kind
	}
	if op.When != nil {
		when, err := learningWhen(op.When)
		if err != nil {
			return l.ID, nil, err
		}
		l.When = when
	}

	// Updating a retired learning brings it back, since reflection found it relevant again
	l.Status = "active"
	l.Sources = addSource(l.Sources, actx.SourceRunID)
	return l.ID, textFlags(l.Text, actx), nil
}

func (c *Content) retireLearning(op Op) (string, []string, error) {
	i, err := c.learningIndex(op.ID)
	if err != nil {
		return "", nil, err
	}
	if c.Learnings[i].Status == "retired" {
		return c.Learnings[i].ID, nil, fmt.Errorf("learning %s is already retired", c.Learnings[i].ID)
	}
	c.Learnings[i].Status = "retired"
	return c.Learnings[i].ID, nil, nil
}

func (c *Content) upsertScript(op Op, actx ApplyContext) (string, []string, error) {
	content := strings.TrimSpace(deref(op.Content))
	if content == "" {
		return "", nil, fmt.Errorf("upsert_script needs the script as content")
	}
	if len(content) > maxScriptBytes {
		return "", nil, fmt.Errorf("the script is %d bytes, the limit is %d", len(content), maxScriptBytes)
	}
	header, err := ParseScriptHeader(content)
	if err != nil {
		return "", nil, err
	}

	// The name can come from the operation or the header, and when both are given they must agree
	name := trimmed(op.Name)
	switch {
	case name == "" && header.Name == "":
		return "", nil, fmt.Errorf("upsert_script needs a name, in the operation or as # ump:name in the script")
	case name == "":
		name = header.Name
	case header.Name != "" && header.Name != name:
		return name, nil, fmt.Errorf("the operation names the script %q but its header says %q", name, header.Name)
	}
	if !ValidScriptName(name) {
		return name, nil, fmt.Errorf("script name %q must start with a letter or digit and only contain letters, digits, dots, dashes and underscores", name)
	}
	if header.Description == "" {
		return name, nil, fmt.Errorf("the script needs a # ump:description header line")
	}

	// Toolkit scripts are executed directly, so a script without an interpreter line gets one
	if !strings.HasPrefix(content, "#!") {
		content = shebangFor(header.Lang) + "\n" + content
	}
	content += "\n"

	var args json.RawMessage
	if len(header.Args) > 0 {
		args, _ = json.Marshal(header.Args)
	}
	s := Script{Name: name, Lang: header.Lang, Description: header.Description, Args: args, SideEffects: header.SideEffects, Content: content}
	i := slices.IndexFunc(c.Toolkit, func(s Script) bool { return s.Name == name })
	if i >= 0 {
		// A new version of a script keeps its track record and where it came from
		s.Stats = c.Toolkit[i].Stats
		s.Sources = addSource(c.Toolkit[i].Sources, actx.SourceRunID)
		c.Toolkit[i] = s
	} else {
		if len(c.Toolkit) >= maxScripts {
			return name, nil, fmt.Errorf("the toolkit already has %d scripts, delete or merge one first", maxScripts)
		}
		s.Sources = addSource(nil, actx.SourceRunID)
		c.Toolkit = append(c.Toolkit, s)
	}
	return name, secretFlags(content, actx), nil
}

func (c *Content) deleteScript(op Op) (string, []string, error) {
	name := trimmed(op.Name)
	i := slices.IndexFunc(c.Toolkit, func(s Script) bool { return s.Name == name })
	if i < 0 {
		return name, nil, fmt.Errorf("there is no toolkit script named %q", name)
	}
	c.Toolkit = slices.Delete(c.Toolkit, i, i+1)
	return name, nil, nil
}

func (c *Content) setSetup(op Op, actx ApplyContext) (string, []string, error) {
	setup := strings.TrimSpace(deref(op.Content))
	if setup == "" {
		c.Setup = nil
		return "setup", nil, nil
	}
	if len(setup) > maxSetupBytes {
		return "setup", nil, fmt.Errorf("the setup script is %d bytes, the limit is %d", len(setup), maxSetupBytes)
	}
	c.Setup = &setup
	return "setup", secretFlags(setup, actx), nil
}

func (c *Content) setDockerfile(op Op, actx ApplyContext) (string, []string, error) {
	dockerfile := strings.TrimSpace(deref(op.Content))
	if dockerfile == "" {
		c.Dockerfile = nil
		return "dockerfile", nil, nil
	}
	if len(dockerfile) > maxDockerfileBytes {
		return "dockerfile", nil, fmt.Errorf("the Dockerfile is %d bytes, the limit is %d", len(dockerfile), maxDockerfileBytes)
	}
	// Checks read the Dockerfile the way the builder does, since a line continuation could otherwise hide an instruction from them
	logical := logicalDockerfile(dockerfile)
	froms := dockerfileFroms(logical)
	if len(froms) == 0 {
		return "dockerfile", nil, fmt.Errorf("the Dockerfile must start with FROM, usually FROM %s", actx.BaseImage)
	}

	// Images builds are less isolated than runs, so anything that pulls in new code from elsewhere waits for a person (PLAN.md §9.4)
	known := []string{actx.BaseImage}
	if c.Dockerfile != nil {
		known = append(known, dockerfileFroms(logicalDockerfile(*c.Dockerfile))...)
	}
	flags := secretFlags(dockerfile, actx)
	for _, from := range froms {
		if !slices.Contains(known, from) {
			flags = append(flags, fmt.Sprintf("%s: builds on a new base image %s", FlagBlocking, from))
		}
	}
	for _, pattern := range riskyDockerfile {
		if pattern.re.MatchString(logical) {
			flags = append(flags, FlagBlocking+": "+pattern.reason)
		}
	}
	c.Dockerfile = &dockerfile
	return "dockerfile", flags, nil
}

// proposeMain graduates the job: its main script does the whole job without the agent (PLAN.md §10.3)
func (c *Content) proposeMain(op Op, actx ApplyContext) (string, []string, error) {
	if !actx.Graduation {
		reason := actx.NotGraduated
		if reason == "" {
			reason = "the graduation criteria are not met"
		}
		return "main", nil, fmt.Errorf("propose_main is not allowed yet: %s", reason)
	}
	return c.setMain(op, actx)
}

// updateMain repairs or improves the main script of a job that already graduated
func (c *Content) updateMain(op Op, actx ApplyContext) (string, []string, error) {
	if c.Main == nil {
		return "main", nil, fmt.Errorf("the job has no main script yet, only propose_main can add one")
	}
	return c.setMain(op, actx)
}

func (c *Content) setMain(op Op, actx ApplyContext) (string, []string, error) {
	main := strings.TrimSpace(deref(op.Content))
	if main == "" {
		return "main", nil, fmt.Errorf("%s needs the complete script as content", op.Op)
	}
	if len(main) > maxMainBytes {
		return "main", nil, fmt.Errorf("the main script is %d bytes, the limit is %d", len(main), maxMainBytes)
	}
	header, err := ParseScriptHeader(main)
	if err != nil {
		return "main", nil, err
	}

	// A main script that carries an input's value instead of reading it silently ignores every run that sets a different one
	if len(actx.Inputs) > 0 && !strings.Contains(main, inputPath) {
		noun := "input"
		if len(actx.Inputs) > 1 {
			noun = "inputs"
		}
		return "main", nil, fmt.Errorf("the job declares the %s %s, but main never reads %s: read them from there, with the default the job describes for runs that don't set them, instead of writing their values into the script", noun, strings.Join(actx.Inputs, ", "), inputPath)
	}
	if !strings.HasPrefix(main, "#!") {
		main = shebangFor(header.Lang) + "\n" + main
	}
	main += "\n"
	c.Main = &main
	return "main", secretFlags(main, actx), nil
}

// setVerify sets the checks every scripted run must pass
func (c *Content) setVerify(op Op) (string, []string, error) {
	raw := strings.TrimSpace(deref(op.Content))
	if raw == "" || raw == "null" {
		c.Verify = nil
		return "verify", nil, nil
	}
	v, err := ParseVerify(json.RawMessage(raw))
	if err != nil {
		return "verify", nil, err
	}
	if len(v.Checks) > maxChecks {
		return "verify", nil, fmt.Errorf("verify has %d checks, keep it to %d", len(v.Checks), maxChecks)
	}
	if len(v.Varies) > maxChecks {
		return "verify", nil, fmt.Errorf("verify names %d varying outputs, keep it to %d", len(v.Varies), maxChecks)
	}
	if slices.ContainsFunc(v.Varies, func(name string) bool { return strings.TrimSpace(name) == "" }) {
		return "verify", nil, fmt.Errorf("varies must name outputs, not empty strings")
	}
	if v.Checks == nil {
		v.Checks = []string{}
	}
	c.Verify, _ = json.Marshal(v)
	return "verify", nil, nil
}

// FlagBlocking prefixes a flag that holds an operation back instead of only warning about it
const FlagBlocking = "needs review"

func hasBlockingFlag(flags []string) bool {
	return slices.ContainsFunc(flags, func(f string) bool { return strings.HasPrefix(f, FlagBlocking) })
}

var riskyDockerfile = []struct {
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n|]*\|\s*(sudo\s+)?(ba|z|da)?sh\b`), "pipes a download into a shell"},
	{regexp.MustCompile(`(?im)^\s*ADD\b[^\n]*(\bhttps?://|\bgit@)`), "adds a file straight from a URL"},
	{regexp.MustCompile(`(?im)^\s*COPY\b[^\n]*--from=\S*[/:.]`), "copies files out of another image"},
}

var (
	// escapeDirective is the parser directive that changes the line continuation character to a backtick
	escapeDirective = regexp.MustCompile("(?m)\\A(?:#[^\\n]*\\n)*#\\s*escape\\s*=\\s*`")
	continuation    = regexp.MustCompile(`\\[ \t]*\r?\n`)
	backtickCont    = regexp.MustCompile("`[ \\t]*\\r?\\n")
)

// logicalDockerfile joins continued lines, so every instruction sits on one line as the builder sees it
func logicalDockerfile(dockerfile string) string {
	if escapeDirective.MatchString(dockerfile) {
		return backtickCont.ReplaceAllString(dockerfile, "")
	}
	return continuation.ReplaceAllString(dockerfile, "")
}

var (
	credentialPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}`),
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}`),
		regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`),
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+`),
	}
	urlPattern = regexp.MustCompile(`https?://[^\s)]+`)
)

// secretFlags holds back text that contains one of the job's secrets or something shaped like a credential
func secretFlags(text string, actx ApplyContext) []string {
	for _, secret := range actx.Secrets {
		if len(secret) >= 6 && strings.Contains(text, secret) {
			return []string{FlagBlocking + ": contains the value of one of the job's secrets"}
		}
	}
	for _, re := range credentialPatterns {
		if re.MatchString(text) {
			return []string{FlagBlocking + ": contains something that looks like a credential"}
		}
	}
	return nil
}

// textFlags checks a learning, which the agent reads as guidance in every future run
func textFlags(text string, actx ApplyContext) []string {
	flags := secretFlags(text, actx)
	if urlPattern.MatchString(text) {
		flags = append(flags, "mentions a URL, check that it came from the job and not from fetched content")
	}
	return flags
}

// dockerfileFroms lists the images a Dockerfile builds on, skipping stages that build on an earlier stage
func dockerfileFroms(dockerfile string) []string {
	var froms, stages []string
	for _, line := range strings.Split(dockerfile, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		args := fields[1:]
		for len(args) > 0 && strings.HasPrefix(args[0], "--") {
			args = args[1:]
		}
		if len(args) == 0 {
			continue
		}
		image := args[0]
		if !slices.Contains(stages, strings.ToLower(image)) {
			froms = append(froms, image)
		}
		if len(args) >= 3 && strings.EqualFold(args[1], "AS") {
			stages = append(stages, strings.ToLower(args[2]))
		}
	}
	return froms
}

func (c *Content) nextLearningID() string {
	highest := 0
	for _, l := range c.Learnings {
		if n, err := strconv.Atoi(strings.TrimPrefix(l.ID, "L")); err == nil && n > highest {
			highest = n
		}
	}
	return "L" + strconv.Itoa(highest+1)
}

func (c *Content) learningIndex(id *string) (int, error) {
	want := trimmed(id)
	if want == "" {
		return -1, fmt.Errorf("the operation needs the learning's id")
	}
	i := slices.IndexFunc(c.Learnings, func(l Learning) bool { return strings.EqualFold(l.ID, want) })
	if i < 0 {
		return -1, fmt.Errorf("there is no learning %s", want)
	}
	return i, nil
}

func learningKind(kind *string, fallback string) (string, error) {
	k := trimmed(kind)
	if k == "" {
		return fallback, nil
	}
	if len(k) > maxLearningKind {
		return "", fmt.Errorf("kind must be a short word such as edge_case, workaround, fact or environment")
	}
	return strings.ToLower(strings.ReplaceAll(k, " ", "_")), nil
}

func learningWhen(when *string) (string, error) {
	w := trimmed(when)
	if len(w) > maxLearningWhen {
		return "", fmt.Errorf("when is %d characters, keep it under %d", len(w), maxLearningWhen)
	}
	return w, nil
}

// addSource records a run as a source, keeping only the most recent ones
func addSource(sources []string, runID string) []string {
	if runID == "" || slices.Contains(sources, runID) {
		return sources
	}
	sources = append(slices.Clone(sources), runID)
	if len(sources) > maxLearningSources {
		sources = sources[len(sources)-maxLearningSources:]
	}
	return sources
}

func trimmed(s *string) string {
	return strings.TrimSpace(deref(s))
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
