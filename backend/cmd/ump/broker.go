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

// valueArg returns the argument, or stdin when it is "-" or missing
func valueArg(args []string, i int) (string, error) {
	if len(args) > i && args[i] != "-" {
		return args[i], nil
	}
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

func pathEscape(s string) string { return url.PathEscape(s) }

func init() {
	register("mcp", command{Summary: "List or call MCP tools through the host: ump mcp tools [server] | ump mcp call <server> <tool> [json|-]", Run: runMCP})
	register("llm", command{Summary: "One LLM call without tools: ump llm [--model utility|agent] [--schema file] [--system text] [prompt|-]", Run: runLLM})
	register("state", command{Summary: "Persistent job state: ump state get <key> | set <key> [value|-] | list", Run: runState})
	register("output", command{Summary: "Structured run output: ump output set <key> [value|-]", Run: runOutput})
	register("step", command{Summary: "Mark progress on the run timeline: ump step <name>", Run: runStep})
	register("fail", command{Summary: "Fail the run with a reason: ump fail <reason>", Run: runFail})
	register("summary", command{Summary: "Set the run's markdown summary: ump summary [text|-]", Run: runSummary})
}

func runMCP(args []string) int {
	c, err := newBrokerClient()
	if err != nil {
		errorf("mcp", "%v", err)
		return 1
	}
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
		if err := c.do(http.MethodGet, path, nil, &tools); err != nil {
			errorf("mcp", "%v", err)
			return 1
		}
		printJSON(tools)
		return 0

	case "call":
		if len(args) < 3 {
			errorf("mcp", "usage: ump mcp call <server> <tool> [json|-]")
			return 2
		}
		raw := "{}"
		if len(args) > 3 {
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
		err = c.do(http.MethodPost, "/v1/mcp/call", map[string]any{"server": args[1], "tool": args[2], "arguments": json.RawMessage(raw)}, &res)
		if err != nil {
			errorf("mcp", "%v", err)
			return 1
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
		switch args[i] {
		case "--model":
			if i+1 < len(args) {
				req["model"] = args[i+1]
				i++
			}
		case "--system":
			if i+1 < len(args) {
				req["system"] = args[i+1]
				i++
			}
		case "--max-tokens":
			if i+1 < len(args) {
				var n int
				_, _ = fmt.Sscan(args[i+1], &n)
				req["maxTokens"] = n
				i++
			}
		case "--schema":
			if i+1 < len(args) {
				schema, err := os.ReadFile(args[i+1]) // #nosec G703 -- the caller names its own schema file inside its own sandbox
				if err != nil {
					errorf("llm", "cannot read schema: %v", err)
					return 1
				}
				if !json.Valid(schema) {
					errorf("llm", "the schema file is not valid JSON")
					return 2
				}
				req["schema"] = json.RawMessage(schema)
				i++
			}
		default:
			prompt = append(prompt, args[i])
		}
	}

	text := strings.Join(prompt, " ")
	if text == "" || text == "-" {
		var err error
		text, err = valueArg(nil, 0)
		if err != nil {
			errorf("llm", "%v", err)
			return 1
		}
	}
	req["prompt"] = text

	c, err := newBrokerClient()
	if err != nil {
		errorf("llm", "%v", err)
		return 1
	}
	var res struct {
		Text string          `json:"text"`
		JSON json.RawMessage `json:"json"`
	}
	if err := c.do(http.MethodPost, "/v1/llm", req, &res); err != nil {
		errorf("llm", "%v", err)
		return 1
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
	c, err := newBrokerClient()
	if err != nil {
		errorf("state", "%v", err)
		return 1
	}
	if len(args) == 0 {
		errorf("state", "usage: ump state get <key> | set <key> [value|-] | list")
		return 2
	}

	switch args[0] {
	case "get":
		if len(args) < 2 {
			errorf("state", "usage: ump state get <key>")
			return 2
		}
		var res struct {
			Value string `json:"value"`
			Found bool   `json:"found"`
		}
		if err := c.do(http.MethodGet, "/v1/state/"+pathEscape(args[1]), nil, &res); err != nil {
			errorf("state", "%v", err)
			return 1
		}
		if !res.Found {
			// A missing key exits with 1 and prints nothing, so scripts can test for it
			return 1
		}
		fmt.Println(res.Value)
		return 0

	case "set":
		if len(args) < 2 {
			errorf("state", "usage: ump state set <key> [value|-]")
			return 2
		}
		value, err := valueArg(args, 2)
		if err != nil {
			errorf("state", "%v", err)
			return 1
		}
		if err := c.do(http.MethodPut, "/v1/state/"+pathEscape(args[1]), map[string]string{"value": value}, nil); err != nil {
			errorf("state", "%v", err)
			return 1
		}
		return 0

	case "list":
		var res map[string]string
		if err := c.do(http.MethodGet, "/v1/state", nil, &res); err != nil {
			errorf("state", "%v", err)
			return 1
		}
		printJSON(res)
		return 0

	default:
		errorf("state", "unknown subcommand %q", args[0])
		return 2
	}
}

func runOutput(args []string) int {
	if len(args) < 2 || args[0] != "set" {
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

	c, err := newBrokerClient()
	if err != nil {
		errorf("output", "%v", err)
		return 1
	}
	if err := c.do(http.MethodPost, "/v1/output", map[string]any{"key": args[1], "value": raw}, nil); err != nil {
		errorf("output", "%v", err)
		return 1
	}
	return 0
}

func runStep(args []string) int {
	if len(args) == 0 {
		errorf("step", "usage: ump step <name>")
		return 2
	}
	c, err := newBrokerClient()
	if err != nil {
		errorf("step", "%v", err)
		return 1
	}
	if err := c.do(http.MethodPost, "/v1/step", map[string]string{"name": strings.Join(args, " ")}, nil); err != nil {
		errorf("step", "%v", err)
		return 1
	}
	return 0
}

func runFail(args []string) int {
	reason := strings.Join(args, " ")
	if reason == "" {
		reason = "the script reported a failure"
	}
	c, err := newBrokerClient()
	if err != nil {
		errorf("fail", "%v", err)
		return 1
	}
	if err := c.do(http.MethodPost, "/v1/fail", map[string]string{"reason": reason}, nil); err != nil {
		errorf("fail", "%v", err)
	}
	// fail always exits non-zero so `set -e` scripts stop here
	return 1
}

func runSummary(args []string) int {
	text, err := valueArg(args, 0)
	if err != nil {
		errorf("summary", "%v", err)
		return 1
	}
	c, err := newBrokerClient()
	if err != nil {
		errorf("summary", "%v", err)
		return 1
	}
	if err := c.do(http.MethodPost, "/v1/summary", map[string]string{"summary": text}, nil); err != nil {
		errorf("summary", "%v", err)
		return 1
	}
	return 0
}
