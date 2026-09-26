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

func TestNextRunSkipsTheSecondPassOfARepeatedHour(t *testing.T) {
	// Clocks fall back from 02:00 to 01:00 at 06:00Z in New York and at 09:00Z in Los Angeles
	nyEnd := time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC)
	laEnd := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		cron, tz    string
		after, want time.Time
	}{
		// A late alarm delivered in the second pass, such as after a restart, arms the first occurrence after the repeated hour
		{"* * * * *", "America/New_York", time.Date(2026, 11, 1, 6, 0, 30, 0, time.UTC), nyEnd},
		{"* * * * *", "America/New_York", time.Date(2026, 11, 1, 6, 5, 0, 0, time.UTC), nyEnd},
		{"*/5 * * * *", "America/New_York", time.Date(2026, 11, 1, 6, 3, 0, 0, time.UTC), nyEnd},
		{"* * * * *", "America/Los_Angeles", time.Date(2026, 11, 1, 9, 10, 0, 0, time.UTC), laEnd},
		{"*/5 * * * *", "America/Los_Angeles", time.Date(2026, 11, 1, 9, 0, 30, 0, time.UTC), laEnd},

		// The first pass fires as usual, and its last occurrence is followed by the first one after the repeated hour
		{"* * * * *", "America/New_York", time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC), time.Date(2026, 11, 1, 5, 31, 0, 0, time.UTC)},
		{"* * * * *", "America/New_York", time.Date(2026, 11, 1, 5, 59, 0, 0, time.UTC), nyEnd},
		{"*/5 * * * *", "America/Los_Angeles", time.Date(2026, 11, 1, 8, 55, 0, 0, time.UTC), laEnd},
	} {
		next, err := nextRun(tc.cron, tc.tz, tc.after)
		require.NoError(t, err, "%s in %s after %s", tc.cron, tc.tz, tc.after)
		require.Equal(t, tc.want, next.UTC(), "%s in %s after %s", tc.cron, tc.tz, tc.after)
	}
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
