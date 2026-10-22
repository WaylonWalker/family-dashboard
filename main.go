package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"
	"unicode"

	"github.com/gorilla/websocket"
)

//go:embed web/*
var webFS embed.FS

type Task struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

type taskStore struct {
	mu    sync.Mutex
	path  string
	tasks []Task
}

func openStore(path string) (*taskStore, error) {
	s := &taskStore{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.tasks = []Task{{ID: newID(), Title: "Set the table"}, {ID: newID(), Title: "Take out recycling"}, {ID: newID(), Title: "Plan the weekend"}}
		return s, s.save()
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.tasks); err != nil {
		return nil, fmt.Errorf("decode tasks: %w", err)
	}
	if s.tasks == nil {
		s.tasks = []Task{}
	}
	return s, nil
}

func (s *taskStore) list() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Task(nil), s.tasks...)
}

func (s *taskStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.tasks, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tasks-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

func (s *taskStore) add(title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tasks) >= 100 {
		return errors.New("task limit reached")
	}
	s.tasks = append(s.tasks, Task{ID: newID(), Title: title})
	return s.save()
}

func (s *taskStore) change(id, action string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, task := range s.tasks {
		if task.ID != id {
			continue
		}
		switch action {
		case "toggle":
			s.tasks[i].Done = !task.Done
		case "delete":
			s.tasks = append(s.tasks[:i], s.tasks[i+1:]...)
		default:
			return errors.New("invalid action")
		}
		return s.save()
	}
	return errors.New("task not found")
}

func newID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf[:])
}

type Event struct {
	Date             string
	Weekday          string
	Time             string
	Start            string
	End              string
	Relative         string
	AllDay           bool
	SpanDays         int
	Status           string
	Countdown        string
	LeaveLabel       string
	LeaveAt          string
	LeaveCountdown   string
	LeaveForPickup   bool
	TravelLabel      string
	TravelMinutes    int
	TravelDecision   string
	ReviewKey        string
	WeatherAt        string
	WeatherIcon      string
	WeatherTemp      string
	WeatherCondition string
	WeatherWear      string
	Title            string
	Category         string
}

type SchoolCard struct {
	Name, LeaveAt, LateAt, HomeAt, LeaveClock, HomeClock     string
	LeaveCountdown, LateCountdown, HomeCountdown             string
	Wear, RainNote, PickupAlertAt, PickupAt, PickupCountdown string
	PickupAlert                                              bool
}

type travelRule struct {
	Aliases       []string `json:"aliases"`
	Location      string   `json:"location,omitempty"`
	Minutes       int      `json:"minutes,omitempty"`
	Cutover       string   `json:"cutover,omitempty"`
	BeforeMinutes int      `json:"before_minutes,omitempty"`
	AfterMinutes  int      `json:"after_minutes,omitempty"`
}

func parseTravelRules(raw string) ([]travelRule, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var rules []travelRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil, err
	}
	for _, rule := range rules {
		if len(rule.Aliases) == 0 {
			return nil, errors.New("travel rule has no aliases")
		}
		if rule.Cutover == "" {
			if rule.Minutes < 1 || rule.BeforeMinutes != 0 || rule.AfterMinutes != 0 {
				return nil, errors.New("invalid fixed travel duration")
			}
		} else if _, err := time.Parse("15:04", rule.Cutover); err != nil || rule.BeforeMinutes < 1 || rule.AfterMinutes < 1 || rule.Minutes != 0 {
			return nil, errors.New("invalid cutover travel duration")
		}
		for _, alias := range rule.Aliases {
			if normalizeEventTitle(alias) == "" {
				return nil, errors.New("travel rule has an empty alias")
			}
		}
	}
	return rules, nil
}

func estimateTravel(title string, start time.Time, rules []travelRule) (int, string, bool) {
	title = " " + normalizeEventTitle(title) + " "
	bestAliasLength := 0
	bestMinutes, bestLocation := 0, ""
	for _, rule := range rules {
		for _, alias := range rule.Aliases {
			normalizedAlias := normalizeEventTitle(alias)
			phrase := " " + normalizedAlias + " "
			if !strings.Contains(title, phrase) {
				continue
			}
			if len(normalizedAlias) <= bestAliasLength {
				continue
			}
			minutes := rule.Minutes
			if rule.Cutover != "" {
				cutover, _ := time.Parse("15:04", rule.Cutover)
				clock := start.Hour()*60 + start.Minute()
				if clock < cutover.Hour()*60+cutover.Minute() {
					minutes = rule.BeforeMinutes
				} else {
					minutes = rule.AfterMinutes
				}
			}
			bestAliasLength = len(normalizedAlias)
			bestMinutes, bestLocation = minutes, rule.Location
		}
	}
	return bestMinutes, bestLocation, bestAliasLength > 0
}

func normalizeEventTitle(s string) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	return strings.Join(words, " ")
}

func addTravelEstimate(event *Event, rules []travelRule, now time.Time, loc *time.Location) {
	if event == nil {
		return
	}
	start, err := time.Parse(time.RFC3339, event.Start)
	if err != nil {
		return
	}
	minutes, destination, ok := estimateTravel(event.Title, start, rules)
	if !ok {
		return
	}
	setTravelDuration(event, minutes, destination, now, loc)
}

