package automation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %q: %v", name, err)
	}
	return loc
}

func TestCompileScheduleValidation(t *testing.T) {
	if _, err := CompileSchedule(ScheduleSpec{Cron: "0 3 * * *", TimeZone: "America/New_York"}); err != nil {
		t.Fatalf("valid schedule rejected: %v", err)
	}
	for name, spec := range map[string]ScheduleSpec{
		"missing cron":         {TimeZone: "UTC"},
		"TZ prefix":            {Cron: "TZ=UTC 0 3 * * *", TimeZone: "UTC"},
		"CRON_TZ prefix":       {Cron: "CRON_TZ=UTC 0 3 * * *", TimeZone: "UTC"},
		"invalid cron":         {Cron: "not a cron", TimeZone: "UTC"},
		"never-occurring cron": {Cron: "0 0 30 2 *", TimeZone: "UTC"},
		"missing time zone":    {Cron: "0 3 * * *"},
		"invalid time zone":    {Cron: "0 3 * * *", TimeZone: "Not/AZone"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CompileSchedule(spec); err == nil {
				t.Fatal("accepted invalid schedule")
			}
		})
	}
}

func TestCompileScheduleTZPrefixErrorMentionsTimeZoneField(t *testing.T) {
	_, err := CompileSchedule(ScheduleSpec{Cron: "TZ=UTC 0 3 * * *", TimeZone: "UTC"})
	if err == nil || !strings.Contains(err.Error(), "schedule.time_zone") {
		t.Fatalf("err = %v", err)
	}
}

// TestScheduleSpringForwardSkipsNonexistentInstant observes robfig/cron's actual
// behavior for a daily "30 2 * * *" schedule across the America/New_York
// spring-forward transition on 2026-03-08 (clocks jump 02:00 -> 03:00, so 02:30
// never exists that day). robfig's field-matching search never finds a match on
// 2026-03-08 and rolls straight to 2026-03-09 — the day is skipped entirely
// rather than shifted to a nearby existing time.
func TestScheduleSpringForwardSkipsNonexistentInstant(t *testing.T) {
	loc := mustLocation(t, "America/New_York")
	compiled, err := CompileSchedule(ScheduleSpec{Cron: "30 2 * * *", TimeZone: "America/New_York"})
	if err != nil {
		t.Fatal(err)
	}
	cursor := time.Date(2026, 3, 6, 12, 0, 0, 0, loc)
	now := time.Date(2026, 3, 13, 12, 0, 0, 0, loc)
	instants, ok := compiled.DueInstants(cursor, now, 10)
	if !ok {
		t.Fatal("cap hit unexpectedly")
	}
	wantUTC := []string{
		"2026-03-07T07:30:00Z",
		"2026-03-09T06:30:00Z", // 2026-03-08 skipped: 02:30 does not exist that day
		"2026-03-10T06:30:00Z",
		"2026-03-11T06:30:00Z",
		"2026-03-12T06:30:00Z",
		"2026-03-13T06:30:00Z",
	}
	if len(instants) != len(wantUTC) {
		t.Fatalf("instants=%v, want %d entries", instants, len(wantUTC))
	}
	var prev time.Time
	for i, instant := range instants {
		if got := instant.UTC().Format(time.RFC3339); got != wantUTC[i] {
			t.Fatalf("instant[%d]=%s, want %s", i, got, wantUTC[i])
		}
		if i > 0 && !instant.After(prev) {
			t.Fatalf("instants not strictly increasing at %d: %v", i, instants)
		}
		prev = instant
	}
}

