// Package backup exports a workspace's configuration to a JSON document and imports it again, through the public API (PLAN.md §16, M6)
// Working through the API keeps every validation, schedule and HA rule of the server, and works against any instance an API token reaches
package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client calls the Umpteenth API with an API token
type Client struct {
	base  string
	token string
	http  *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), token: token, http: &http.Client{Timeout: time.Minute}}
}

// APIError is an error answer of the API
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API answered %d: %s", e.Status, e.Message)
}

// do sends one request and decodes the JSON answer into out
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach %s: %w", c.base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var problem struct {
			Message string `json:"message"`
			Fields  []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"fields"`
		}
		msg := strings.TrimSpace(string(data))
		if json.Unmarshal(data, &problem) == nil && problem.Message != "" {
			msg = problem.Message
			for _, f := range problem.Fields {
				msg += fmt.Sprintf("; %s %s", f.Field, f.Message)
			}
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// listAll pages through a list endpoint
func listAll[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	for page := 1; ; page++ {
		var res struct {
			Items []T `json:"items"`
			Total int `json:"total"`
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		err := c.do(ctx, http.MethodGet, path+sep+"page="+strconv.Itoa(page)+"&pageSize=100", nil, &res)
		if err != nil {
			return nil, err
		}
		all = append(all, res.Items...)
		if len(res.Items) == 0 || len(all) >= res.Total {
			return all, nil
		}
	}
}

func esc(s string) string {
	return url.PathEscape(s)
}