func setTravelDuration(event *Event, minutes int, destination string, now time.Time, loc *time.Location) {
	if event == nil || minutes < 1 || minutes > 360 {
		return
	}
	travel := time.Duration(minutes) * time.Minute
	event.TravelMinutes = minutes
	event.TravelDecision = travelDecisionNeeded
	event.TravelLabel = fmt.Sprintf("%d min each way", minutes)
	if destination != "" {
		event.TravelLabel += " to " + destination
	}
	if event.AllDay {
		return
	}
	start, err := time.Parse(time.RFC3339, event.Start)
	if err != nil {
		return
	}
	leaveAt := start.Add(-travel)
	if !now.Before(start) {
		if end, err := time.Parse(time.RFC3339, event.End); err == nil && end.After(start) {
			leaveAt = end.Add(-travel)
			event.LeaveForPickup = true
		}
	}
	event.LeaveAt = leaveAt.Format(time.RFC3339)
	event.LeaveCountdown = formatLeaveCountdown(leaveAt.Sub(now))
	if !leaveAt.After(now) {
		if event.LeaveForPickup {
			event.LeaveLabel = "Leave for pickup now"
		} else {
			event.LeaveLabel = "Leave now"
		}
	} else if event.LeaveForPickup {
		event.LeaveLabel = "Leave for pickup by " + leaveAt.In(loc).Format("3:04 PM")
	} else {
		event.LeaveLabel = "Leave by " + leaveAt.In(loc).Format("3:04 PM")
	}
}

func formatLeaveCountdown(remaining time.Duration) string {
	if remaining <= 0 {
		return "NOW"
	}
	seconds := int((remaining + time.Second - 1) / time.Second)
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		if minutes%60 == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh %dm", hours, minutes%60)
	}
	days := hours / 24
	if hours%24 == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, hours%24)
}

type Day struct {
	Label  string
	Number string
	Today  bool
}
type Page struct {
	WeatherKind           string
	Hour24                int
	CalendarBadge         string
	CalendarNote          string
	CalendarMessage       string
	Weather               weatherView
	Today                 string
	Clock                 string
	Period                string
	Days                  []Day
	Events                []Event
	TravelReview          []Event
	CanManageTravel       bool
	NextUp                *Event
	SchoolCards           []SchoolCard
	SchoolAfternoon       bool
	Tasks                 []Task
	Count                 int
	CSRF                  string
	KioskMode             bool
	DevMode               bool
	TaskAction            string
}

type app struct {
	calendar       *calendarFeed
	schoolCalendar []byte
	weather        *weatherFeed
	store          *taskStore
	tmpl           *template.Template
	csrf           string
	tz             *time.Location
	now            func() time.Time
	auth           authConfig
	travelRules    []travelRule
	travelReviews  *travelDecisionStore
	devices        *deviceStore
	pairings       *pairingPool
	devMode        bool
	devVersion     string
	metricsHandler http.Handler
	pairTpl        *template.Template
	approveTpl     *template.Template
	approveDoneTpl *template.Template
	devicesTpl     *template.Template
	frigateMu      sync.Mutex
	frigateClients map[chan frigateEvent]struct{}
	frigateToken   string
	frigateActive  map[string]frigateEvent
}

type frigateEvent struct {
	ID           string  `json:"id"`
	Camera       string  `json:"camera"`
	Label        string  `json:"label"`
	Timestamp    string  `json:"timestamp"`
	Snapshot     string  `json:"snapshot"`
	ClipURL      string  `json:"clip_url"`
	Kind         string  `json:"kind,omitempty"`
	Sensor       string  `json:"sensor,omitempty"`
	TemperatureF float64 `json:"temperature_f,omitempty"`
	Severity     string  `json:"severity,omitempty"`
	Active       bool    `json:"active,omitempty"`
}

type frigateConfig struct {
	Cameras map[string]json.RawMessage `json:"cameras"`
}

