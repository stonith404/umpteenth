package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// brokerClient talks to the host-side broker with the run's token (PLAN.md §4.10)
type brokerClient struct {
	base  string
	token string
	http  *http.Client
}

func newBrokerClient() (*brokerClient, error) {
	base := strings.TrimRight(os.Getenv("UMP_BROKER_URL"), "/")
	token := os.Getenv("UMP_TOKEN")
	if base == "" || token == "" {
		return nil, errors.New("UMP_BROKER_URL and UMP_TOKEN are not set; ump only works inside an Umpteenth sandbox")
	}
	// LLM calls can take minutes, so the timeout is generous
	return &brokerClient{base: base, token: token, http: &http.Client{Timeout: 10 * time.Minute}}, nil
}

// brokerError is the error body of the broker
type brokerError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// do sends a request and decodes the JSON response into out
func (c *brokerClient) do(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	// #nosec G704 -- the broker URL is set by the adapter in the sandbox environment, and reaching it is the point of this client
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req) // #nosec G704 -- same broker URL as above
	if err != nil {
		return fmt.Errorf("broker unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 {
		var be brokerError
		if json.Unmarshal(data, &be) == nil && be.Message != "" {
			return errors.New(be.Message)
		}
		return fmt.Errorf("broker returned %s", resp.Status)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// call sends one request to the broker and returns the exit code, reporting a failure on stderr
func call(cmd, method, path string, body, out any) int {
	c, err := newBrokerClient()
	if err == nil {
		err = c.do(method, path, body, out)
	}
	if err != nil {
		errorf(cmd, "%v", err)
		return 1
	}
	return 0
}

// valueArg returns the argument at i, or stdin when it is "-" or missing
// Callers refuse arguments after it, since they are most likely the rest of an unquoted value
func valueArg(args []string, i int) (string, error) {
	if len(args) > i && args[i] != "-" {
		return args[i], nil
	}
	return readStdin()
}

// textArg joins the arguments into one text, or reads stdin when there are none or only "-"
func textArg(args []string) (string, error) {
	text := strings.Join(args, " ")
	if text != "" && text != "-" {
		return text, nil
	}
	return readStdin()
}

func readStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n"), nil
}

// printJSON writes indented JSON to stdout
func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func init() {
	register("mcp", command{Summary: "List or call MCP tools through the host: ump mcp tools [server] | ump mcp call <server> <tool> [json|-]", Run: runMCP})
	register("llm", command{Summary: "One LLM call without tools: ump llm [--model utility|agent] [--schema file] [--system text] [--max-tokens n] [prompt|-]", Run: runLLM})
	register("state", command{Summary: "Persistent job state: ump state get <key> | set <key> [value|-] | list", Run: runState})
	register("output", command{Summary: "Structured run output: ump output set <key> [value|-]", Run: runOutput})
	register("step", command{Summary: "Mark progress on the run timeline: ump step <name>", Run: runStep})
	register("fail", command{Summary: "Fail the run with a reason: ump fail <reason>", Run: runFail})
	register("summary", command{Summary: "Set the run's markdown summary: ump summary [text|-]", Run: runSummary})
}

func runMCP(args []string) int {
	if len(args) == 0 {
		errorf("mcp", "usage: ump mcp tools [server] | ump mcp call <server> <tool> [json|-]")
		return 2
	}

	switch args[0] {
	case "tools":
		path := "/v1/mcp/tools"
		if len(args) > 1 {
			path += "?server=" + url.QueryEscape(args[1])
		}
		var tools []map[string]any
		if code := call("mcp", http.MethodGet, path, nil, &tools); code != 0 {
			return code
		}
		printJSON(tools)
		return 0

	case "call":
		if len(args) < 3 || len(args) > 4 {
			errorf("mcp", "usage: ump mcp call <server> <tool> [json|-]")
			return 2
		}
		raw := "{}"
		if len(args) > 3 {
			var err error
			raw, err = valueArg(args, 3)
			if err != nil {
				errorf("mcp", "%v", err)
				return 1
			}
		}
		if !json.Valid([]byte(raw)) {
			errorf("mcp", "arguments must be JSON")
			return 2
		}
		var res struct {
			Content string `json:"content"`
			IsError bool   `json:"isError"`
		}
		body := map[string]any{"server": args[1], "tool": args[2], "arguments": json.RawMessage(raw)}
		if code := call("mcp", http.MethodPost, "/v1/mcp/call", body, &res); code != 0 {
			return code
		}
		fmt.Println(res.Content)
		if res.IsError {
			return 1
		}
		return 0

	default:
		errorf("mcp", "unknown subcommand %q", args[0])
		return 2
	}
}

func runLLM(args []string) int {
	req := map[string]any{"model": "utility"}
	var prompt []string
	for i := 0; i < len(args); i++ {
		name := args[i]
		switch name {
		case "--model", "--system", "--max-tokens", "--schema":
		default:
			prompt = append(prompt, name)
			continue
		}

		// Every flag takes a value, and a missing one must not silently turn into a default
		if i+1 >= len(args) {
			errorf("llm", "%s needs a value", name)
			return 2
		}
		i++
		value := args[i]
		switch name {
		case "--model":
			if value != "utility" && value != "agent" {
				errorf("llm", "--model must be utility or agent")
				return 2
			}
			req["model"] = value
		case "--system":
			req["system"] = value
		case "--max-tokens":
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				errorf("llm", "--max-tokens must be a positive number")
				return 2
			}
			req["maxTokens"] = n
		case "--schema":
			schema, err := os.ReadFile(value) // #nosec G304 G703 -- the caller names its own schema file inside its own sandbox
			if err != nil {
				errorf("llm", "cannot read schema: %v", err)
				return 1
			}
			if !json.Valid(schema) {
				errorf("llm", "the schema file is not valid JSON")
				return 2
			}
			req["schema"] = json.RawMessage(schema)
		}
	}

	text, err := textArg(prompt)
	if err != nil {
		errorf("llm", "%v", err)
		return 1
	}
	req["prompt"] = text

	var res struct {
		Text string          `json:"text"`
		JSON json.RawMessage `json:"json"`
	}
	if code := call("llm", http.MethodPost, "/v1/llm", req, &res); code != 0 {
		return code
	}
	if len(res.JSON) > 0 {
		var v any
		_ = json.Unmarshal(res.JSON, &v)
		printJSON(v)
		return 0
	}
	fmt.Println(res.Text)
	return 0
}

