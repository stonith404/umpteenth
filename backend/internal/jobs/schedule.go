package jobs

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
)

// cronParser accepts standard five-field expressions and descriptors such as @daily
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// nextRun returns the first occurrence of the cron expression after t, evaluated in the job's timezone
// The expression is evaluated on the local wall clock, so an occurrence inside a skipped DST hour fires once right after the jump and a repeated hour never fires twice
func nextRun(expr, timezone string, after time.Time) (time.Time, error) {
	sched, loc, err := parseSchedule(expr, timezone)
	if err != nil {
		return time.Time{}, err
	}

	// UTC has no DST, so it stands in for the local wall clock while the schedule is evaluated
	local := after.In(loc)
	wall := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC)
	for range 8 {
		nextWall := sched.Next(wall)
		if nextWall.IsZero() {
			return time.Time{}, fmt.Errorf("schedule %q never fires", expr)
		}

		next := time.Date(nextWall.Year(), nextWall.Month(), nextWall.Day(), nextWall.Hour(), nextWall.Minute(), 0, 0, loc)

		// A wall-clock time inside a skipped DST hour doesn't exist, so it is read with the offset from before the jump, which lands just after it
		if next.Hour() != nextWall.Hour() || next.Minute() != nextWall.Minute() {
			_, offset := next.Add(-3 * time.Hour).Zone()
			next = nextWall.Add(-time.Duration(offset) * time.Second).In(loc)
		}
		if next.After(after) {
			return next, nil
		}
		wall = nextWall
	}
	return time.Time{}, fmt.Errorf("schedule %q has no occurrence after %s", expr, after.Format(time.RFC3339))
}

func parseSchedule(expr, timezone string) (cron.Schedule, *time.Location, error) {
	loc := time.UTC
	if timezone != "" {
		// Local would depend on the replica's clock settings, so only real IANA names are accepted
		l, err := time.LoadLocation(timezone)
		if err != nil || timezone == "Local" {
			return nil, nil, apperror.InvalidField("timezone", "invalid", "is not a known IANA timezone")
		}
		loc = l
	}

	// The timezone has its own field, and @every would allow schedules down to every second
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=") {
		return nil, nil, apperror.InvalidField("cron", "invalid", "must not set a timezone, use the timezone field instead")
	}
	if strings.HasPrefix(expr, "@every") {
		return nil, nil, apperror.InvalidField("cron", "invalid", "must be a five-field expression or a descriptor such as @daily")
	}
	sched, err := cronParser.Parse(expr)
	if err != nil {
		return nil, nil, apperror.InvalidField("cron", "invalid", "is not a valid cron expression: "+err.Error())
	}
	return sched, loc, nil
}

// validateSchedule checks a cron/timezone pair, allowing an empty cron for jobs without a schedule
func validateSchedule(expr, timezone *string) error {
	if expr == nil || strings.TrimSpace(*expr) == "" {
		return nil
	}
	tz := ""
	if timezone != nil {
		tz = *timezone
	}

	// An expression that parses but never fires, such as February 30, would leave the job silently unscheduled
	_, err := nextRun(*expr, tz, time.Now())
	if err != nil && !apperror.IsCode(err, apperror.CodeValidationFailed) {
		return apperror.InvalidField("cron", "invalid", "never fires")
	}
	return err
}
