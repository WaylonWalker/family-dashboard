package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
	a.now = func() time.Time { return time.Date(2026, 10, 26, 15, 30, 0, 0, time.UTC) }
	h := a.routes()
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if r.Code != 200 || !strings.Contains(r.Body.String(), "SCHOOL CALENDAR") || !strings.Contains(r.Body.String(), "School morning") || strings.Contains(r.Body.String(), "School Day —") || strings.Contains(r.Body.String(), "Bus ride to school") || !strings.Contains(r.Body.String(), "Family tasks") || !strings.Contains(r.Body.String(), "Cameras") {
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

func TestEssentialsAPIsProxyCurrentSensorsAndAllFrigateCameras(t *testing.T) {
	sensorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sensor-test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"readings":[{"kind":"temperature","name":"freezer","temperature_f":12.5}],"updated_at":"2026-10-21T12:00:00Z"}`))
	}))
	defer sensorServer.Close()
	frigateServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"cameras":{"front-door":{},"freezer":{}}}`))
		case "/api/freezer/latest.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpeg-snapshot"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer frigateServer.Close()
	t.Setenv("SENSOR_BACKEND_URL", sensorServer.URL)
	t.Setenv("FRIGATE_BASE_URL", frigateServer.URL)
	a := proxyApp(t)
	a.frigateToken = "sensor-test-token"

	readings := served(a, humanReq(http.MethodGet, "/api/essentials/sensors", "", "family-hq:read"))
	if readings.Code != http.StatusOK || !strings.Contains(readings.Body.String(), `"temperature_f":12.5`) {
		t.Fatalf("sensor response status %d body %s", readings.Code, readings.Body.String())
	}
	cameras := served(a, humanReq(http.MethodGet, "/api/essentials/cameras", "", "family-hq:read"))
	if cameras.Code != http.StatusOK || !strings.Contains(cameras.Body.String(), "front-door") || !strings.Contains(cameras.Body.String(), "freezer") {
		t.Fatalf("camera response status %d body %s", cameras.Code, cameras.Body.String())
	}
	snapshot := served(a, humanReq(http.MethodGet, "/api/essentials/cameras/freezer/snapshot", "", "family-hq:read"))
	if snapshot.Code != http.StatusOK || snapshot.Header().Get("Content-Type") != "image/jpeg" || snapshot.Body.String() != "jpeg-snapshot" {
		t.Fatalf("snapshot response status %d type %q body %s", snapshot.Code, snapshot.Header().Get("Content-Type"), snapshot.Body.String())
	}
	_, frameToken, err := a.devices.create("test-tv", "test-user", a.now())
	if err != nil {
		t.Fatal(err)
	}
	frameRequest := httptest.NewRequest(http.MethodGet, "/kiosk/api/essentials/cameras/freezer/snapshot", nil)
	frameRequest.AddCookie(&http.Cookie{Name: deviceCookieName, Value: frameToken})
	frameSnapshot := served(a, frameRequest)
	if frameSnapshot.Code != http.StatusOK || frameSnapshot.Body.String() != "jpeg-snapshot" {
		t.Fatalf("paired TV snapshot status %d body %s", frameSnapshot.Code, frameSnapshot.Body.String())
	}
}

