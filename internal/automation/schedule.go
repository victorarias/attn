package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// scheduleValidationSample anchors the "does this cron expression ever fire"
// check to a fixed instant, so validation is deterministic and independent of
// when it runs.
var scheduleValidationSample = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// CompiledSchedule is a parsed, validated ScheduleSpec ready to compute due
// instants.
type CompiledSchedule struct {
	cron     cron.Schedule
	location *time.Location
}

// CompileSchedule parses and validates a ScheduleSpec. schedule.cron must parse
// as a standard cron expression (no embedded TZ=/CRON_TZ= prefix, which would
// silently compete with schedule.time_zone) and have a real next occurrence;
// schedule.time_zone must be a loadable IANA name.
func CompileSchedule(spec ScheduleSpec) (CompiledSchedule, error) {
	cronExpr := strings.TrimSpace(spec.Cron)
	if cronExpr == "" {
		return CompiledSchedule{}, errors.New("schedule.cron is required")
	}
	if strings.HasPrefix(cronExpr, "TZ=") || strings.HasPrefix(cronExpr, "CRON_TZ=") {
		return CompiledSchedule{}, errors.New("schedule.cron must not embed a TZ=/CRON_TZ= prefix; set schedule.time_zone instead")
	}
	sched, err := cron.ParseStandard(cronExpr)
	if err != nil {
		return CompiledSchedule{}, fmt.Errorf("schedule.cron must be a valid cron expression: %w", err)
	}
	tz := strings.TrimSpace(spec.TimeZone)
	if tz == "" {
		return CompiledSchedule{}, errors.New("schedule.time_zone is required")
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return CompiledSchedule{}, fmt.Errorf("schedule.time_zone must be an IANA timezone: %w", err)
	}
	if sched.Next(scheduleValidationSample).IsZero() {
		return CompiledSchedule{}, fmt.Errorf("schedule.cron %q describes a time that never occurs", cronExpr)
	}
	return CompiledSchedule{cron: sched, location: loc}, nil
}

// DueInstants returns the schedule's intended instants strictly after cursor and
// at-or-before now, in order, iterating cron.Next at most limit times. ok is
// false when the cap was hit before reaching now — the caller's replay-storm
// guard, so a large backlog (e.g. a very old cursor) cannot fire an unbounded
// burst of catch-up occurrences in a single pass.
func (s CompiledSchedule) DueInstants(cursor, now time.Time, limit int) (instants []time.Time, ok bool) {
	at := cursor.In(s.location)
	for i := 0; i < limit; i++ {
		next := s.cron.Next(at)
		if next.IsZero() || next.After(now) {
			return instants, true
		}
		instants = append(instants, next)
		at = next
	}
	return instants, false
}

// ScheduledOccurrenceKey derives a scheduled occurrence's idempotency key from
// its intended instant, normalized to UTC so the key is independent of the
// schedule's configured time zone (and so an ambiguous local wall-clock time,
// e.g. during a fall-back DST transition, still maps to a distinct key per
// underlying instant).
func ScheduledOccurrenceKey(intended time.Time) string {
	return "scheduled:" + intended.UTC().Format(time.RFC3339)
}

// ScheduledInput is the structured occurrence input recorded for a scheduled
// automation run.
type ScheduledInput struct {
	Provider   string `json:"provider"`
	IntendedAt string `json:"intended_at"`
	ObservedAt string `json:"observed_at"`
}

func NewScheduledInput(intended, observed time.Time) ScheduledInput {
	return ScheduledInput{
		Provider:   "schedule",
		IntendedAt: intended.UTC().Format(time.RFC3339),
		ObservedAt: observed.UTC().Format(time.RFC3339),
	}
}

func ParseScheduledInput(raw json.RawMessage) (ScheduledInput, error) {
	var input ScheduledInput
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return input, fmt.Errorf("parse scheduled input: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return input, errors.New("parse scheduled input: expected one JSON object")
	}
	if input.Provider != "schedule" {
		return input, errors.New("scheduled input provider must be schedule")
	}
	if _, err := time.Parse(time.RFC3339, input.IntendedAt); err != nil {
		return input, fmt.Errorf("scheduled input intended_at: %w", err)
	}
	if _, err := time.Parse(time.RFC3339, input.ObservedAt); err != nil {
		return input, fmt.Errorf("scheduled input observed_at: %w", err)
	}
	return input, nil
}
