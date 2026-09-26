//go:build unit

package testutil_test

import (
	"fmt"
	"testing"

	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// Parallel tests, like the parallel packages of go test ./..., each need their own database
func TestParallelTestsGetTheirOwnDatabase(t *testing.T) {
	for i := range 40 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			testutil.NewDatabaseForTest(t)
		})
	}
}
