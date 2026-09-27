// Command gencatalog snapshots models.dev into the catalog bundled with the llm package, run through go generate
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gencatalog <output file>")
		os.Exit(2)
	}
	err := run(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "gencatalog:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Download the models.dev document
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, llm.ModelsDevURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("models.dev answered %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Keep only what the catalog uses, indented so a regenerated snapshot reviews as a readable diff
	c, err := llm.ParseModelsDev(data, time.Now().UTC())
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	err = enc.Encode(c)
	if err != nil {
		return err
	}
	// #nosec G703 -- go generate passes the output path
	return os.WriteFile(out, buf.Bytes(), 0o600)
}
