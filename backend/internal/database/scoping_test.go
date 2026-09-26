//go:build unit

package database

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// tenantTables are the tables whose rows carry workspace_id
// Child tables such as job_state are reached through their scoped parent and are checked by the services instead
var tenantTables = []string{"providers", "models", "settings", "secrets", "mcp_servers", "jobs", "runs", "api_tokens", "workspace_members", "workspace_invites"}

var queryHeader = regexp.MustCompile(`(?m)^-- name: (\w+)`)

// TestQueriesAreTenantScoped fails when a query touches a tenant-owned table without filtering on workspace_id
// A query that is deliberately global must say so with a "-- unscoped: <reason>" comment
func TestQueriesAreTenantScoped(t *testing.T) {
	files, err := filepath.Glob("../*/queries.sql")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, file := range files {
		content, err := os.ReadFile(file) // #nosec G304 -- test reads the repository's own query files
		require.NoError(t, err)

		// Split the file into its named queries
		locs := queryHeader.FindAllStringSubmatchIndex(string(content), -1)
		for i, loc := range locs {
			end := len(content)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			query := string(content[loc[0]:end])
			name := string(content[loc[2]:loc[3]])

			if strings.Contains(query, "-- unscoped:") || strings.Contains(query, "workspace_id") {
				continue
			}
			for _, table := range tenantTables {
				if regexp.MustCompile(`(?i)\b(FROM|JOIN|INTO|UPDATE)\s+` + table + `\b`).MatchString(query) {
					t.Errorf("%s: query %s touches %s without filtering on workspace_id; add the filter or a '-- unscoped: <reason>' comment", filepath.Base(filepath.Dir(file)), name, table)
				}
			}
		}
	}
}
