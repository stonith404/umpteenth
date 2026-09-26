package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
)

const (
	// The model sees at most this much output per command, the rest stays in the sandbox log
	outputHead = 8_000
	outputTail = 22_000
	// read_file reads at most this many bytes of a file
	maxReadBytes    = 2 << 20
	defaultReadRows = 2000
	maxLineChars    = 2000
)

// OutputSink receives live command output for the UI
type OutputSink func(callID string, chunk []byte)

// SandboxTools is the set of tools that operate inside the run's sandbox
type SandboxTools struct {
	Sandbox        sandbox.Sandbox
	User           sandbox.User
	Env            map[string]string
	DefaultTimeout time.Duration
	MaxTimeout     time.Duration
	Sink           OutputSink
}

// Tools returns the sandbox-backed built-in tools
func (s *SandboxTools) Tools() []Tool {
	return []Tool{&bashTool{s}, &readFileTool{s}, &writeFileTool{s}, &editFileTool{s}}
}

var callIDSanitizer = regexp.MustCompile(`[^A-Za-z0-9_-]`)

type bashTool struct{ s *SandboxTools }

func (t *bashTool) ReadOnly() bool { return false }

func (t *bashTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "bash",
		Description: "Run a shell command with `bash -lc` in /workspace inside the sandbox. Files persist between calls, shell variables and `cd` do not. stdout and stderr are combined. Long output is truncated for you, but the full output is saved to the log file named in the result, which you can grep.",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"command":{"type":"string","description":"The command to run"},` +
			`"timeout_sec":{"type":"integer","minimum":1,"maximum":600,"description":"Timeout in seconds, default 120"}` +
			`},"required":["command"],"additionalProperties":false}`),
	}
}

func (t *bashTool) Run(ctx context.Context, call llm.ToolCall) Result {
	var args struct {
		Command    string `json:"command"`
		TimeoutSec int    `json:"timeout_sec"`
	}
	if err := DecodeArgs(call, &args); err != nil || strings.TrimSpace(args.Command) == "" {
		return Errorf("bash needs a non-empty `command` string.")
	}

	timeout := t.s.DefaultTimeout
	if args.TimeoutSec > 0 {
		timeout = min(time.Duration(args.TimeoutSec)*time.Second, t.s.MaxTimeout)
	}

	// eval runs the command as the shell would, PIPESTATUS reports its own exit code without forcing pipefail onto the pipelines inside it
	script := `mkdir -p /ump/logs; { eval "$2"; } < /dev/null 2>&1 | tee "$1"; exit "${PIPESTATUS[0]}"`
	return t.s.exec(ctx, call.ID, "ump-bash", script, []string{args.Command}, timeout)
}

// exec runs a bash script whose output goes through tee, so the full output lands in the sandbox while it streams live
// The script gets the log path as $1 followed by args
func (s *SandboxTools) exec(ctx context.Context, callID, argv0, script string, args []string, timeout time.Duration) Result {
	logPath := "/ump/logs/" + callIDSanitizer.ReplaceAllString(callID, "_") + ".txt"
	capture := newHeadTail(outputHead, outputTail)
	sink := &sinkWriter{callID: callID, sink: s.Sink, next: capture}

	res, err := s.Sandbox.Exec(ctx, sandbox.ExecRequest{
		Cmd:     append([]string{"bash", "-lc", script, argv0, logPath}, args...),
		WorkDir: sandbox.WorkspaceDir,
		Env:     s.Env,
		User:    s.User,
		Stdout:  sink,
		Stderr:  sink,
		Timeout: timeout,
	})
	if err != nil {
		if errors.Is(err, sandbox.ErrSandboxGone) {
			return Errorf("The sandbox is gone (it may have run out of memory). The run cannot continue.")
		}
		return Errorf("The command could not be started: %v", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "exit code: %d\n", res.ExitCode)
	switch {
	case res.TimedOut:
		fmt.Fprintf(&b, "[the command timed out after %s and was killed]\n", timeout)
	case res.OOMKilled:
		b.WriteString("[the command was killed because the sandbox ran out of memory]\n")
	}
	output, truncated := capture.String()
	b.WriteString(output)
	if truncated {
		fmt.Fprintf(&b, "\n[output truncated: %d bytes in total, the full output is in %s — use grep, head or sed on it]", capture.total, logPath)
	}

	return Result{
		Content: b.String(),
		IsError: res.ExitCode != 0 || res.TimedOut,
		Meta:    map[string]any{"exitCode": res.ExitCode, "timedOut": res.TimedOut, "oomKilled": res.OOMKilled, "bytes": capture.total, "logPath": logPath},
	}
}

type readFileTool struct{ s *SandboxTools }

func (t *readFileTool) ReadOnly() bool { return true }

func (t *readFileTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "read_file",
		Description: "Read a text file from the sandbox with line numbers. Relative paths are relative to /workspace.",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"path":{"type":"string"},` +
			`"offset":{"type":"integer","minimum":1,"description":"1-based line to start at"},` +
			`"limit":{"type":"integer","minimum":1,"description":"Maximum number of lines, default 2000"}` +
			`},"required":["path"],"additionalProperties":false}`),
	}
}