func newApp(path string) (*app, error) {
	store, err := openStore(path)
	if err != nil {
		return nil, err
	}
	tpl, err := template.ParseFS(webFS, "web/index.html")
	if err != nil {
		return nil, err
	}
	tz, err := time.LoadLocation("America/Chicago")
	if err != nil {
		return nil, err
	}
	secretURL := strings.TrimSpace(os.Getenv("GOOGLE_CALENDAR_ICAL_URL"))
	feed, err := newCalendarFeed(secretURL, tz)
	if err != nil {
		if os.Getenv("FAMILY_DASHBOARD_DEV_MODE") != "1" {
			return nil, err
		}
		// A bad local .env shouldn't stop the UI developer. The bundled school
		// calendar remains available while the Google feed is disconnected.
		log.Printf("Calendar configuration warning:\n%v\n\nContinuing with the bundled school calendar because 'just serve' is for development. Fix .env and restart to use the Google feed.", err)
		feed = nil
	} else if secretURL == "" && os.Getenv("FAMILY_DASHBOARD_DEV_MODE") == "1" {
		log.Printf("Calendar: showing bundled school events; add GOOGLE_CALENDAR_ICAL_URL to .env to include family events.")
	}
	schoolCalendar, err := webFS.ReadFile("web/calendars/dunlap-school-2026-27.ics")
	if err != nil {
		return nil, fmt.Errorf("read embedded school calendar: %w", err)
	}
	weather, err := newWeatherFeed(tz)
	if err != nil {
		if os.Getenv("FAMILY_DASHBOARD_DEV_MODE") != "1" {
			return nil, err
		}
		log.Printf("Weather configuration warning: %v\nContinuing without weather in local development; fix .env and restart just serve.", err)
	}
	travelRules, err := parseTravelRules(os.Getenv("FAMILY_TRAVEL_RULES_JSON"))
	if err != nil {
		return nil, errors.New("invalid FAMILY_TRAVEL_RULES_JSON configuration")
	}
	travelRules = append(travelRules,
		travelRule{
			Aliases:  []string{"Ayla Vv"},
			Location: "Lakeview",
			Minutes:  20,
		},
		travelRule{
			Aliases:  []string{"7th grade conferences", "Wyatt conference"},
			Location: "Dunlap School",
			Minutes:  15,
		},
	)
	travelReviews, err := openTravelDecisionStore(filepath.Join(filepath.Dir(path), "travel-decisions.json"))
	if err != nil {
		return nil, fmt.Errorf("open travel decisions: %w", err)
	}
	a := &app{store: store, tmpl: tpl, csrf: newID(), tz: tz, now: time.Now, calendar: feed, schoolCalendar: schoolCalendar, weather: weather, travelRules: travelRules, travelReviews: travelReviews, devMode: os.Getenv("FAMILY_DASHBOARD_DEV_MODE") == "1", devVersion: newID(), frigateClients: make(map[chan frigateEvent]struct{}), frigateToken: strings.TrimSpace(os.Getenv("FRIGATE_EVENT_TOKEN")), frigateActive: make(map[string]frigateEvent)}
	if err := a.initializeAuth(path); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *app) page(now time.Time) Page {
	return a.pageContext(context.Background(), now)
}

func (a *app) pageContext(ctx context.Context, now time.Time) Page {
	n := now.In(a.tz)
	days := make([]Day, 0, 7)
	for i := 0; i < 7; i++ {
		d := n.AddDate(0, 0, i)
		days = append(days, Day{Label: d.Format("Mon"), Number: d.Format("2"), Today: i == 0})
	}
	sample := []struct {
		Offset   int
		Clock    string
		Title    string
		Category string
	}{
		{0, "6:30 PM", "Family dinner", "family"},
		{1, "4:00 PM", "School activity", "school"},
		{2, "5:30 PM", "Music practice", "activities"},
		{4, "10:00 AM", "Weekend plans", "family"},
	}
	events := make([]Event, 0, len(sample))
	for _, x := range sample {
		d := n.AddDate(0, 0, x.Offset)
		label := d.Format("Jan 2")
		if x.Offset == 0 {
			label = "Today"
		} else if x.Offset == 1 {
			label = "Tomorrow"
		}
		clock, _ := time.ParseInLocation("3:04 PM", x.Clock, a.tz)
		start := time.Date(d.Year(), d.Month(), d.Day(), clock.Hour(), clock.Minute(), 0, 0, a.tz)
		end := start.Add(time.Hour)
		events = append(events, Event{Date: label, Weekday: d.Format("Mon"), Time: x.Clock, Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339), Relative: relativeTime(start, n), Title: x.Title, Category: x.Category})
	}
	badge, note, message := "SCHOOL CALENDAR", "Dunlap CUSD 2026–27 · local iCal", ""
	schoolItems, schoolErr := parseICalendar(a.schoolCalendar, n, a.tz)
	if schoolErr != nil {
		message = "School calendar unavailable: " + schoolErr.Error()
	}
	if a.calendar == nil {
		events = nil
	}
	if a.calendar != nil {
		items, status, problem := a.calendar.snapshotContext(ctx, n)
		badge = "GOOGLE CALENDAR"
		note = status
		message = problem
		events = make([]Event, 0, len(items))
		for _, item := range items {
			d := item.start.In(a.tz)
			label := d.Format("Jan 2")
			if midnight(d, a.tz).Equal(midnight(n, a.tz)) {
				label = "Today"
			} else if midnight(d, a.tz).Equal(midnight(n, a.tz).AddDate(0, 0, 1)) {
				label = "Tomorrow"
			}
			clock := d.Format("3:04 PM")
			if item.allDay {
				clock = "All day"
			} else if item.end.Sub(item.start) > time.Hour {
				clock = clock + " – " + item.end.In(a.tz).Format("3:04 PM")
			}
			events = append(events, Event{Date: label, Weekday: d.Format("Mon"), Time: clock, Start: item.start.Format(time.RFC3339), End: item.end.Format(time.RFC3339), Relative: relativeTime(item.start, n), AllDay: item.allDay, SpanDays: allDayDays(item.start, item.end, a.tz), Title: item.title, Category: "family", ReviewKey: travelDecisionKey(item)})
		}
		if len(events) == 0 && message == "" {
			message = "No upcoming events this week."
		}
	}
	if a.calendar != nil {
		badge = "SCHOOL + GOOGLE CALENDAR"
	}
	travelReview := make([]Event, 0)
	for i := range events {
		event := &events[i]
		addTravelEstimate(event, a.travelRules, n, a.tz)
		if event.ReviewKey == "" {
			continue
		}
		if decision, ok := a.travelReviews.get(event.ReviewKey); ok {
			switch decision.State {
			case travelDecisionNeeded:
				setTravelDuration(event, decision.Minutes, decision.Location, n, a.tz)
			case travelDecisionNone:
				event.TravelMinutes = 0
				event.LeaveAt = ""
				event.LeaveCountdown = ""
				event.LeaveLabel = ""
				event.TravelLabel = "No travel needed"
				event.TravelDecision = travelDecisionNone
			case travelDecisionPending:
				event.TravelMinutes = 0
				event.LeaveAt = ""
				event.LeaveCountdown = ""
				event.LeaveLabel = ""
				event.TravelLabel = ""
				event.LeaveForPickup = false
				event.TravelDecision = travelDecisionPending
			}
		} else if event.TravelMinutes > 0 {
			event.TravelDecision = travelDecisionNeeded
		} else {
			event.TravelDecision = travelDecisionPending
		}
		end, endErr := time.Parse(time.RFC3339, event.End)
		if event.TravelDecision == travelDecisionPending && endErr == nil && end.After(n) {
			travelReview = append(travelReview, *event)
		}
	}
	// Order family events by their actual start time before selecting the
	// featured event or rendering the list.
	upcoming := events[:0]
	for _, event := range events {
		end, err := time.Parse(time.RFC3339, event.End)
		if err == nil && end.After(n) {
			upcoming = append(upcoming, event)
		}
	}
	events = upcoming
	sort.SliceStable(events, func(i, j int) bool {
		startI, errI := time.Parse(time.RFC3339, events[i].Start)
		startJ, errJ := time.Parse(time.RFC3339, events[j].Start)
		if errI != nil || errJ != nil {
			return errI == nil && errJ != nil
		}
		return startI.Before(startJ)
	})
	nextUp, events := nextEvent(events, n)
	tasks := a.store.list()
	count := 0
	for _, t := range tasks {
		if !t.Done {
			count++
		}
	}
	wv := a.weather.snapshotContext(ctx, n)
	schoolCards := schoolDayCards(schoolItems, wv, n, a.tz)
	addEventWeather(nextUp, wv, n, a.tz)
	weatherKind := "unknown"
	if len(wv.Slots) > 0 {
		weatherKind = wv.Slots[0].Icon
	}
	return Page{WeatherKind: weatherKind, Hour24: n.Hour(), Today: n.Format("Mon, Jan 2"), Clock: n.Format("3:04"), Period: n.Format("PM"), Days: days, Events: events, TravelReview: travelReview, NextUp: nextUp, SchoolCards: schoolCards, SchoolAfternoon: n.Hour() >= 11, Tasks: tasks, Count: count, CSRF: a.csrf, CalendarBadge: badge, CalendarNote: note, CalendarMessage: message, Weather: wv}
}

