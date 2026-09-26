// Package cmds holds the umpteenth command-line interface
package cmds

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/stonith404/umpteenth/backend/internal/bootstrap"
	"github.com/stonith404/umpteenth/backend/internal/common"
	"github.com/stonith404/umpteenth/backend/internal/config"
)

var rootCmd = &cobra.Command{
	Use:          "umpteenth",
	Short:        "Self-hosted agentic jobs that get cheaper over time",
	SilenceUsage: true,
	// Running without a subcommand starts the server, like `umpteenth serve`
	RunE: serve,
}

// configFile is the --config flag of the commands that read the configuration
var configFile string

func init() {
	serveCmd := &cobra.Command{Use: "serve", Short: "Start the server", RunE: serve}
	healthcheckCmd := &cobra.Command{Use: "healthcheck", Short: "Check that the local server is healthy", RunE: healthcheck}
	for _, cmd := range []*cobra.Command{rootCmd, serveCmd, healthcheckCmd} {
		cmd.Flags().StringVar(&configFile, "config", os.Getenv("CONFIG_FILE"), "YAML config file, CONFIG_FILE or config.yml in the working directory by default")
	}

	rootCmd.AddCommand(
		serveCmd,
		healthcheckCmd,
		&cobra.Command{Use: "openapi", Short: "Print the OpenAPI spec as JSON", RunE: openapi},
		&cobra.Command{Use: "version", Short: "Print the version", Run: func(*cobra.Command, []string) { fmt.Println(common.Version) }},
	)
}

// Execute runs the CLI
func Execute() {
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		os.Exit(1)
	}
}

func serve(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load(configFile)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return bootstrap.Bootstrap(ctx, cfg)
}

func healthcheck(cmd *cobra.Command, _ []string) error {
	// The server's configuration tells which port to probe
	cfg, err := config.Load(configFile)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(cfg.Server.Port)+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

func openapi(cmd *cobra.Command, _ []string) error {
	spec, err := bootstrap.OpenAPI()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(spec)
}
