//go:build unit

package jobs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNextRunAcrossDaylightSavingTime(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	// The repeated hour in autumn fires once, the next occurrence is the following day
	fired := time.Date(2026, 11, 1, 1, 30, 0, 0, ny)
	next, err := nextRun("30 1 * * *", "America/New_York", fired)
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 11, 2, 1, 30, 0, 0, ny), next)
	require.Equal(t, 25*time.Hour, next.Sub(fired))

	// The skipped hour in spring fires right after the jump instead of skipping the day
	next, err = nextRun("30 2 * * *", "America/New_York", time.Date(2026, 3, 7, 12, 0, 0, 0, ny))
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 3, 8, 3, 30, 0, 0, ny), next)

	// An ordinary day is unaffected
	next, err = nextRun("0 9 * * 1-5", "Europe/Zurich", time.Date(2026, 9, 25, 23, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC), next.UTC())
}

func TestValidateScheduleRejectsSurprisingExpressions(t *testing.T) {
	for _, tc := range []struct{ cron, tz string }{
		{"@every 1s", ""},
		{"CRON_TZ=Asia/Tokyo 0 9 * * *", "UTC"},
		{"0 0 30 2 *", "UTC"},
		{"0 9 * * *", "Local"},
		{"0 9 * * *", "Mars/Olympus"},
	} {
		require.Error(t, validateSchedule(&tc.cron, &tc.tz), "%s in %s", tc.cron, tc.tz)
	}
	for _, tc := range []struct{ cron, tz string }{{"*/5 * * * *", "UTC"}, {"@daily", "Europe/Berlin"}, {"0 9 * * 1-5", ""}} {
		require.NoError(t, validateSchedule(&tc.cron, &tc.tz), tc.cron)
	}
}
