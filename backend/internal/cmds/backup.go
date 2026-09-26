package cmds

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"

	"github.com/stonith404/umpteenth/backend/internal/backup"
)

// backupFlags are the connection flags export and import share
type backupFlags struct {
	url   string
	token string
}

// register adds the connection flags, defaulting to UMPTEENTH_URL or APP_URL and UMPTEENTH_API_TOKEN
func (f *backupFlags) register(cmd *cobra.Command) {
	url := os.Getenv("UMPTEENTH_URL")
	if url == "" {
		url = os.Getenv("APP_URL")
	}
	if url == "" {
		url = "http://localhost:8080"
	}
	cmd.Flags().StringVar(&f.url, "url", url, "Umpteenth to talk to, UMPTEENTH_URL or APP_URL by default")
	cmd.Flags().StringVar(&f.token, "token", os.Getenv("UMPTEENTH_API_TOKEN"), "API token from Settings → API tokens, UMPTEENTH_API_TOKEN by default")
}

func (f *backupFlags) client() (*backup.Client, error) {
	if f.token == "" {
		return nil, errors.New("an API token is required: pass --token or set UMPTEENTH_API_TOKEN")
	}
	return backup.NewClient(f.url, f.token), nil
}

func init() {
	var (
		exportFlags backupFlags
		history     bool
		output      string
	)
	exportCmd := &cobra.Command{
		Use:   "export",
		Short: "Export jobs, playbooks, MCP servers and settings as JSON, without any secret values",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := exportFlags.client()
			if err != nil {
				return err
			}
			doc, err := backup.Export(cmd.Context(), c, backup.ExportOptions{History: history})
			if err != nil {
				return err
			}
			raw, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				return err
			}
			if output == "" || output == "-" {
				_, err = fmt.Println(string(raw))
				return err
			}
			if err := os.WriteFile(output, append(raw, '\n'), 0o600); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Exported %d jobs, %d MCP servers and %d providers to %s\n", len(doc.Jobs), len(doc.MCPServers), len(doc.Providers), output)
			return nil
		},
	}
	exportFlags.register(exportCmd)
	exportCmd.Flags().BoolVar(&history, "history", false, "include every playbook version, not only the current one")
	exportCmd.Flags().StringVarP(&output, "output", "o", "", "write to this file instead of stdout")

	var (
		importFlags backupFlags
		secretsFile string
		dryRun      bool
	)
	importCmd := &cobra.Command{
		Use:   "import <file>",
		Short: "Import an export, creating what the workspace doesn't have yet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := importFlags.client()
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			var doc backup.Document
			if err := json.Unmarshal(raw, &doc); err != nil {
				return fmt.Errorf("%s is not valid JSON: %w", args[0], err)
			}
			opts := backup.ImportOptions{DryRun: dryRun}
			if secretsFile != "" {
				opts.SecretValues, err = godotenv.Read(secretsFile)
				if err != nil {
					return fmt.Errorf("failed to read %s: %w", secretsFile, err)
				}
			}

			report, err := backup.Import(cmd.Context(), c, &doc, opts)
			printReport(report, dryRun)
			return err
		},
	}
	importFlags.register(importCmd)
	importCmd.Flags().StringVar(&secretsFile, "secrets", "", "a KEY=value file with values for secrets the export names")
	importCmd.Flags().BoolVar(&dryRun, "dry-run", false, "only report what the import would do")

	rootCmd.AddCommand(exportCmd, importCmd)
}

func printReport(r *backup.Report, dryRun bool) {
	if r == nil {
		return
	}
	verb := "Created"
	if dryRun {
		verb = "Would create"
	}
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Printf("%s:\n  %s\n", title, strings.Join(lines, "\n  "))
	}
	section(verb, r.Created)
	section("Skipped", r.Skipped)
	section("Left to do", r.Todo)
	if len(r.Created)+len(r.Skipped)+len(r.Todo) == 0 {
		fmt.Println("Nothing to import")
	}
}
