package reflection

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/events"
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

// installCommand recognizes package installs, the work the environment rule moves into the Dockerfile (PLAN.md §9.2)
// uv run --with and npx fetch packages on the fly, which also repeats in every run
var installCommand = regexp.MustCompile(`(?i)\b(pip3?\s+install|uv\s+(pip\s+install|tool\s+install|add)|uv\s+run\b[^|;&\n]*--with|uvx\s|npx\s|npm\s+(install|i)\b|pnpm\s+(add|install)|yarn\s+add|apt(-get)?\s+install|apk\s+add|cargo\s+install|go\s+install|gem\s+install)`)

// Timings is how much of a run went into preparing its environment
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
}

type toolCallPayload struct {
	CallID string          `json:"callId"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
}

type toolResultPayload struct {
	CallID  string `json:"callId"`
	Name    string `json:"name"`
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
	)
	line := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteString("\n")
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
				text, _ = agent.Truncate(text, maxTextChars, 0)
				line("[turn %d] assistant: %s", p.Turn, redact(text))
			}
		case events.TypeToolCall:
			var p toolCallPayload
			_ = json.Unmarshal([]byte(row.Payload), &p)
			args, _ := agent.Truncate(string(p.Args), maxArgsChars, 0)
			pending[p.CallID] = commandOf(p)
			if p.Name == "remember" {
				var note struct {
					Note string `json:"note"`
				}
				_ = json.Unmarshal(p.Args, &note)
				t.Notes = append(t.Notes, redact(note.Note))
			}
			line("[call %s] %s", p.Name, redact(args))
		case events.TypeToolResult:
			var p toolResultPayload
			_ = json.Unmarshal([]byte(row.Payload), &p)
			content, _ := agent.Truncate(strings.TrimSpace(p.Content), head, tail)
			status := "ok"
			if p.IsError {
				status = "error"
			}
			line("  -> %s after %s: %s", status, seconds(ms), redact(content))
			if cmd := pending[p.CallID]; cmd != "" && installCommand.MatchString(cmd) {
				t.Timings.InstallMs += ms
				short, _ := agent.Truncate(cmd, 200, 0)
				t.Timings.Installs = append(t.Timings.Installs, redact(short))
			}
		case events.TypeSandboxSetup:
			var p struct {
				ExitCode int    `json:"exitCode"`
				Output   string `json:"output"`
			}
			_ = json.Unmarshal([]byte(row.Payload), &p)
			t.Timings.SetupMs += ms
			out, _ := agent.Truncate(strings.TrimSpace(p.Output), head, tail)
			line("[setup script] exit %d after %s: %s", p.ExitCode, seconds(ms), redact(out))
		case events.TypeImageWait:
			if ms > 0 {
				line("[waited %s for the job image to build]", seconds(ms))
			}
		case events.TypeBrokerCall, events.TypeScriptStep:
			line("[%s] %s", row.Type, redact(truncated(row.Payload, 300)))
		case events.TypeVerifyResult:
			line("[verify] %s", redact(truncated(row.Payload, 2000)))
		case events.TypeFallback:
			line("[fallback to the agent] %s", redact(truncated(row.Payload, 1000)))
		case events.TypeError:
			var p struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal([]byte(row.Payload), &p)
			line("[error] %s", redact(p.Message))
		case events.TypeFinish:
			line("[finish] %s", redact(truncated(row.Payload, 3000)))
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

// newRedactor replaces the job's secret values, so they never reach the reflection model or the playbook
func newRedactor(env map[string]string) func(string) string {
	type secret struct{ name, value string }
	var secrets []secret
	for name, value := range env {
		if len(value) < 6 {
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