func schoolDayCards(items []calendarItem, weather weatherView, now time.Time, loc *time.Location) []SchoolCard {
	var aylaArrive, aylaLeave, wyattBus, wyattHome *calendarItem
	for i := range items {
		x := &items[i]
		if !midnight(x.start.In(loc), loc).Equal(midnight(now, loc)) {
			continue
		}
		t := strings.ToLower(x.title)
		switch {
		case strings.Contains(t, "ayla") && strings.Contains(t, "arrive at school"):
			aylaArrive = x
		case strings.Contains(t, "ayla") && strings.Contains(t, "leave school"):
			aylaLeave = x
		case strings.Contains(t, "wyatt") && strings.Contains(t, "bus ride to school"):
			wyattBus = x
		case strings.Contains(t, "wyatt") && strings.Contains(t, "bus ride home"):
			wyattHome = x
		}
	}
	if aylaArrive == nil || aylaLeave == nil || wyattBus == nil || wyattHome == nil {
		return nil
	}
	chance := func(at time.Time) bool {
		for _, slot := range weather.Forecast {
			if d := slot.At.Sub(at); d >= -90*time.Minute && d <= 90*time.Minute && (slot.PopValue > 0 || slot.RainValue > 0 || strings.Contains(strings.ToLower(slot.Condition), "rain")) {
				return true
			}
		}
		return false
	}
	remaining := func(t time.Time) string { return formatLeaveCountdown(t.Sub(now)) }
	leaveAyla := aylaArrive.start
	leaveWyatt := wyattBus.start.Add(-15 * time.Minute)
	pickup := aylaLeave.start.Add(-25 * time.Minute)
	rain := chance(aylaLeave.start)
	rainNote := ""
	if rain {
		rainNote = "Rain is possible during Ayla’s walk home; plan to pick her up."
	}
	wear := "Light layers"
	if len(weather.Forecast) > 0 {
		var nearest *weatherSlot
		var best time.Duration
		for i := range weather.Forecast {
			d := weather.Forecast[i].At.Sub(leaveAyla)
			if d < 0 {
				d = -d
			}
			if nearest == nil || d < best {
				nearest = &weather.Forecast[i]
				best = d
			}
		}
		if nearest != nil {
			wear = nearest.Wear
			if nearest.RainValue > 0 || nearest.PopValue >= .2 {
				wear += " · rain jacket"
			}
			for _, s := range weather.Forecast {
				if s.At.After(leaveAyla) && s.At.Before(aylaLeave.start) && s.FeelsValue+10 < nearest.FeelsValue {
					wear += " · bring a coat for the colder afternoon"
					break
				}
			}
		}
	}
	alertAt := pickup.Add(-2 * time.Hour)
	alert := rain
	return []SchoolCard{
		{Name: "Ayla", LeaveAt: leaveAyla.Format(time.RFC3339), LateAt: aylaArrive.end.Format(time.RFC3339), HomeAt: aylaLeave.end.Format(time.RFC3339), LeaveClock: leaveAyla.In(loc).Format("3:04 PM"), HomeClock: aylaLeave.end.In(loc).Format("3:04 PM"), LeaveCountdown: remaining(leaveAyla), LateCountdown: remaining(aylaArrive.end), HomeCountdown: remaining(aylaLeave.end), Wear: wear, RainNote: rainNote, PickupAlert: alert, PickupAlertAt: alertAt.Format(time.RFC3339), PickupAt: pickup.Format(time.RFC3339), PickupCountdown: remaining(pickup)},
		{Name: "Wyatt", LeaveAt: leaveWyatt.Format(time.RFC3339), LateAt: wyattBus.start.Format(time.RFC3339), HomeAt: wyattHome.end.Format(time.RFC3339), LeaveClock: leaveWyatt.In(loc).Format("3:04 PM"), HomeClock: wyattHome.end.In(loc).Format("3:04 PM"), LeaveCountdown: remaining(leaveWyatt), LateCountdown: remaining(wyattBus.start), HomeCountdown: remaining(wyattHome.end), Wear: wear},
	}
}

