package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDashboardAndTaskPersistence(t *testing.T) {
	// `just` loads .env before running tests. Keep this assertion independent of
	// private calendar and weather configuration from the developer's machine.
	t.Setenv("GOOGLE_CALENDAR_ICAL_URL", "")
	t.Setenv("PLAY_OUTSIDE_OPEN_WEATHER_API_KEY", "")
	path := filepath.Join(t.TempDir(), "tasks.json")
	a, err := newApp(path)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Date(2026, 10, 20, 15, 30, 0, 0, time.UTC) }
	h := a.routes()
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if r.Code != 200 || !strings.Contains(r.Body.String(), "DEMO CALENDAR") || !strings.Contains(r.Body.String(), "Family tasks") {
		t.Fatalf("bad dashboard: status %d", r.Code)
	}
	values := url.Values{"action": {"add"}, "title": {"Buy milk"}, "csrf": {a.csrf}}
	req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != 303 {
		t.Fatalf("add status %d body %s", r.Code, r.Body.String())
	}
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	tasks := s.list()
	if len(tasks) != 4 || tasks[3].Title != "Buy milk" {
		t.Fatalf("task persistence: %+v", tasks)
	}
	id := tasks[3].ID
	for _, action := range []string{"toggle", "delete"} {
		values := url.Values{"action": {action}, "id": {id}, "csrf": {a.csrf}}
		req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		if r.Code != 303 {
			t.Fatalf("%s status %d", action, r.Code)
		}
	}
	if len(a.store.list()) != 3 {
		t.Fatal("delete did not persist")
	}
}

func TestAllDayDaysCountsCalendarDatesAcrossDST(t *testing.T) {
	loc := chicago(t)
	start := time.Date(2026, time.March, 7, 0, 0, 0, 0, loc)
	end := time.Date(2026, time.March, 9, 0, 0, 0, 0, loc)
	if got := allDayDays(start, end, loc); got != 2 {
		t.Fatalf("allDayDays(%v, %v) = %d, want 2", start, end, got)
	}
	if got := allDayDays(start, start.AddDate(0, 0, 1), loc); got != 1 {
		t.Fatalf("single-day all-day event spans %d days, want 1", got)
	}
}

func TestNextEventSkipsEventsAlreadyInProgress(t *testing.T) {
	now := time.Date(2026, time.October, 21, 19, 7, 0, 0, time.UTC)
	events := []Event{
		{Title: "No school", Start: now.Add(-12 * time.Hour).Format(time.RFC3339), End: now.Add(12 * time.Hour).Format(time.RFC3339), AllDay: true},
		{Title: "Ayla Vv", Start: now.Add(2 * time.Hour).Format(time.RFC3339), End: now.Add(4 * time.Hour).Format(time.RFC3339)},
	}
	next, remaining := nextEvent(events, now)
	if next == nil || next.Title != "Ayla Vv" || next.Countdown != "in 2h" {
		t.Fatalf("next event = %+v, want Ayla Vv in 2h", next)
	}
	if len(remaining) != 1 || remaining[0].Title != "No school" {
		t.Fatalf("remaining events = %+v, want in-progress all-day event retained", remaining)
	}
}

func TestRejectBadTaskMutations(t *testing.T) {
	a, err := newApp(filepath.Join(t.TempDir(), "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		label  string
		csrf   string
		action string
		title  string
		want   int
	}{
		{"missing csrf", "", "add", "test", 403},
		{"empty title", a.csrf, "add", "", 400},
		{"long title", a.csrf, "add", strings.Repeat("x", 121), 400},
		{"invalid action", a.csrf, "erase", "test", 400},
	} {
		t.Run(tc.label, func(t *testing.T) {
			values := url.Values{"csrf": {tc.csrf}, "action": {tc.action}, "title": {tc.title}}
			req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(values.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r := httptest.NewRecorder()
			a.routes().ServeHTTP(r, req)
			if r.Code != tc.want {
				t.Errorf("got %d want %d", r.Code, tc.want)
			}
		})
	}
}

func TestPhotoFrameAssetsAndIconNames(t *testing.T) {
	a, err := newApp(filepath.Join(t.TempDir(), "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := a.routes()
	for _, tc := range []struct{ path, contentType, contains string }{
		{"/app.js", "application/javascript", "120000"},
		{"/style.css", "text/css", "kanagawa-wave"},
		{"/dashboard", "text/html", "feature-window"},
	} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if r.Code != http.StatusOK || !strings.HasPrefix(r.Header().Get("Content-Type"), tc.contentType) || !strings.Contains(r.Body.String(), tc.contains) {
			t.Errorf("asset %s: code=%d type=%s present=%t", tc.path, r.Code, r.Header().Get("Content-Type"), strings.Contains(r.Body.String(), tc.contains))
		}
	}
	landing := httptest.NewRecorder()
	h.ServeHTTP(landing, httptest.NewRequest(http.MethodGet, "/", nil))
	if landing.Code != http.StatusOK || !strings.Contains(landing.Body.String(), "Open Family HQ") || strings.Contains(landing.Body.String(), "Family tasks") {
		t.Errorf("public landing page leaked dashboard data or failed: %d", landing.Code)
	}
	og := httptest.NewRecorder()
	h.ServeHTTP(og, httptest.NewRequest(http.MethodGet, "/og", nil))
	if og.Code != http.StatusOK || !strings.HasPrefix(og.Header().Get("Content-Type"), "image/png") || !strings.HasPrefix(og.Body.String(), "\x89PNG\r\n\x1a\n") {
		t.Errorf("Open Graph image failed: code=%d type=%s", og.Code, og.Header().Get("Content-Type"))
	}
	for kind, want := range map[string]string{
		"Clear": "sun", "Clouds": "cloud", "Rain": "rain", "Drizzle": "rain", "Thunderstorm": "storm", "Snow": "snow", "Mist": "haze",
	} {
		if icon := weatherIcon(kind); icon != want {
			t.Errorf("%s icon got %q want %q", kind, icon, want)
		}
	}
}