func (t *readFileTool) Run(ctx context.Context, call llm.ToolCall) Result {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := DecodeArgs(call, &args); err != nil || args.Path == "" {
		return Errorf("read_file needs a `path`.")
	}

	content, err := t.s.readAsUser(ctx, resolvePath(args.Path))
	if err != nil {
		return Errorf("%v", err)
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return Errorf("%s looks like a binary file; inspect it with bash instead.", args.Path)
	}

	lines := strings.Split(string(content), "\n")
	offset := max(args.Offset, 1)
	limit := args.Limit
	if limit <= 0 {
		limit = defaultReadRows
	}
	if offset > len(lines) {
		return Result{Content: fmt.Sprintf("[the file has only %d lines]", len(lines))}
	}

	end := min(offset-1+limit, len(lines))
	var b strings.Builder
	for i := offset - 1; i < end; i++ {
		line := lines[i]
		if len(line) > maxLineChars {
			line = line[:maxLineChars] + " [line truncated]"
		}
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
	}
	if end < len(lines) {
		fmt.Fprintf(&b, "[showing lines %d-%d of %d; use offset to read more]", offset, end, len(lines))
	}
	return Result{Content: b.String()}
}

type writeFileTool struct{ s *SandboxTools }

func (t *writeFileTool) ReadOnly() bool { return false }

func (t *writeFileTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "write_file",
		Description: "Create or overwrite a file in the sandbox. Relative paths are relative to /workspace. Parent directories are created.",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"path":{"type":"string"},` +
			`"content":{"type":"string"}` +
			`},"required":["path","content"],"additionalProperties":false}`),
	}
}

func (t *writeFileTool) Run(ctx context.Context, call llm.ToolCall) Result {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := DecodeArgs(call, &args); err != nil || args.Path == "" {
		return Errorf("write_file needs `path` and `content`.")
	}
	p := resolvePath(args.Path)
	if err := t.s.writeAsUser(ctx, p, []byte(args.Content)); err != nil {
		return Errorf("%v", err)
	}
	return Result{Content: fmt.Sprintf("Wrote %d bytes to %s.", len(args.Content), p)}
}

type editFileTool struct{ s *SandboxTools }

func (t *editFileTool) ReadOnly() bool { return false }

func (t *editFileTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "edit_file",
		Description: "Replace an exact string in a file. Fails unless `old` occurs exactly once, or set replace_all to replace every occurrence.",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"path":{"type":"string"},` +
			`"old":{"type":"string","description":"Exact text to replace"},` +
			`"new":{"type":"string","description":"Replacement text"},` +
			`"replace_all":{"type":"boolean"}` +
			`},"required":["path","old","new"],"additionalProperties":false}`),
	}
}