func TestEssentialsCameraLiveProxiesMSEStream(t *testing.T) {
	t.Setenv("FAMILY_AUTH_MODE", "off")
	t.Setenv("FAMILY_TEST_PAIRING", "")
	streamName := make(chan string, 1)
	frigateUpgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	frigateServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/config":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"cameras":{"freezer":{},"front-door":{}}}`))
		case "/api/go2rtc/streams":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"freezer":{"producers":[{}]},"front-door_main":{"producers":[{}]}}`))
		case "/live/mse/api/ws":
			streamName <- r.URL.Query().Get("src")
			conn, err := frigateUpgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
			_ = conn.WriteJSON(map[string]string{"type": "mse", "value": `video/mp4; codecs="avc1.64001F"`})
			_ = conn.WriteMessage(websocket.BinaryMessage, []byte("init-segment"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer frigateServer.Close()
	t.Setenv("FRIGATE_BASE_URL", frigateServer.URL)
	a, err := newApp(filepath.Join(t.TempDir(), "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	dashboardServer := httptest.NewServer(a.routes())
	defer dashboardServer.Close()
	wsURL := "ws" + strings.TrimPrefix(dashboardServer.URL, "http") + "/api/essentials/cameras/front-door/live"
	client, handshake, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		if handshake != nil {
			t.Fatalf("dial dashboard live stream: %v (status %s)", err, handshake.Status)
		}
		t.Fatalf("dial dashboard live stream: %v", err)
	}
	defer client.Close()
	if err := client.WriteJSON(map[string]string{"type": "mse", "value": "avc1.64001F"}); err != nil {
		t.Fatal(err)
	}
	var response map[string]string
	if err := client.ReadJSON(&response); err != nil {
		t.Fatalf("read MSE response: %v", err)
	}
	if response["type"] != "mse" || !strings.Contains(response["value"], "video/mp4") {
		t.Fatalf("unexpected MSE response: %#v", response)
	}
	messageType, message, err := client.ReadMessage()
	if err != nil || messageType != websocket.BinaryMessage || string(message) != "init-segment" {
		t.Fatalf("read stream segment: type %d body %q err %v", messageType, message, err)
	}
	if got := <-streamName; got != "front-door_main" {
		t.Fatalf("Frigate stream name = %q, want front-door_main", got)
	}
	attackerOrigin := http.Header{"Origin": []string{"https://attacker.invalid"}}
	_, handshake, err = websocket.DefaultDialer.Dial(wsURL, attackerOrigin)
	if err == nil || handshake == nil || handshake.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin websocket status = %v, error = %v; want 403", handshake, err)
	}
	unknownURL := "ws" + strings.TrimPrefix(dashboardServer.URL, "http") + "/api/essentials/cameras/not-a-camera/live"
	_, handshake, err = websocket.DefaultDialer.Dial(unknownURL, nil)
	if err == nil || handshake == nil || handshake.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown camera websocket status = %v, error = %v; want 404", handshake, err)
	}
}

func TestSchoolDayCardsUseSchoolTimesAndRainPickup(t *testing.T) {
	loc := chicago(t)
	day := dateIn(t, "2026-10-20 00:00")
	items := []calendarItem{
		{title: "Ayla — Arrive at school", start: day.Add(7*time.Hour + 20*time.Minute), end: day.Add(7*time.Hour + 40*time.Minute)},
		{title: "Wyatt — Bus ride to school", start: day.Add(7*time.Hour + 45*time.Minute), end: day.Add(8*time.Hour + 5*time.Minute)},
		{title: "Ayla — Leave school", start: day.Add(14*time.Hour + 5*time.Minute), end: day.Add(14*time.Hour + 15*time.Minute)},
		{title: "Wyatt — Bus ride home from school", start: day.Add(15*time.Hour + 5*time.Minute), end: day.Add(15*time.Hour + 25*time.Minute)},
	}
	forecast := []weatherSlot{
		{At: day.Add(7 * time.Hour), Wear: "Jacket & layers", FeelsValue: 50},
		{At: day.Add(14 * time.Hour), Condition: "light rain", PopValue: .1, FeelsValue: 30},
	}
	weather := weatherView{Forecast: forecast}
	cards := schoolDayCards(items, weather, day.Add(10*time.Hour), loc)
	if len(cards) != 2 {
		t.Fatalf("cards = %+v, want Ayla and Wyatt", cards)
	}
	ayla, wyatt := cards[0], cards[1]
	if got := ayla.LeaveAt; got != items[0].start.Format(time.RFC3339) {
		t.Fatalf("Ayla leave = %s, want school event time %s", got, items[0].start.Format(time.RFC3339))
	}
	if got, want := wyatt.LeaveAt, items[1].start.Add(-15*time.Minute).Format(time.RFC3339); got != want {
		t.Fatalf("Wyatt leave = %s, want 15 minutes before bus at %s", got, want)
	}
	if got, want := wyatt.LateAt, items[1].start.Format(time.RFC3339); got != want {
		t.Fatalf("Wyatt late time = %s, want bus time %s", got, want)
	}
	if got, want := ayla.HomeAt, items[2].end.Format(time.RFC3339); got != want {
		t.Fatalf("Ayla home time = %s, want event end %s", got, want)
	}
	if !strings.Contains(ayla.RainNote, "plan to pick her up") || !strings.Contains(ayla.Wear, "colder afternoon") {
		t.Fatalf("Ayla weather advice = %+v, want rain pickup and colder afternoon coat", ayla)
	}
	if !ayla.PickupAlert {
		t.Fatal("rain pickup alert should be available when rain is forecast")
	}
	if got, want := ayla.PickupAlertAt, day.Add(11*time.Hour+40*time.Minute).Format(time.RFC3339); got != want {
		t.Fatalf("pickup alert begins at %s, want %s", got, want)
	}
	cards = schoolDayCards(items, weather, day.Add(11*time.Hour+40*time.Minute), loc)
	if len(cards) != 2 || !cards[0].PickupAlert {
		t.Fatalf("pickup alert should be active two hours before pickup: %+v", cards)
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

func TestTravelRulesMatchTitlesAndApplyCutovers(t *testing.T) {
	rules, err := parseTravelRules(`[
		{"aliases":["studio","acting 2"],"location":"Lakeview","minutes":20},
		{"aliases":["appointment"],"cutover":"16:00","before_minutes":45,"after_minutes":15}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	loc := chicago(t)
	start := time.Date(2026, time.October, 21, 16, 30, 0, 0, loc)
	if minutes, place, ok := estimateTravel("Ayla Vv Studio", start, rules); !ok || minutes != 20 || place != "Lakeview" {
		t.Fatalf("studio estimate = %d, %q, %v", minutes, place, ok)
	}
	if minutes, _, ok := estimateTravel("Wyatt appointment", time.Date(2026, 10, 21, 15, 59, 0, 0, loc), rules); !ok || minutes != 45 {
		t.Fatalf("before-cutover estimate = %d, %v", minutes, ok)
	}
	if minutes, _, ok := estimateTravel("Wyatt appointment", time.Date(2026, 10, 21, 16, 0, 0, 0, loc), rules); !ok || minutes != 15 {
		t.Fatalf("after-cutover estimate = %d, %v", minutes, ok)
	}
	event := Event{Title: "Ayla Vv Studio", Start: start.Format(time.RFC3339)}
	addTravelEstimate(&event, rules, time.Date(2026, 10, 21, 15, 0, 0, 0, loc), loc)
	if event.LeaveLabel != "Leave by 4:10 PM" || event.LeaveCountdown != "1h 10m" || event.TravelLabel != "20 min each way to Lakeview" {
		t.Fatalf("travel card labels = %q, %q, %q", event.LeaveLabel, event.LeaveCountdown, event.TravelLabel)
	}

	pickup := Event{
		Title: "Ayla Vv Studio",
		Start: time.Date(2026, time.October, 21, 16, 30, 0, 0, loc).Format(time.RFC3339),
		End:   time.Date(2026, time.October, 21, 17, 30, 0, 0, loc).Format(time.RFC3339),
	}
	now := time.Date(2026, time.October, 21, 17, 0, 0, 0, loc)
	addTravelEstimate(&pickup, rules, now, loc)
	if !pickup.LeaveForPickup || pickup.LeaveLabel != "Leave for pickup by 5:10 PM" || pickup.LeaveCountdown != "10m" {
		t.Fatalf("pickup estimate = %+v", pickup)
	}

	future := Event{
		Title: "Future appointment",
		Start: now.Add(time.Hour).Format(time.RFC3339),
		End:   now.Add(2 * time.Hour).Format(time.RFC3339),
	}
	active, remaining := nextEvent([]Event{future, pickup}, now)
	if active == nil || active.Title != pickup.Title || active.Status != "IN PROGRESS" || active.Countdown != "NOW" {
		t.Fatalf("next event = %+v, want active pickup", active)
	}
	if len(remaining) != 1 || remaining[0].Title != future.Title {
		t.Fatalf("remaining events = %+v, want future appointment", remaining)
	}
}

func TestFormatLeaveCountdown(t *testing.T) {
	for _, tc := range []struct {
		remaining time.Duration
		want      string
	}{
		{-time.Second, "NOW"},
		{0, "NOW"},
		{time.Second, "1s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m"},
		{70 * time.Minute, "1h 10m"},
		{25 * time.Hour, "1d 1h"},
	} {
		if got := formatLeaveCountdown(tc.remaining); got != tc.want {
			t.Errorf("formatLeaveCountdown(%s) = %q, want %q", tc.remaining, got, tc.want)
		}
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
	if landing.Code != http.StatusOK || !strings.Contains(landing.Body.String(), "Sign in to Family HQ") || !strings.Contains(landing.Body.String(), "Pair a display with your phone") || strings.Contains(landing.Body.String(), "Family tasks") {
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