// TestScheduleFallBackVisitsAmbiguousHourTwice observes robfig/cron's actual
// behavior for a daily "30 1 * * *" schedule across the America/New_York
// fall-back transition on 2026-11-01 (clocks are set back 02:00 -> 01:00, so
// 01:30 occurs twice). robfig's search visits the wall-clock instant 01:30 on
// 2026-11-01 twice: once at the pre-transition offset (-04:00) and once at the
// post-transition offset (-05:00), producing two distinct, strictly increasing
// UTC instants for the same local time.
func TestScheduleFallBackVisitsAmbiguousHourTwice(t *testing.T) {
	loc := mustLocation(t, "America/New_York")
	compiled, err := CompileSchedule(ScheduleSpec{Cron: "30 1 * * *", TimeZone: "America/New_York"})
	if err != nil {
		t.Fatal(err)
	}
	cursor := time.Date(2026, 10, 30, 12, 0, 0, 0, loc)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, loc)
	instants, ok := compiled.DueInstants(cursor, now, 10)
	if !ok {
		t.Fatal("cap hit unexpectedly")
	}
	wantUTC := []string{
		"2026-10-31T05:30:00Z",
		"2026-11-01T05:30:00Z", // ambiguous hour, pre-transition offset -04:00
		"2026-11-01T06:30:00Z", // ambiguous hour, post-transition offset -05:00
		"2026-11-02T06:30:00Z",
	}
	if len(instants) != len(wantUTC) {
		t.Fatalf("instants=%v, want %d entries", instants, len(wantUTC))
	}
	var prev time.Time
	for i, instant := range instants {
		if got := instant.UTC().Format(time.RFC3339); got != wantUTC[i] {
			t.Fatalf("instant[%d]=%s, want %s", i, got, wantUTC[i])
		}
		if i > 0 && !instant.After(prev) {
			t.Fatalf("instants not strictly increasing at %d: %v", i, instants)
		}
		prev = instant
	}
	keyOne := ScheduledOccurrenceKey(instants[1])
	keyTwo := ScheduledOccurrenceKey(instants[2])
	if keyOne == keyTwo {
		t.Fatalf("ambiguous fall-back instants collapsed to one key: %s", keyOne)
	}
	if keyOne != "scheduled:2026-11-01T05:30:00Z" || keyTwo != "scheduled:2026-11-01T06:30:00Z" {
		t.Fatalf("keys = %s, %s", keyOne, keyTwo)
	}
}