// relativeTime gives calendar items a compact label that fits beside their time.
func relativeTime(start, now time.Time) string {
	delta := start.Sub(now).Seconds()
	if math.Abs(delta) < 1 {
		return "now"
	}
	future := delta > 0
	seconds := math.Abs(delta)
	unit, size := "s", 1.0
	switch {
	case seconds >= 365.25*24*60*60:
		unit, size = "y", 365.25*24*60*60
	case seconds >= 30.44*24*60*60:
		unit, size = "mo", 30.44*24*60*60
	case seconds >= 7*24*60*60:
		unit, size = "w", 7*24*60*60
	case seconds >= 24*60*60:
		unit, size = "d", 24*60*60
	case seconds >= 60*60:
		unit, size = "h", 60*60
	case seconds >= 60:
		unit, size = "m", 60
	}
	count := int(math.Round(seconds / size))
	if count < 1 {
		count = 1
	}
	if future {
		return fmt.Sprintf("in %d%s", count, unit)
	}
	return fmt.Sprintf("%d%s ago", count, unit)
}

// allDayDays counts calendar dates covered by an all-day event. iCalendar's
// DTEND is exclusive, so a July 1 through July 3 event covers two days.
func allDayDays(start, end time.Time, loc *time.Location) int {
	startDay := midnight(start, loc)
	endDay := midnight(end, loc)
	days := 0
	for day := startDay; day.Before(endDay) && days < 366; day = day.AddDate(0, 0, 1) {
		days++
	}
	return days
}

func nextEvent(events []Event, now time.Time) (*Event, []Event) {
	for i, event := range events {
		if event.TravelMinutes == 0 {
			continue
		}
		start, startErr := time.Parse(time.RFC3339, event.Start)
		end, endErr := time.Parse(time.RFC3339, event.End)
		if startErr != nil || endErr != nil || start.After(now) || !end.After(now) {
			continue
		}
		event.Status = "IN PROGRESS"
		event.Countdown = "NOW"
		return &event, append(events[:i], events[i+1:]...)
	}
	for i, event := range events {
		start, startErr := time.Parse(time.RFC3339, event.Start)
		end, endErr := time.Parse(time.RFC3339, event.End)
		if startErr != nil || endErr != nil || !start.After(now) || !end.After(now) {
			continue
		}
		event.Status = "NEXT UP"
		if event.AllDay {
			event.Countdown = event.Date
		} else {
			event.Countdown = relativeTime(start, now)
		}
		return &event, append(events[:i], events[i+1:]...)
	}
	return nil, events
}

func (a *app) renderPage(w http.ResponseWriter, r *http.Request, kiosk bool) {
	securityHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	pg := a.pageContext(r.Context(), a.now())
	pg.KioskMode = kiosk
	pg.DevMode = a.devMode
	pg.CanManageTravel = !kiosk && a.human(r, "family-hq:manage")
	if kiosk {
		pg.TaskAction = "/kiosk/tasks"
	} else {
		pg.TaskAction = "/tasks"
	}
	if err := a.tmpl.Execute(w, pg); err != nil {
		log.Printf("render: %v", err)
	}
}

