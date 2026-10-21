package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func chicago(t *testing.T) *time.Location {
	t.Helper()
	l, e := time.LoadLocation("America/Chicago")
	if e != nil {
		t.Fatal(e)
	}
	return l
}
func dateIn(t *testing.T, s string) time.Time {
	t.Helper()
	d, e := time.ParseInLocation("2006-01-02 15:04", s, chicago(t))
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestCalendarRecurringAllDayExclusionAndOverride(t *testing.T) {
	calendar := `BEGIN:VCALENDAR
VERSION:2.0
BEGIN:VEVENT
UID:weekly
DTSTART;TZID=America/Chicago:20261014T160000
DTEND;TZID=America/Chicago:20261014T170000
RRULE:FREQ=WEEKLY;BYDAY=WE;COUNT=5
EXDATE;TZID=America/Chicago:20261028T160000
SUMMARY:Music\, Choir
END:VEVENT
BEGIN:VEVENT
UID:weekly
RECURRENCE-ID;TZID=America/Chicago:20261021T160000
DTSTART;TZID=America/Chicago:20261021T180000
DTEND;TZID=America/Chicago:20261021T190000
SUMMARY:Changed rehearsal
END:VEVENT
BEGIN:VEVENT
UID:all-day
DTSTART;VALUE=DATE:20261022
DTEND;VALUE=DATE:20261023
SUMMARY:Picture day
END:VEVENT
BEGIN:VEVENT
UID:deleted
DTSTART;TZID=America/Chicago:20261021T190000
DTEND;TZID=America/Chicago:20261021T200000
STATUS:CANCELLED
SUMMARY:Do not show
END:VEVENT
END:VCALENDAR`
	got, err := parseICalendar([]byte(strings.ReplaceAll(calendar, "\n", "\r\n")), dateIn(t, "2026-10-21 10:00"), chicago(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].title != "Changed rehearsal" || got[0].start.In(chicago(t)).Hour() != 18 {
		t.Fatalf("override: %+v", got[0])
	}
	if got[1].title != "Picture day" || !got[1].allDay {
		t.Fatalf("all-day: %+v", got[1])
	}
}
func TestCalendarWeeklyRecurrenceAndTimezoneAcrossDST(t *testing.T) {
	raw := `BEGIN:VCALENDAR
BEGIN:VEVENT
UID:school
DTSTART;TZID=America/Chicago:20261020T083000
DTEND;TZID=America/Chicago:20261020T093000
RRULE:FREQ=DAILY;COUNT=20;BYDAY=MO,TU,WE,TH,FR
SUMMARY:School\nMorning
END:VEVENT
END:VCALENDAR`
	got, e := parseICalendar([]byte(raw), dateIn(t, "2026-10-24 12:00"), chicago(t))
	if e != nil {
		t.Fatal(e)
	}
	// Weekend excluded; Mon through Fri in the selected week.
	if len(got) != 5 {
		t.Fatalf("expected 5 occurrences got %d: %+v", len(got), got)
	}
	for _, v := range got {
		if v.start.In(chicago(t)).Hour() != 8 || v.start.In(chicago(t)).Minute() != 30 {
			t.Fatalf("wrong time %+v", v)
		}
	}
}
func TestRecurrenceUnsupportedRuleFailsExplicitly(t *testing.T) {
	ical := `BEGIN:VCALENDAR
BEGIN:VEVENT
UID:unsupported
DTSTART:20261021T100000Z
DTEND:20261021T110000Z
RRULE:FREQ=YEARLY;BYSETPOS=1
SUMMARY:Unsupported
END:VEVENT
END:VCALENDAR`
	_, err := parseICalendar([]byte(ical), dateIn(t, "2026-10-21 10:00"), chicago(t))
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported error, got %v", err)
	}
}
func TestCalendarCacheErrorAndNoSecretLeak(t *testing.T) {
	loc := chicago(t)
	secret := "https://calendar.google.com/calendar/ical/test%40example.com/private-secrettoken123/basic.ics"
	feed, err := newCalendarFeed(secret, loc)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	feed.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls > 1 {
			return nil, fmt.Errorf("error fetching %s", r.URL)
		}
		body := `BEGIN:VCALENDAR
BEGIN:VEVENT
UID:one
DTSTART;TZID=America/Chicago:20261021T120000
DTEND;TZID=America/Chicago:20261021T130000
SUMMARY:Real private event
END:VEVENT
END:VCALENDAR`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
	now := dateIn(t, "2026-10-21 10:00")
	results, _, problem := feed.snapshot(now)
	if len(results) != 1 || problem != "" {
		t.Fatalf("first: %+v %q", results, problem)
	}
	feed.snapshot(now.Add(10 * time.Minute))
	if calls != 1 {
		t.Fatalf("expected no refresh, calls=%d", calls)
	}
	results, _, problem = feed.snapshot(now.Add(16 * time.Minute))
	if len(results) != 1 || problem == "" || strings.Contains(problem, "secrettoken123") {
		t.Fatalf("failed should preserve cache and redact URL: %+v, %q", results, problem)
	}
	feed.snapshot(now.Add(16*time.Minute + time.Second))
	if calls != 2 {
		t.Fatal("error cooldown missing")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
func TestCalendarDemoVsRealMode(t *testing.T) {
	t.Setenv("GOOGLE_CALENDAR_ICAL_URL", "")
	a, e := newApp(filepath.Join(t.TempDir(), "tasks.json"))
	if e != nil {
		t.Fatal(e)
	}
	a.now = func() time.Time { return dateIn(t, "2026-10-21 10:00") }
	page := a.page(a.now())
	if page.CalendarBadge != "DEMO CALENDAR" {
		t.Fatalf("demo: %+v", page)
	}
	t.Setenv("GOOGLE_CALENDAR_ICAL_URL", "https://calendar.google.com/calendar/ical/family/private-token/basic.ics")
	b, e := newApp(filepath.Join(t.TempDir(), "tasks.json"))
	if e != nil {
		t.Fatal(e)
	}
	b.now = a.now
	b.calendar.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("BEGIN:VCALENDAR\nEND:VCALENDAR"))}, nil
	})}
	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	b.routes().ServeHTTP(w, req)
	if w.Code != 200 || strings.Contains(w.Body.String(), "DEMO CALENDAR") || !strings.Contains(w.Body.String(), "GOOGLE CALENDAR") || strings.Contains(w.Body.String(), "private-token") {
		t.Fatalf("bad live page %d %s", w.Code, w.Body.String())
	}
}
func TestBadCalendarURLRejectedWithoutPrintingSecret(t *testing.T) {
	for _, s := range []string{"http://calendar.google.com/calendar/ical/test/private-token/basic.ics", "https://evil.example.com/calendar/ical/test/private-token/basic.ics", "https://calendar.google.com.evil.com/calendar/ical/test/private-token/basic.ics"} {
		if _, err := newCalendarFeed(s, chicago(t)); err == nil || strings.Contains(err.Error(), "private-token") {
			t.Fatalf("invalid URL accepted or leaked: %q, %v", s, err)
		}
	}
}
func TestPersistedTasksUnaffected(t *testing.T) {
	t.Setenv("GOOGLE_CALENDAR_ICAL_URL", "")
	a, e := newApp(filepath.Join(t.TempDir(), "tasks.json"))
	if e != nil {
		t.Fatal(e)
	}
	if err := a.store.add("Remember appointment"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.store.path); err != nil {
		t.Fatal(err)
	}
}

func TestCalendarExDateAndDSTAllDay(t *testing.T) {
	raw := `BEGIN:VCALENDAR
BEGIN:VEVENT
UID:deleted-week
DTSTART;VALUE=DATE:20261024
DTEND;VALUE=DATE:20261026
RRULE:FREQ=WEEKLY;COUNT=3
EXDATE;VALUE=DATE:20261031
SUMMARY:Two-day event
END:VEVENT
END:VCALENDAR`
	got, err := parseICalendar([]byte(raw), dateIn(t, "2026-10-31 12:00"), chicago(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("excluded instance should not be shown: %+v", got)
	}
	got, err = parseICalendar([]byte(raw), dateIn(t, "2026-11-07 12:00"), chicago(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].allDay || got[0].end.In(chicago(t)).Hour() != 0 {
		t.Fatalf("all-day instance across DST must end at midnight: %+v", got)
	}
}