func runState(args []string) int {
	if len(args) == 0 {
		errorf("state", "usage: ump state get <key> | set <key> [value|-] | list")
		return 2
	}

	switch args[0] {
	case "get":
		if len(args) != 2 {
			errorf("state", "usage: ump state get <key>")
			return 2
		}
		var res struct {
			Value string `json:"value"`
			Found bool   `json:"found"`
		}
		if code := call("state", http.MethodGet, "/v1/state/"+url.PathEscape(args[1]), nil, &res); code != 0 {
			return code
		}
		if !res.Found {
			// A missing key exits with 1 and prints nothing, so scripts can test for it
			return 1
		}
		fmt.Println(res.Value)
		return 0

	case "set":
		if len(args) < 2 || len(args) > 3 {
			errorf("state", "usage: ump state set <key> [value|-]")
			return 2
		}
		value, err := valueArg(args, 2)
		if err != nil {
			errorf("state", "%v", err)
			return 1
		}
		return call("state", http.MethodPut, "/v1/state/"+url.PathEscape(args[1]), map[string]string{"value": value}, nil)

	case "list":
		var res map[string]string
		if code := call("state", http.MethodGet, "/v1/state", nil, &res); code != 0 {
			return code
		}
		printJSON(res)
		return 0

	default:
		errorf("state", "unknown subcommand %q", args[0])
		return 2
	}
}

func runOutput(args []string) int {
	if len(args) < 2 || len(args) > 3 || args[0] != "set" {
		errorf("output", "usage: ump output set <key> [value|-]")
		return 2
	}
	value, err := valueArg(args, 2)
	if err != nil {
		errorf("output", "%v", err)
		return 1
	}

	// JSON values stay structured, anything else is stored as a string
	var raw json.RawMessage
	if json.Valid([]byte(value)) {
		raw = json.RawMessage(value)
	} else {
		raw, _ = json.Marshal(value)
	}
	return call("output", http.MethodPost, "/v1/output", map[string]any{"key": args[1], "value": raw}, nil)
}

func runStep(args []string) int {
	if len(args) == 0 {
		errorf("step", "usage: ump step <name>")
		return 2
	}
	return call("step", http.MethodPost, "/v1/step", map[string]string{"name": strings.Join(args, " ")}, nil)
}

func runFail(args []string) int {
	reason := strings.Join(args, " ")
	if reason == "" {
		reason = "the script reported a failure"
	}
	_ = call("fail", http.MethodPost, "/v1/fail", map[string]string{"reason": reason}, nil)

	// fail always exits non-zero so `set -e` scripts stop here
	return 1
}

func runSummary(args []string) int {
	text, err := textArg(args)
	if err != nil {
		errorf("summary", "%v", err)
		return 1
	}
	return call("summary", http.MethodPost, "/v1/summary", map[string]string{"summary": text}, nil)
}