func (a *app) receiveFrigateEvent(w http.ResponseWriter, r *http.Request) {
	if a.frigateToken == "" {
		http.Error(w, "Frigate event integration is not configured", http.StatusServiceUnavailable)
		return
	}
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(provided), []byte(a.frigateToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	var event frigateEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil || event.ID == "" {
		http.Error(w, "invalid Frigate event", http.StatusBadRequest)
		return
	}
	if event.Kind == "" {
		event.Kind = "person"
	}
	if event.Kind == "person" {
		if event.Label != "person" || event.Camera == "" {
			http.Error(w, "invalid person event", http.StatusBadRequest)
			return
		}
	} else if event.Kind == "temperature" || event.Kind == "water" {
		if event.Sensor == "" || event.Severity == "" {
			http.Error(w, "invalid sensor event", http.StatusBadRequest)
			return
		}
	} else {
		http.Error(w, "unsupported event kind", http.StatusBadRequest)
		return
	}
	a.frigateMu.Lock()
	if a.frigateActive == nil {
		a.frigateActive = make(map[string]frigateEvent)
	}
	if event.Kind != "person" {
		key := event.Kind + ":" + event.Sensor
		if event.Active {
			a.frigateActive[key] = event
		} else {
			delete(a.frigateActive, key)
		}
	}
	for client := range a.frigateClients {
		select {
		case client <- event:
		default:
		}
	}
	a.frigateMu.Unlock()
	w.WriteHeader(http.StatusAccepted)
}

func (a *app) essentialsSensors(w http.ResponseWriter, r *http.Request) {
	backendURL := strings.TrimRight(strings.TrimSpace(os.Getenv("SENSOR_BACKEND_URL")), "/")
	if backendURL == "" || a.frigateToken == "" {
		http.Error(w, "sensor service is not configured", http.StatusServiceUnavailable)
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, backendURL, nil)
	if err != nil {
		http.Error(w, "sensor service is unavailable", http.StatusBadGateway)
		return
	}
	request.Header.Set("Authorization", "Bearer "+a.frigateToken)
	response, err := (&http.Client{Timeout: 8 * time.Second}).Do(request)
	if err != nil {
		http.Error(w, "sensor service is unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		http.Error(w, "sensor service is unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	if _, err := io.Copy(w, io.LimitReader(response.Body, 1<<20)); err != nil {
		log.Printf("copy sensor readings: %v", err)
	}
}

func (a *app) frigateRequest(r *http.Request, endpoint string) (*http.Response, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("FRIGATE_BASE_URL")), "/")
	if baseURL == "" {
		return nil, errors.New("Frigate is not configured")
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, baseURL+endpoint, nil)
	if err != nil {
		return nil, err
	}
	return (&http.Client{Timeout: 8 * time.Second}).Do(request)
}

func (a *app) essentialsCameras(w http.ResponseWriter, r *http.Request) {
	response, err := a.frigateRequest(r, "/api/config")
	if err != nil {
		http.Error(w, "Frigate camera list is unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		http.Error(w, "Frigate camera list is unavailable", http.StatusBadGateway)
		return
	}
	var config frigateConfig
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&config); err != nil {
		http.Error(w, "Frigate camera list is invalid", http.StatusBadGateway)
		return
	}
	cameras := make([]string, 0, len(config.Cameras))
	for camera := range config.Cameras {
		if camera != "" && !strings.ContainsAny(camera, "/\\") {
			cameras = append(cameras, camera)
		}
	}
	sort.Strings(cameras)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(map[string][]string{"cameras": cameras})
}

func (a *app) essentialsCameraSnapshot(w http.ResponseWriter, r *http.Request) {
	camera := r.PathValue("camera")
	if camera == "" {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		offset := 0
		if len(parts) == 6 && parts[0] == "kiosk" {
			offset = 1
		}
		if len(parts) == offset+5 && parts[offset] == "api" && parts[offset+1] == "essentials" && parts[offset+2] == "cameras" && parts[offset+4] == "snapshot" {
			camera, _ = url.PathUnescape(parts[offset+3])
		}
	}
	if camera == "" || strings.ContainsAny(camera, "/\\") {
		http.Error(w, "invalid camera", http.StatusBadRequest)
		return
	}
	response, err := a.frigateRequest(r, "/api/"+url.PathEscape(camera)+"/latest.jpg")
	if err != nil {
		http.Error(w, "camera snapshot is unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		http.Error(w, "camera snapshot is unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(w, io.LimitReader(response.Body, 8<<20)); err != nil {
		log.Printf("copy Frigate snapshot camera=%s: %v", camera, err)
	}
}

func (a *app) streamFrigateEvents(w http.ResponseWriter, r *http.Request) {
	if !a.human(r, "family-hq:read") && !a.frame(r) {
		http.Error(w, "Family HQ access is required", http.StatusForbidden)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		http.Error(w, "streaming deadline cannot be cleared", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	client := make(chan frigateEvent, 4)
	a.frigateMu.Lock()
	a.frigateClients[client] = struct{}{}
	for _, event := range a.frigateActive {
		select {
		case client <- event:
		default:
		}
	}
	a.frigateMu.Unlock()
	defer func() {
		a.frigateMu.Lock()
		delete(a.frigateClients, client)
		a.frigateMu.Unlock()
	}()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		case event := <-client:
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: frigate\ndata: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		a.publicHome(w, r)
	})
	mux.HandleFunc("GET /dashboard", a.requireHuman("family-hq:read", func(w http.ResponseWriter, r *http.Request) {
		a.renderPage(w, r, false)
	}))
	mux.HandleFunc("POST /api/frigate/events", a.receiveFrigateEvent)
	mux.HandleFunc("GET /api/frigate/events/stream", a.requireHuman("family-hq:read", a.streamFrigateEvents))
	mux.HandleFunc("GET /api/essentials/sensors", a.requireDashboardReader(a.essentialsSensors))
	mux.HandleFunc("GET /api/essentials/cameras", a.requireDashboardReader(a.essentialsCameras))
	mux.HandleFunc("GET /api/essentials/cameras/{camera}/snapshot", a.requireDashboardReader(a.essentialsCameraSnapshot))
	mux.HandleFunc("GET /kiosk/api/essentials/sensors", a.requireDashboardReader(a.essentialsSensors))
	mux.HandleFunc("GET /kiosk/api/essentials/cameras", a.requireDashboardReader(a.essentialsCameras))
	mux.HandleFunc("GET /kiosk/api/essentials/cameras/{camera}/snapshot", a.requireDashboardReader(a.essentialsCameraSnapshot))
	mux.HandleFunc("GET /og", a.openGraphImage)
	mux.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		b, err := webFS.ReadFile("web/manifest.webmanifest")
		if err != nil {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		b, err := webFS.ReadFile("web/sw.js")
		if err != nil {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /icons/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name != "family-hq-192.png" && name != "family-hq-512.png" && name != "family-hq-maskable-512.png" {
			http.NotFound(w, r)
			return
		}
		b, err := webFS.ReadFile("web/icons/" + name)
		if err != nil {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /session", a.requireHuman("family-hq:read", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]bool{
			"authenticated": true,
			"canManage":     hasScope(r.Header.Get("X-Hlab-Scopes"), "family-hq:manage"),
		})
	}))
	mux.HandleFunc("GET /home.js", func(w http.ResponseWriter, r *http.Request) {
		b, err := webFS.ReadFile("web/home.js")
		if err != nil {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /__dev/ping", func(w http.ResponseWriter, r *http.Request) {
		if !a.devMode {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, a.devVersion)
	})
	mux.HandleFunc("GET /__dev/ws", func(w http.ResponseWriter, r *http.Request) {
		if !a.devMode {
			http.NotFound(w, r)
			return
		}
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true
				}
				parsed, err := url.Parse(origin)
				return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.EqualFold(parsed.Host, r.Host)
			},
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// A new server process has a new version. The client reconnects after
		// a restart and compares this value with the one it already saw.
		if err := conn.WriteMessage(websocket.TextMessage, []byte(a.devVersion)); err != nil {
			return
		}
		// net/http's WriteTimeout applies to the original request. The WebSocket
		// is a long-lived connection, so clear that request deadline after upgrade.
		_ = conn.UnderlyingConn().SetDeadline(time.Time{})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("GET /dev-reload.js", func(w http.ResponseWriter, r *http.Request) {
		if !a.devMode {
			http.NotFound(w, r)
			return
		}
		b, err := webFS.ReadFile("web/dev-reload.js")
		if err != nil {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		b, err := webFS.ReadFile("web/favicon.ico")
		if err != nil {
			http.Error(w, "unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/x-icon")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) {
		b, err := webFS.ReadFile("web/app.js")
		if err != nil {
			http.Error(w, "unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(b)
	})
	mux.HandleFunc("GET /style.css", func(w http.ResponseWriter, r *http.Request) {
		b, err := webFS.ReadFile("web/style.css")
		if err != nil {
			http.Error(w, "unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=600")
		w.Write(b)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		if a.metricsHandler == nil {
			http.NotFound(w, r)
			return
		}
		a.metricsHandler.ServeHTTP(w, r)
	})
	mux.HandleFunc("POST /tasks", a.requireHuman("family-hq:read", a.mutate))
	mux.HandleFunc("POST /travel-review", a.requireHuman("family-hq:read", a.reviewTravel))
	a.authRoutes(mux)
	return instrumentHTTP(mux)
}

// publicHome serves a data-free landing page. The private dashboard lives at
// /dashboard so the public root can bypass ForwardAuth at the ingress.
func (a *app) publicHome(w http.ResponseWriter, r *http.Request) {
	baseURL := a.auth.publicURL
	if baseURL == "" {
		scheme := "https"
		if r.TLS == nil {
			scheme = "http"
		}
		baseURL = scheme + "://" + r.Host
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	imageURL := baseURL + "/og"
	reloadScript := ""
	if a.devMode {
		reloadScript = `<script src="/dev-reload.js" defer></script>`
	}
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="theme-color" content="#26352c"><title>Family HQ — Our little corner of the world</title>
<link rel="icon" href="/favicon.ico" sizes="32x32" type="image/x-icon">
<meta name="description" content="A calm, shared home for the family calendar, weather, and everyday tasks.">
<meta property="og:type" content="website"><meta property="og:title" content="Family HQ">
<meta property="og:description" content="A calm, shared home for the family calendar, weather, and everyday tasks.">
<meta property="og:image" content="%s"><meta property="og:image:type" content="image/png"><meta property="og:image:alt" content="Family HQ — Our little corner of the world">
<meta property="og:image:width" content="1200"><meta property="og:image:height" content="630">
<meta name="twitter:card" content="summary_large_image"><meta name="twitter:title" content="Family HQ">
<meta name="twitter:description" content="A calm, shared home for the family calendar, weather, and everyday tasks."><meta name="twitter:image" content="%s">
%s<script src="/home.js?v=display-2" defer></script><style>*{box-sizing:border-box}body{margin:0;min-height:100vh;display:grid;place-items:center;background:#26352c;color:#f6f3e9;font:16px/1.5 system-ui,sans-serif}.card{width:min(720px,calc(100%% - 40px));padding:clamp(32px,8vw,76px);border:1px solid #536356;border-radius:28px;background:linear-gradient(145deg,#34473a,#293a30);box-shadow:0 28px 90px #1118}p{color:#c4d0bf;margin:0 0 12px}.eyebrow{text-transform:uppercase;letter-spacing:.16em;font-size:.75rem;color:#b8c58f}h1{font-size:clamp(3rem,10vw,6rem);line-height:.98;letter-spacing:-.06em;margin:0 0 24px}h1 span{color:#d4a86a}.copy{max-width:38rem;font-size:1.2rem;margin-bottom:20px}.session-status{min-height:1.5em;color:#d4a86a;font-size:.95rem}.actions{display:flex;flex-wrap:wrap;gap:10px;margin-top:16px}.pill{display:inline-block;border:1px solid #829179;border-radius:999px;padding:10px 16px;color:#f6f3e9;text-decoration:none}.pill:hover{background:#536356}.pill.secondary{background:transparent}.pill[hidden]{display:none}</style></head>
<body><main class="card"><p class="eyebrow">A little more in sync</p><h1>Family HQ<span>.</span></h1><p class="copy">Your family’s calendar, the weather, and everyday tasks together in one calm place.</p><p class="session-status" id="auth-status" aria-live="polite">Checking sign-in…</p><nav class="actions" aria-label="Family HQ options"><a class="pill" id="dashboard-link" href="/dashboard">Sign in to Family HQ</a><a class="pill secondary" href="/kiosk">Pair a display with your phone</a><a class="pill secondary" id="devices-link" href="/devices" hidden>Manage paired displays</a></nav></main></body></html>`, template.HTMLEscapeString(imageURL), template.HTMLEscapeString(imageURL), reloadScript)
}

func (a *app) openGraphImage(w http.ResponseWriter, r *http.Request) {
	b, err := webFS.ReadFile("web/og.png")
	if err != nil {
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

func (a *app) mutate(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(r) {
		http.Error(w, "invalid request origin", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		http.Error(w, "form required", http.StatusUnsupportedMediaType)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(a.csrf)) != 1 {
		http.Error(w, "forbidden", 403)
		return
	}
	action := r.PostForm.Get("action")
	if (action == "add" || action == "delete") && !a.human(r, "family-hq:manage") {
		http.Error(w, "Family HQ management access required", 403)
		return
	}
	var err error
	switch action {
	case "add":
		title := strings.TrimSpace(r.PostForm.Get("title"))
		if len(title) == 0 || len(title) > 120 || strings.ContainsAny(title, "\x00\r\n") {
			http.Error(w, "task title must be 1–120 characters", 400)
			return
		}
		err = a.store.add(title)
	case "toggle", "delete":
		err = a.store.change(r.PostForm.Get("id"), action)
	default:
		http.Error(w, "invalid action", 400)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (a *app) reviewTravel(w http.ResponseWriter, r *http.Request) {
	if !a.human(r, "family-hq:manage") {
		http.Error(w, "Family HQ management access required", http.StatusForbidden)
		return
	}
	if a.calendar == nil {
		http.Error(w, "family calendar is not configured", http.StatusServiceUnavailable)
		return
	}
	if !a.sameOrigin(r) {
		http.Error(w, "invalid request origin", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		http.Error(w, "form required", http.StatusUnsupportedMediaType)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(a.csrf)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	key := r.PostForm.Get("key")
	if key == "" || len(key) > 512 {
		http.Error(w, "invalid calendar event", http.StatusBadRequest)
		return
	}
	items, _, _ := a.calendar.snapshotContext(r.Context(), a.now())
	found := false
	for _, item := range items {
		if travelDecisionKey(item) == key {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "calendar event is no longer upcoming", http.StatusConflict)
		return
	}
	var err error
	switch r.PostForm.Get("action") {
	case travelDecisionNone:
		err = a.travelReviews.set(key, travelDecisionNone, 0, "", a.now())
	case travelDecisionNeeded:
		minutes, parseErr := strconv.Atoi(r.PostForm.Get("minutes"))
		if parseErr != nil {
			http.Error(w, "enter a travel time in minutes", http.StatusBadRequest)
			return
		}
		err = a.travelReviews.set(key, travelDecisionNeeded, minutes, r.PostForm.Get("location"), a.now())
	case "reopen":
		err = a.travelReviews.set(key, travelDecisionPending, 0, "", a.now())
	default:
		http.Error(w, "invalid travel decision", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard#travel-review", http.StatusSeeOther)
}

func main() {
	path := os.Getenv("TASKS_FILE")
	if path == "" {
		path = "./data/tasks.json"
	}
	a, err := newApp(path)
	if err != nil {
		log.Fatal(err)
	}
	telemetry, err := setupTelemetry(context.Background())
	if err != nil {
		log.Fatalf("initialize telemetry: %v", err)
	}
	a.metricsHandler = telemetry.metricsHandler
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	server := &http.Server{Addr: addr, Handler: a.routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	if err := a.validateProductionStart(addr); err != nil {
		log.Fatal(err)
	}
	a.authSummary()
	log.Printf("family dashboard listening %s", addr)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("server: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := telemetry.shutdown(ctx); err != nil {
		log.Printf("telemetry shutdown: %v", err)
	}
}
