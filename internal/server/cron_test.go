package server

import (
	"testing"
	"time"
)

// ── ParseCron ─────────────────────────────────────────────────────────────────

func TestParseCronEveryMinute(t *testing.T) {
	spec, err := ParseCron("* * * * *")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	// Should match any time.
	if !spec.Matches(time.Now()) {
		t.Error("* * * * * should match every minute")
	}
}

func TestParseCronStep(t *testing.T) {
	spec, err := ParseCron("*/15 * * * *")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	match := func(min int) bool {
		t := time.Date(2024, 1, 1, 12, min, 0, 0, time.UTC)
		return spec.Matches(t)
	}
	for _, m := range []int{0, 15, 30, 45} {
		if !match(m) {
			t.Errorf("*/15 should match minute %d", m)
		}
	}
	for _, m := range []int{1, 14, 16, 29, 31} {
		if match(m) {
			t.Errorf("*/15 should NOT match minute %d", m)
		}
	}
}

func TestParseCronSpecificValue(t *testing.T) {
	spec, err := ParseCron("0 9 * * 1")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	// 2024-01-08 is a Monday.
	monday9am := time.Date(2024, 1, 8, 9, 0, 0, 0, time.UTC)
	if !spec.Matches(monday9am) {
		t.Error("0 9 * * 1 should match Monday 09:00")
	}
	// Tuesday same time.
	tuesday9am := time.Date(2024, 1, 9, 9, 0, 0, 0, time.UTC)
	if spec.Matches(tuesday9am) {
		t.Error("0 9 * * 1 should NOT match Tuesday 09:00")
	}
	// Monday at wrong hour.
	monday10am := time.Date(2024, 1, 8, 10, 0, 0, 0, time.UTC)
	if spec.Matches(monday10am) {
		t.Error("0 9 * * 1 should NOT match Monday 10:00")
	}
}

func TestParseCronCommaValues(t *testing.T) {
	spec, err := ParseCron("0 9,17 * * *")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	at9 := time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC)
	at17 := time.Date(2024, 1, 1, 17, 0, 0, 0, time.UTC)
	at12 := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	if !spec.Matches(at9) {
		t.Error("should match hour 9")
	}
	if !spec.Matches(at17) {
		t.Error("should match hour 17")
	}
	if spec.Matches(at12) {
		t.Error("should NOT match hour 12")
	}
}

func TestParseCronFirstOfMonth(t *testing.T) {
	spec, err := ParseCron("0 0 1 * *")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	midnight1st := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	midnight2nd := time.Date(2024, 3, 2, 0, 0, 0, 0, time.UTC)

	if !spec.Matches(midnight1st) {
		t.Error("should match 1st of month at midnight")
	}
	if spec.Matches(midnight2nd) {
		t.Error("should NOT match 2nd of month")
	}
}

func TestParseCronSundayBothValues(t *testing.T) {
	// dow 0 and 7 should both mean Sunday.
	for _, expr := range []string{"0 0 * * 0", "0 0 * * 7"} {
		spec, err := ParseCron(expr)
		if err != nil {
			t.Fatalf("ParseCron(%q): %v", expr, err)
		}
		// 2024-01-07 is a Sunday.
		sunday := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)
		if !spec.Matches(sunday) {
			t.Errorf("%q should match Sunday", expr)
		}
	}
}

// ── ParseCron errors ──────────────────────────────────────────────────────────

func TestParseCronWrongFieldCount(t *testing.T) {
	for _, bad := range []string{"* * * *", "* * * * * *", ""} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("ParseCron(%q) should return error", bad)
		}
	}
}

func TestParseCronOutOfRange(t *testing.T) {
	for _, bad := range []string{
		"60 * * * *",  // minute > 59
		"* 24 * * *",  // hour > 23
		"* * 32 * *",  // dom > 31
		"* * * 13 *",  // month > 12
		"* * * * 8",   // dow > 7
	} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("ParseCron(%q) should return error for out-of-range value", bad)
		}
	}
}

func TestParseCronInvalidStep(t *testing.T) {
	for _, bad := range []string{"*/0 * * * *", "*/abc * * * *"} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("ParseCron(%q) should return error", bad)
		}
	}
}

func TestParseCronNonNumeric(t *testing.T) {
	if _, err := ParseCron("x * * * *"); err == nil {
		t.Error("ParseCron with non-numeric field should error")
	}
}

// ── CronScheduler ─────────────────────────────────────────────────────────────

func TestCronSchedulerStop(t *testing.T) {
	sched := NewCronScheduler()
	sched.Start()
	sched.Stop()
	// Double stop should not panic.
	sched.Stop()
}

func TestCronSchedulerNoFire(t *testing.T) {
	// A spec that never matches (minute 61 doesn't exist, use a very specific time).
	sched := NewCronScheduler()
	fired := false
	spec, _ := ParseCron("59 23 31 12 5") // only fires Dec 31 at 23:59 on a Friday
	sched.Add(spec, "TestAgent", func() { fired = true })
	// Don't start — just verify Add works without panic.
	_ = fired
}
