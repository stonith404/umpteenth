package reflection

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/reflection/reflectiondb"
)

// Size limits of the condensed transcript, which must fit the reflection prompt next to the playbook
const (
	maxTranscriptChars = 60_000
	maxArgsChars       = 1_500
	maxTextChars       = 1_500
	resultHead         = 900
	resultTail         = 700
	shortResultHead    = 300
	shortResultTail    = 200
)

// localInstall recognizes npm, pnpm, yarn and bun adding named packages, which in a run usually means the agent fetched a library for its own script
// A script may do the same as its own work in a cloned repository, so only the transcript counts it, while playbook.FindInstall decides what scripts may not do
var localInstall = regexp.MustCompile(`(?i)\b(?:npm\s+(?:install|i|add)|pnpm\s+add|yarn\s+add|bun\s+add)\s+(?:-\S+\s+)*[@A-Za-z]`)

// Timings is how much of a run went into setup and installing software by hand
type Timings struct {
	SetupMs   int64
	InstallMs int64
	Installs  []string
}

// transcript is the condensed story of a run
type transcript struct {
	Text    string
	Notes   []string
	Timings Timings
	// NothingToDo is set when the agent finished saying the run found nothing to do
	NothingToDo bool
}

type toolCallPayload struct {
	CallID string          `json:"callId"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
}

type toolResultPayload struct {
	CallID  string `json:"callId"`
	Content string `json:"content"`
	IsError bool   `json:"isError"`
}

type llmCallPayload struct {
	Turn  int    `json:"turn"`
	Text  string `json:"text"`
	Error string `json:"error"`
}

// buildTranscript condenses a run's events into text reflection can read
// Tool results are shortened further when the whole transcript would not fit, and as a last resort the middle of the run is dropped
func buildTranscript(rows []reflectiondb.RunEventsRow, redact func(string) string) transcript {
	t := buildWithLimits(rows, redact, resultHead, resultTail)
	if len(t.Text) > maxTranscriptChars {
		t = buildWithLimits(rows, redact, shortResultHead, shortResultTail)
	}
	if len(t.Text) > maxTranscriptChars {
		t.Text, _ = agent.Truncate(t.Text, maxTranscriptChars*2/5, maxTranscriptChars*3/5)
	}
	return t
}

func buildWithLimits(rows []reflectiondb.RunEventsRow, redact func(string) string, head, tail int) transcript {
	var (
		t       transcript
		b       strings.Builder
		pending = map[string]string{}
		// installers are the base names of files the run wrote that install software
		installers []string
	)
	line := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteString("\n")
	}

	// Text is redacted before it is shortened, since a cut through a secret would leave the part before the cut unredacted
	clip := func(s string, keepHead, keepTail int) string {
		out, _ := agent.Truncate(redact(s), keepHead, keepTail)
		return out
	}
	for _, row := range rows {
		ms := int64(0)
		if row.Ms != nil {
			ms = *row.Ms
		}
		switch row.Type {
		case events.TypeLLMCall:
			var p llmCallPayload
			_ = json.Unmarshal([]byte(row.Payload), &p)
			if p.Error != "" {
				line("[turn %d] model call failed: %s", p.Turn, redact(p.Error))
			}
			if text := strings.TrimSpace(p.Text); text != "" {
				line("[turn %d] assistant: %s", p.Turn, clip(text, maxTextChars, 0))
			}
		case events.TypeToolCall:
			var p toolCallPayload
			_ = json.Unmarshal([]byte(row.Payload), &p)
			pending[p.CallID] = commandOf(p)

			// A script the agent wrote that installs software counts as an install once a bash command runs it
			if file, text := writtenFile(p); file != "" && playbook.FindInstall(text) != "" {
				installers = append(installers, path.Base(file))
			}
			if p.Name == "remember" {
				var note struct {
					Note string `json:"note"`
				}
				_ = json.Unmarshal(p.Args, &note)
				t.Notes = append(t.Notes, redact(note.Note))
			}
			line("[call %s] %s", p.Name, clip(string(p.Args), maxArgsChars, 0))
		case events.TypeToolResult:
			var p toolResultPayload
			_ = json.Unmarshal([]byte(row.Payload), &p)
			status := "ok"
			if p.IsError {
				status = "error"
			}
			line("  -> %s after %s: %s", status, seconds(ms), clip(strings.TrimSpace(p.Content), head, tail))
			if cmd := pending[p.CallID]; cmd != "" && installs(cmd, installers) {
				t.Timings.InstallMs += ms
				t.Timings.Installs = append(t.Timings.Installs, clip(cmd, 200, 0))
			}
		case events.TypeSandboxSetup:
			var p struct {
				ExitCode int    `json:"exitCode"`
				Output   string `json:"output"`
			}
			_ = json.Unmarshal([]byte(row.Payload), &p)
			t.Timings.SetupMs += ms
			line("[setup script] exit %d after %s: %s", p.ExitCode, seconds(ms), clip(strings.TrimSpace(p.Output), head, tail))
		case events.TypeImageWait:
			if ms > 0 {
				line("[waited %s for the job image to build]", seconds(ms))
			}
		case events.TypeBrokerCall, events.TypeScriptStep:
			line("[%s] %s", row.Type, clip(row.Payload, 300, 0))
		case events.TypeVerifyResult:
			line("[verify] %s", clip(row.Payload, 2000, 0))
		case events.TypeFallback:
			line("[fallback to the agent] %s", clip(row.Payload, 1000, 0))
		case events.TypeError:
			var p struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal([]byte(row.Payload), &p)
			line("[error] %s", redact(p.Message))
		case events.TypeFinish:
			var p struct {
				NothingToDo bool `json:"nothingToDo"`
			}
			_ = json.Unmarshal([]byte(row.Payload), &p)
			t.NothingToDo = p.NothingToDo
			line("[finish] %s", clip(row.Payload, 3000, 0))
		}
	}
	t.Text = b.String()
	return t
}

// commandOf returns the shell command of a bash call, the only kind of call that installs packages by hand
func commandOf(p toolCallPayload) string {
	if p.Name != "bash" {
		return ""
	}
	var args struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(p.Args, &args)
	return args.Command
}

// writtenFile returns the path and new text of a write_file or edit_file call
func writtenFile(p toolCallPayload) (string, string) {
	if p.Name != "write_file" && p.Name != "edit_file" {
		return "", ""
	}
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		New     string `json:"new"`
	}
	_ = json.Unmarshal(p.Args, &args)
	return args.Path, args.Content + args.New
}

// installs reports whether a bash command installs software, directly or by running a file the run wrote that does
func installs(cmd string, installers []string) bool {
	if playbook.FindInstall(cmd) != "" || localInstall.MatchString(cmd) {
		return true
	}

	// A file the run wrote counts only when the command executes it, since git add or an editor naming a repository's Dockerfile installs nothing
	return slices.ContainsFunc(installers, func(name string) bool { return runsFile(cmd, name) })
}

// interpreters run the file named in their arguments, such as bash get_node.sh or python3 get_node.py
var interpreters = regexp.MustCompile(`^(?:bash|sh|zsh|dash|source|\.|python(?:3(?:\.\d+)?)?|node|bun|deno|tsx|ts-node|ruby|perl)$`)

// runsFile reports whether a shell command executes a file with the given base name, as a command of its own or through an interpreter
func runsFile(cmd, name string) bool {
	replacer := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n")
	for part := range strings.SplitSeq(replacer.Replace(cmd), "\n") {
		// Variable assignments and wrappers such as sudo come before the command that runs
		fields := strings.Fields(part)
		for len(fields) > 0 && (strings.Contains(fields[0], "=") || fields[0] == "sudo" || fields[0] == "env") {
			fields = fields[1:]
		}
		if len(fields) == 0 {
			continue
		}

		// The file runs as the command itself, such as ./get_node.sh or /workspace/get_node.sh
		if path.Base(fields[0]) == name {
			return true
		}

		// Runners such as uv run and npx tsx pass the file on to an interpreter
		args := fields[1:]
		switch {
		case fields[0] == "uv" && len(args) > 0 && args[0] == "run", fields[0] == "npx" && len(args) > 0 && args[0] == "tsx":
			args = args[1:]
		case !interpreters.MatchString(fields[0]):
			continue
		}
		if slices.ContainsFunc(args, func(arg string) bool { return path.Base(arg) == name }) {
			return true
		}
	}
	return false
}

// newRedactor replaces the job's secret values, so they never reach the reflection model or the playbook
func newRedactor(env map[string]string) func(string) string {
	type secret struct{ name, value string }
	var secrets []secret
	for name, value := range env {
		if len(value) < playbook.MinSecretLen {
			continue
		}

		// Payloads reach the transcript as JSON too, where the encoder escapes quotes, backslashes and, by default, <, > and &
		for _, form := range jsonForms(value) {
			secrets = append(secrets, secret{name, form})
		}
	}
	// Longer values go first, so a secret that contains another is replaced as a whole
	slices.SortFunc(secrets, func(a, b secret) int { return len(b.value) - len(a.value) })
	return func(s string) string {
		for _, sec := range secrets {
			s = strings.ReplaceAll(s, sec.value, "[secret $"+sec.name+"]")
		}
		return s
	}
}

func truncated(s string, n int) string {
	out, _ := agent.Truncate(s, n, 0)
	return out
}

func seconds(ms int64) string {
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

// jsonForms returns a value as written raw and inside JSON strings, with and without HTML escaping
func jsonForms(value string) []string {
	forms := []string{value}
	for _, escapeHTML := range []bool{true, false} {
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(escapeHTML)
		if enc.Encode(value) != nil {
			continue
		}
		encoded := strings.TrimSuffix(b.String(), "\n")
		if form := encoded[1 : len(encoded)-1]; !slices.Contains(forms, form) {
			forms = append(forms, form)
		}
	}
	return forms
}
