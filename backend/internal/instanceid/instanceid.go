// Package instanceid loads the stable ID of this Umpteenth installation, shared by all replicas
package instanceid

import (
	"context"
	"fmt"

	"github.com/stonith404/umpteenth/backend/internal/database"
)

const kvKey = "instance_id"

// Load returns the instance ID, generating and storing it on first start
// Concurrent first starts converge on one value because the insert is a no-op when the key exists
func Load(ctx context.Context, db *database.DB) (string, error) {
	_, err := db.ExecContext(ctx, "INSERT INTO kv (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING", kvKey, database.NewID())
	if err != nil {
		return "", fmt.Errorf("failed to initialize instance ID: %w", err)
	}

	var id string
	err = db.QueryRowContext(ctx, "SELECT value FROM kv WHERE key = $1", kvKey).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("failed to load instance ID: %w", err)
	}
	return id, nil
}