func TestScheduleUTCSanity(t *testing.T) {
	compiled, err := CompileSchedule(ScheduleSpec{Cron: "0 9 * * *", TimeZone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	cursor := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	instants, ok := compiled.DueInstants(cursor, now, 10)
	if !ok {
		t.Fatal("cap hit unexpectedly")
	}
	want := []string{"2026-06-01T09:00:00Z", "2026-06-02T09:00:00Z", "2026-06-03T09:00:00Z"}
	if len(instants) != len(want) {
		t.Fatalf("instants=%v, want %d entries", instants, len(want))
	}
	for i, instant := range instants {
		if got := instant.UTC().Format(time.RFC3339); got != want[i] {
			t.Fatalf("instant[%d]=%s, want %s", i, got, want[i])
		}
	}
}

// TestScheduleEuropeLondonAcrossDST checks a Europe/London daily "30 1 * * *"
// schedule across the 2026-03-29 spring-forward transition (clocks jump 01:00 ->
// 02:00, so 01:30 does not exist that day): the day is skipped, matching the
// same field-matching behavior observed for America/New_York.
func TestScheduleEuropeLondonAcrossDST(t *testing.T) {
	loc := mustLocation(t, "Europe/London")
	compiled, err := CompileSchedule(ScheduleSpec{Cron: "30 1 * * *", TimeZone: "Europe/London"})
	if err != nil {
		t.Fatal(err)
	}
	cursor := time.Date(2026, 3, 27, 12, 0, 0, 0, loc)
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, loc)
	instants, ok := compiled.DueInstants(cursor, now, 10)
	if !ok {
		t.Fatal("cap hit unexpectedly")
	}
	wantUTC := []string{
		"2026-03-28T01:30:00Z",
		"2026-03-30T00:30:00Z", // 2026-03-29 skipped: 01:30 does not exist that day
		"2026-03-31T00:30:00Z",
		"2026-04-01T00:30:00Z",
		"2026-04-02T00:30:00Z",
	}
	if len(instants) != len(wantUTC) {
		t.Fatalf("instants=%v, want %d entries", instants, len(wantUTC))
	}
	for i, instant := range instants {
		if got := instant.UTC().Format(time.RFC3339); got != wantUTC[i] {
			t.Fatalf("instant[%d]=%s, want %s", i, got, wantUTC[i])
		}
	}
}

func TestDueInstantsCursorEqualsNowReturnsNone(t *testing.T) {
	compiled, err := CompileSchedule(ScheduleSpec{Cron: "0 9 * * *", TimeZone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	instants, ok := compiled.DueInstants(now, now, 10)
	if !ok || len(instants) != 0 {
		t.Fatalf("instants=%v ok=%v, want none", instants, ok)
	}
}

func TestDueInstantsCapHitReturnsNotOK(t *testing.T) {
	compiled, err := CompileSchedule(ScheduleSpec{Cron: "0 9 * * *", TimeZone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	cursor := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	instants, ok := compiled.DueInstants(cursor, now, 5)
	if ok {
		t.Fatal("expected cap to be hit")
	}
	if len(instants) != 5 {
		t.Fatalf("instants=%v, want 5", instants)
	}
}

func TestDueInstantsMidWindowCursor(t *testing.T) {
	compiled, err := CompileSchedule(ScheduleSpec{Cron: "0 9 * * *", TimeZone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	cursor := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC) // strictly after
	now := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	instants, ok := compiled.DueInstants(cursor, now, 10)
	if !ok {
		t.Fatal("cap hit unexpectedly")
	}
	want := []string{"2026-06-03T09:00:00Z", "2026-06-04T09:00:00Z"}
	if len(instants) != len(want) {
		t.Fatalf("instants=%v, want %v", instants, want)
	}
	for i, instant := range instants {
		if got := instant.UTC().Format(time.RFC3339); got != want[i] {
			t.Fatalf("instant[%d]=%s, want %s", i, got, want[i])
		}
	}
}

func TestParseScheduledInput(t *testing.T) {
	valid := json.RawMessage(`{"provider":"schedule","intended_at":"2026-06-01T09:00:00Z","observed_at":"2026-06-01T09:00:02Z"}`)
	input, err := ParseScheduledInput(valid)
	if err != nil {
		t.Fatal(err)
	}
	if input.Provider != "schedule" || input.IntendedAt != "2026-06-01T09:00:00Z" || input.ObservedAt != "2026-06-01T09:00:02Z" {
		t.Fatalf("input = %#v", input)
	}
	for name, raw := range map[string]string{
		"unknown field":  `{"provider":"schedule","intended_at":"2026-06-01T09:00:00Z","observed_at":"2026-06-01T09:00:02Z","extra":true}`,
		"wrong provider": `{"provider":"github","intended_at":"2026-06-01T09:00:00Z","observed_at":"2026-06-01T09:00:02Z"}`,
		"trailing json":  `{"provider":"schedule","intended_at":"2026-06-01T09:00:00Z","observed_at":"2026-06-01T09:00:02Z"}{}`,
		"bad timestamp":  `{"provider":"schedule","intended_at":"not-a-time","observed_at":"2026-06-01T09:00:02Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseScheduledInput(json.RawMessage(raw)); err == nil {
				t.Fatal("accepted invalid scheduled input")
			}
		})
	}
}

func TestNewScheduledInputNormalizesToUTC(t *testing.T) {
	loc := mustLocation(t, "America/New_York")
	intended := time.Date(2026, 6, 1, 5, 0, 0, 0, loc)
	observed := time.Date(2026, 6, 1, 5, 0, 2, 0, loc)
	input := NewScheduledInput(intended, observed)
	if input.Provider != "schedule" || input.IntendedAt != "2026-06-01T09:00:00Z" || input.ObservedAt != "2026-06-01T09:00:02Z" {
		t.Fatalf("input = %#v", input)
	}
}
