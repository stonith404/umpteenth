package playbook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Verify is how a scripted run proves it did the job
type Verify struct {
	// Checks are deterministic conditions every scripted run must meet
	Checks []string `json:"checks"`
	// LLM asks the utility model to judge the result against the success criteria, for results a check can't judge
	LLM bool `json:"llm"`
	// Varies names outputs whose value may differ between two runs moments apart, which a shadow run doesn't compare with the run it repeats
	Varies []string `json:"varies,omitempty"`
}

// ParseVerify reads a playbook's verify value, where null means no checks beyond the built-in ones
func ParseVerify(raw json.RawMessage) (Verify, error) {
	var v Verify
	if len(raw) == 0 || string(raw) == "null" {
		return v, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf(`verify must look like {"checks":["exit_code == 0"],"llm":false,"varies":[]}: %w`, err)
	}
	for _, c := range v.Checks {
		if _, err := ParseCheck(c); err != nil {
			return v, err
		}
	}
	return v, nil
}

// Check is one parsed verify condition
type Check struct {
	kind  string
	key   string
	op    string
	value json.RawMessage
}

// Check kinds
const (
	checkExitCode = "exit_code"
	checkOutput   = "output"
	checkFile     = "file"
	checkStdout   = "stdout"
)

var (
	exitCodeCheck = regexp.MustCompile(`^exit_code\s*(==|!=)\s*(-?\d+)$`)
	outputCheck   = regexp.MustCompile(`^output\.([A-Za-z0-9_.-]+)\s+(exists|==|!=|>=|<=|>|<)\s*(.*)$`)
	fileCheck     = regexp.MustCompile(`^file\s+(\S+)\s+exists$`)
	stdoutCheck   = regexp.MustCompile(`^stdout\s+contains\s+(".*")$`)
)

// CheckSyntax lists the supported forms, for error messages and the reflection prompt
const CheckSyntax = `exit_code == 0 · output.<name> exists · output.<name> <op> <JSON value> with op one of == != > >= < <= · file /ump/outputs/<path> exists · stdout contains "<text>"`

// ParseCheck parses one verify condition
func ParseCheck(s string) (Check, error) {
	src := strings.TrimSpace(s)
	if m := exitCodeCheck.FindStringSubmatch(src); m != nil {
		return Check{kind: checkExitCode, op: m[1], value: json.RawMessage(m[2])}, nil
	}
	if m := outputCheck.FindStringSubmatch(src); m != nil {
		op, value := m[2], strings.TrimSpace(m[3])
		if op == "exists" {
			if value != "" {
				return Check{}, fmt.Errorf("check %q: exists takes no value", src)
			}
			return Check{kind: checkOutput, key: m[1], op: op}, nil
		}
		if !json.Valid([]byte(value)) {
			return Check{}, fmt.Errorf("check %q: the value must be JSON, e.g. 3, \"text\" or true", src)
		}
		return Check{kind: checkOutput, key: m[1], op: op, value: json.RawMessage(value)}, nil
	}
	if m := fileCheck.FindStringSubmatch(src); m != nil {
		if !strings.HasPrefix(m[1], "/ump/outputs/") && !strings.HasPrefix(m[1], "/workspace/") {
			return Check{}, fmt.Errorf("check %q: files must be under /ump/outputs/ or /workspace/", src)
		}
		return Check{kind: checkFile, key: m[1]}, nil
	}
	if m := stdoutCheck.FindStringSubmatch(src); m != nil {
		var text string
		if err := json.Unmarshal([]byte(m[1]), &text); err != nil {
			return Check{}, fmt.Errorf("check %q: the text must be a JSON string", src)
		}
		return Check{kind: checkStdout, key: text}, nil
	}
	return Check{}, fmt.Errorf("check %q is not supported, use one of: %s", src, CheckSyntax)
}

// CheckInput is what a scripted run produced
type CheckInput struct {
	ExitCode int
	Outputs  map[string]json.RawMessage
	Stdout   string
	// FileExists looks a path up in the sandbox
	FileExists func(path string) bool
}

// Eval reports whether the check holds, and why not when it doesn't
func (c Check) Eval(in CheckInput) (bool, string) {
	switch c.kind {
	case checkExitCode:
		want, _ := strconv.Atoi(string(c.value))
		if (in.ExitCode == want) == (c.op == "==") {
			return true, ""
		}
		return false, fmt.Sprintf("exit code was %d", in.ExitCode)
	case checkOutput:
		got, ok := in.Outputs[c.key]
		if !ok || string(got) == "null" {
			return false, fmt.Sprintf("output %s was not set", c.key)
		}
		if c.op == "exists" {
			return true, ""
		}
		ok, err := compareJSON(got, c.op, c.value)
		if err != nil {
			return false, fmt.Sprintf("output %s is %s: %v", c.key, got, err)
		}
		if !ok {
			return false, fmt.Sprintf("output %s is %s", c.key, got)
		}
		return true, ""
	case checkFile:
		if in.FileExists != nil && in.FileExists(c.key) {
			return true, ""
		}
		return false, fmt.Sprintf("%s does not exist", c.key)
	case checkStdout:
		if strings.Contains(in.Stdout, c.key) {
			return true, ""
		}
		return false, fmt.Sprintf("stdout does not contain %q", c.key)
	}
	return false, "unknown check"
}

// SameValue reports whether two outputs hold the same JSON value, where numbers compare numerically so 2 and 2.0 match
func SameValue(a, b json.RawMessage) bool {
	equal, err := compareJSON(a, "==", b)
	return err == nil && equal
}

// compareJSON compares numbers numerically and everything else by its JSON value
func compareJSON(got json.RawMessage, op string, want json.RawMessage) (bool, error) {
	var a, b any
	if json.Unmarshal(got, &a) != nil || json.Unmarshal(want, &b) != nil {
		return false, errors.New("not valid JSON")
	}
	af, aNum := a.(float64)
	bf, bNum := b.(float64)
	switch op {
	case "==", "!=":
		var equal bool
		if aNum && bNum {
			equal = af == bf
		} else {
			ca, _ := json.Marshal(a)
			cb, _ := json.Marshal(b)
			equal = bytes.Equal(ca, cb)
		}
		return equal == (op == "=="), nil
	}
	if !aNum || !bNum {
		return false, fmt.Errorf("%s compares numbers only", op)
	}
	switch op {
	case ">":
		return af > bf, nil
	case ">=":
		return af >= bf, nil
	case "<":
		return af < bf, nil
	}
	return af <= bf, nil
}