func (t *editFileTool) Run(ctx context.Context, call llm.ToolCall) Result {
	var args struct {
		Path       string `json:"path"`
		Old        string `json:"old"`
		New        string `json:"new"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := DecodeArgs(call, &args); err != nil || args.Path == "" || args.Old == "" {
		return Errorf("edit_file needs `path`, a non-empty `old` and `new`.")
	}

	p := resolvePath(args.Path)
	content, err := t.s.readAsUser(ctx, p)
	if err != nil {
		return Errorf("%v", err)
	}

	count := strings.Count(string(content), args.Old)
	switch {
	case count == 0:
		return Errorf("`old` was not found in %s. Read the file and copy the text exactly.", p)
	case count > 1 && !args.ReplaceAll:
		return Errorf("`old` occurs %d times in %s. Add surrounding context to make it unique, or set replace_all.", count, p)
	}

	updated := strings.ReplaceAll(string(content), args.Old, args.New)
	if err := t.s.writeAsUser(ctx, p, []byte(updated)); err != nil {
		return Errorf("%v", err)
	}
	return Result{Content: fmt.Sprintf("Replaced %d occurrence(s) in %s.", count, p)}
}

// readAsUser reads through an exec as the agent user, so file permissions inside the sandbox apply
func (s *SandboxTools) readAsUser(ctx context.Context, p string) ([]byte, error) {
	// head limits the output, but a root job can replace head, so the host caps what it keeps too
	out, errOut := &cappedBuffer{max: maxReadBytes + 1}, &cappedBuffer{max: 64 << 10}
	res, err := s.Sandbox.Exec(ctx, sandbox.ExecRequest{
		Cmd:     []string{"sh", "-c", `head -c "$2" -- "$1"`, "ump-read", p, fmt.Sprint(maxReadBytes + 1)},
		User:    s.User,
		Stdout:  out,
		Stderr:  errOut,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", p, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("could not read %s: %s", p, strings.TrimSpace(errOut.String()))
	}
	if out.Len() > maxReadBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes; inspect it with bash (head, grep, sed)", p, maxReadBytes)
	}
	return out.Bytes(), nil
}

// writeAsUser writes through an exec as the agent user, so the agent cannot write where it has no permission
func (s *SandboxTools) writeAsUser(ctx context.Context, p string, content []byte) error {
	errOut := &cappedBuffer{max: 64 << 10}
	res, err := s.Sandbox.Exec(ctx, sandbox.ExecRequest{
		Cmd:     []string{"sh", "-c", `mkdir -p -- "$(dirname -- "$1")" && cat > "$1"`, "ump-write", p},
		User:    s.User,
		Stdin:   bytes.NewReader(content),
		Stderr:  errOut,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("could not write %s: %w", p, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("could not write %s: %s", p, strings.TrimSpace(errOut.String()))
	}
	return nil
}

func resolvePath(p string) string {
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Join(sandbox.WorkspaceDir, p)
}

// StateStore persists per-job key-value state across runs
type StateStore interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
}

// StateTools returns state_get and state_set bound to the job's state
func StateTools(store StateStore) []Tool {
	return []Tool{&stateGetTool{store}, &stateSetTool{store}}
}

type stateGetTool struct{ store StateStore }

func (t *stateGetTool) ReadOnly() bool { return true }

func (t *stateGetTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "state_get",
		Description: "Read a value from this job's persistent state, which survives between runs (e.g. the last seen item ID).",
		Schema:      json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`),
	}
}

func (t *stateGetTool) Run(ctx context.Context, call llm.ToolCall) Result {
	var args struct {
		Key string `json:"key"`
	}
	if err := DecodeArgs(call, &args); err != nil || args.Key == "" {
		return Errorf("state_get needs a `key`.")
	}
	value, ok, err := t.store.Get(ctx, args.Key)
	if err != nil {
		return Errorf("Failed to read state: %v", err)
	}
	if !ok {
		return Result{Content: "(not set)"}
	}
	return Result{Content: value}
}

type stateSetTool struct{ store StateStore }

func (t *stateSetTool) ReadOnly() bool { return false }

