package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ── Cron expression parser ────────────────────────────────────────────────────
//
// Supports standard 5-field cron: minute hour dom month dow
// Each field: * | */N | N | N,M,P
//
// Examples:
//   "* * * * *"      every minute
//   "0 9 * * 1"      every Monday at 09:00
//   "*/15 * * * *"   every 15 minutes
//   "0 0 1 * *"      first of every month at midnight

// CronSpec is a parsed 5-field cron expression.
type CronSpec struct {
	Minute     cronField // 0–59
	Hour       cronField // 0–23
	DayOfMonth cronField // 1–31
	Month      cronField // 1–12
	DayOfWeek  cronField // 0–7 (0 and 7 both = Sunday)
}

type cronField struct {
	wildcard bool
	values   []int // explicit values (empty when wildcard=true and step=0)
	step     int   // for */N; 0 = no step
}

// ParseCron parses a 5-field cron expression.
func ParseCron(expr string) (CronSpec, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return CronSpec{}, fmt.Errorf("cron: expected 5 fields, got %d", len(fields))
	}
	limits := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	parsed := [5]cronField{}
	for i, f := range fields {
		cf, err := parseCronField(f, limits[i][0], limits[i][1])
		if err != nil {
			return CronSpec{}, fmt.Errorf("cron field %d: %w", i+1, err)
		}
		parsed[i] = cf
	}
	return CronSpec{
		Minute:     parsed[0],
		Hour:       parsed[1],
		DayOfMonth: parsed[2],
		Month:      parsed[3],
		DayOfWeek:  parsed[4],
	}, nil
}

func parseCronField(s string, lo, hi int) (cronField, error) {
	if s == "*" {
		return cronField{wildcard: true}, nil
	}
	if strings.HasPrefix(s, "*/") {
		n, err := strconv.Atoi(s[2:])
		if err != nil || n < 1 {
			return cronField{}, fmt.Errorf("invalid step %q", s)
		}
		return cronField{wildcard: true, step: n}, nil
	}
	// Comma-separated values.
	var vals []int
	for _, part := range strings.Split(s, ",") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return cronField{}, fmt.Errorf("%q is not a number", part)
		}
		if n < lo || n > hi {
			return cronField{}, fmt.Errorf("%d out of range [%d,%d]", n, lo, hi)
		}
		vals = append(vals, n)
	}
	return cronField{values: vals}, nil
}

// Matches reports whether the spec matches the given time (truncated to minute).
func (cs CronSpec) Matches(t time.Time) bool {
	dow := int(t.Weekday()) // 0=Sunday
	return cs.Minute.matches(t.Minute()) &&
		cs.Hour.matches(t.Hour()) &&
		cs.DayOfMonth.matches(t.Day()) &&
		cs.Month.matches(int(t.Month())) &&
		(cs.DayOfWeek.matches(dow) || (dow == 0 && cs.DayOfWeek.matches(7))) // 0 and 7 = Sunday
}

func (f cronField) matches(v int) bool {
	if f.wildcard {
		if f.step > 0 {
			return v%f.step == 0
		}
		return true
	}
	for _, x := range f.values {
		if x == v {
			return true
		}
	}
	return false
}

// ── Scheduler ─────────────────────────────────────────────────────────────────

// CronScheduler fires registered functions on their configured schedules.
// It ticks once per minute on the minute boundary.
type CronScheduler struct {
	entries []cronEntry
	stop    chan struct{}
}

type cronEntry struct {
	spec      CronSpec
	agentName string
	run       func() // called in its own goroutine
}

// NewCronScheduler creates a stopped scheduler.
func NewCronScheduler() *CronScheduler {
	return &CronScheduler{stop: make(chan struct{})}
}

// Add registers a function to be called whenever spec matches the current time.
func (cs *CronScheduler) Add(spec CronSpec, agentName string, fn func()) {
	cs.entries = append(cs.entries, cronEntry{spec, agentName, fn})
}

// Start launches the background ticker goroutine.
func (cs *CronScheduler) Start() {
	go func() {
		for {
			// Sleep until the start of the next minute.
			now := time.Now()
			next := now.Truncate(time.Minute).Add(time.Minute)
			timer := time.NewTimer(time.Until(next))
			select {
			case <-cs.stop:
				timer.Stop()
				return
			case t := <-timer.C:
				for _, e := range cs.entries {
					if e.spec.Matches(t) {
						go e.run()
					}
				}
			}
		}
	}()
}

// Stop shuts down the scheduler (in-flight runs are not interrupted).
func (cs *CronScheduler) Stop() {
	select {
	case <-cs.stop: // already closed
	default:
		close(cs.stop)
	}
}