func (t *stateSetTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "state_set",
		Description: "Store a value in this job's persistent state for future runs. Values are strings; store JSON for structured data.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"},"value":{"type":"string"}},"required":["key","value"],"additionalProperties":false}`),
	}
}

func (t *stateSetTool) Run(ctx context.Context, call llm.ToolCall) Result {
	var args struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := DecodeArgs(call, &args); err != nil || args.Key == "" {
		return Errorf("state_set needs `key` and `value`.")
	}
	if err := t.store.Set(ctx, args.Key, args.Value); err != nil {
		return Errorf("Failed to save state: %v", err)
	}
	return Result{Content: "Saved."}
}

// Notes collects remember() notes for reflection
type Notes struct {
	mu    sync.Mutex
	items []string
}

// All returns the collected notes
func (n *Notes) All() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.items...)
}

// RememberTool returns the remember tool writing into notes
func RememberTool(notes *Notes) Tool { return &rememberTool{notes} }

type rememberTool struct{ notes *Notes }

func (t *rememberTool) ReadOnly() bool { return true }

func (t *rememberTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "remember",
		Description: "Flag something surprising for future runs of this job: an edge case, a workaround, something you had to install. It is reviewed after the run.",
		Schema:      json.RawMessage(`{"type":"object","properties":{"note":{"type":"string"}},"required":["note"],"additionalProperties":false}`),
	}
}

func (t *rememberTool) Run(_ context.Context, call llm.ToolCall) Result {
	var args struct {
		Note string `json:"note"`
	}
	if err := DecodeArgs(call, &args); err != nil || strings.TrimSpace(args.Note) == "" {
		return Errorf("remember needs a `note`.")
	}
	t.notes.mu.Lock()
	t.notes.items = append(t.notes.items, strings.TrimSpace(args.Note))
	t.notes.mu.Unlock()
	return Result{Content: "Noted."}
}

// FinishTool returns the required end-of-run tool
func FinishTool() Tool { return finishTool{} }

type finishTool struct{}

func (finishTool) ReadOnly() bool { return false }

func (finishTool) Def() llm.ToolDef {
	return llm.ToolDef{
		Name:        "finish",
		Description: "End the run. Call this exactly once when the job is done or cannot be done. Give a short markdown summary of what happened and any structured outputs the job defines.",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"status":{"type":"string","enum":["success","failure"]},` +
			`"summary":{"type":"string","description":"Markdown summary of what was done"},` +
			`"outputs":{"type":"object","description":"Structured outputs, keyed by output name"}` +
			`},"required":["status","summary"],"additionalProperties":false}`),
	}
}

func (finishTool) Run(_ context.Context, call llm.ToolCall) Result {
	var f Finish
	if err := DecodeArgs(call, &f); err != nil || (f.Status != "success" && f.Status != "failure") {
		return Errorf("finish needs `status` (success or failure) and `summary`.")
	}
	return Result{Content: "Run finished.", Finish: &f}
}

// headTail keeps the beginning and end of a stream without buffering all of it
type headTail struct {
	head      []byte
	tail      []byte
	headLimit int
	tailLimit int
	total     int
}

func newHeadTail(head, tail int) *headTail {
	return &headTail{headLimit: head, tailLimit: tail}
}

// NewHeadTail returns a writer that keeps only the first head and last tail bytes, for output whose size the sandbox controls
func NewHeadTail(head, tail int) interface {
	io.Writer
	String() (string, bool)
} {
	return newHeadTail(head, tail)
}

// cappedBuffer keeps the first max bytes written to it and drops the rest, so a sandbox can't make the host buffer without end
type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.Len(); room > 0 {
		c.Buffer.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (h *headTail) Write(p []byte) (int, error) {
	h.total += len(p)
	rest := p
	if room := h.headLimit - len(h.head); room > 0 {
		n := min(room, len(rest))
		h.head = append(h.head, rest[:n]...)
		rest = rest[n:]
	}
	if len(rest) > 0 {
		h.tail = append(h.tail, rest...)
		if len(h.tail) > h.tailLimit {
			h.tail = append(h.tail[:0], h.tail[len(h.tail)-h.tailLimit:]...)
		}
	}
	return len(p), nil
}

// String returns the kept output and whether bytes in the middle were dropped
func (h *headTail) String() (string, bool) {
	dropped := h.total - len(h.head) - len(h.tail)
	if dropped <= 0 {
		return string(h.head) + string(h.tail), false
	}
	return string(h.head) + fmt.Sprintf("\n\n[… %d bytes omitted …]\n\n", dropped) + string(h.tail), true
}

// sinkWriter streams output live while also capturing it; the mutex keeps stdout and stderr writes from interleaving mid-chunk
type sinkWriter struct {
	mu     sync.Mutex
	callID string
	sink   OutputSink
	next   *headTail
}

func (w *sinkWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sink != nil {
		w.sink(w.callID, append([]byte(nil), p...))
	}
	return w.next.Write(p)
}
