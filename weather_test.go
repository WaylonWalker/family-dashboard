package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClothingAdvice(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		feels, wind, pop, rain, snow float64
		contains                     string
	}{
		{"cold", 24, 7, 0, 0, 0, "Heavy coat"},
		{"cool", 47, 0, 0, 0, 0, "Jacket & layers"},
		{"rain", 64, 8, 0.8, 0, 0, "rain shell"},
		{"wind", 65, 24, 0, 0, 0, "wind layer"},
		{"snow", 20, 5, 0, 0, 1, "snow boots"},
		{"heat", 92, 5, 0, 0, 0, "shade & water"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := clothing(tc.feels, tc.wind, tc.pop, tc.rain, tc.snow)
			if !strings.Contains(out, tc.contains) {
				t.Fatalf("got %q, expected %q", out, tc.contains)
			}
		})
	}
}

func TestWeatherForecastCachedAndNoKeyInHTML(t *testing.T) {
	loc := chicago(t)
	now := dateIn(t, "2026-10-21 10:00")
	var calls atomic.Int32
	key := "sensitive-api-key-123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("appid") != key || r.URL.Query().Get("units") != "imperial" {
			t.Errorf("unexpected query params")
			http.Error(w, "bad params", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/data/2.5/forecast":
			type entry map[string]any
			rows := []entry{}
			for _, h := range []int{12, 15, 18, 21} {
				dt := time.Date(2026, 10, 21, h, 0, 0, 0, loc).Unix()
				rows = append(rows, entry{"dt": dt, "main": map[string]any{"temp": 49.3, "feels_like": 41.5}, "wind": map[string]any{"speed": 21.4}, "pop": 0.8, "weather": []map[string]any{{"main": "Rain", "description": "light rain"}}})
			}
			json.NewEncoder(w).Encode(map[string]any{"list": rows})
		case "/data/2.5/weather":
			json.NewEncoder(w).Encode(map[string]any{"dt": now.Unix(), "main": map[string]any{"temp": 53, "feels_like": 49.4}, "wind": map[string]any{"speed": 5}, "weather": []map[string]any{{"main": "Clouds", "description": "cloudy"}}})
		case "/data/2.5/air_pollution":
			json.NewEncoder(w).Encode(map[string]any{"list": []map[string]any{{"main": map[string]any{"aqi": 4}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	wf := &weatherFeed{key: key, lat: 40.7, lon: -89.5, place: "Peoria area", tz: loc, base: server.URL, client: server.Client()}
	view := wf.snapshot(now)
	if view.Badge != "LIVE WEATHER" || len(view.Slots) != 5 || len(view.Forecast) != 4 {
		t.Fatalf("unexpected forecast: %+v", view)
	}
	if view.Current.Temp != "53°" || view.Current.Condition != "cloudy" || view.Current.Feels != "49°" {
		t.Fatalf("incorrect current conditions %+v", view.Current)
	}
	if view.Air != "Air quality 4/5" || !strings.Contains(view.Message, "Air quality") {
		t.Fatalf("air data: %+v", view)
	}
	if view.Slots[1].Temp != "49°" || view.Slots[1].Feels != "42°" || view.Slots[1].Rain != "80%" || view.Slots[1].Wind != "21 mph" {
		t.Fatalf("incorrect forecast values %+v", view.Slots[1])
	}
	if !strings.Contains(view.Slots[1].Wear, "rain shell") || !strings.Contains(view.Slots[1].Wear, "wind layer") {
		t.Fatalf("clothing advice %+v", view.Slots[1])
	}
	if calls.Load() != 3 {
		t.Errorf("first fetch made %d requests", calls.Load())
	}
	wf.snapshot(now.Add(time.Minute))
	if calls.Load() != 3 {
		t.Errorf("cached fetch made additional requests")
	}
	// Inject the cached view into a real rendered page; tokens must stay server-side.
	path := filepath.Join(t.TempDir(), "tasks.json")
	t.Setenv("PLAY_OUTSIDE_OPEN_WEATHER_API_KEY", "")
	t.Setenv("OPEN_WEATHER_API_KEY", "")
	a, err := newApp(path)
	if err != nil {
		t.Fatal(err)
	}
	a.weather = wf
	a.calendar = &calendarFeed{
		tz:      loc,
		fetched: now,
		next:    now.Add(time.Minute),
		events: []calendarItem{{
			uid: "family-dinner", title: "Family dinner",
			start: dateIn(t, "2026-10-21 12:30"), end: dateIn(t, "2026-10-21 13:30"),
		}},
	}
	a.now = func() time.Time { return now }
	rr := httptest.NewRecorder()
	a.routes().ServeHTTP(rr, httptest.NewRequest("GET", "/dashboard", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "What to wear") || !strings.Contains(rr.Body.String(), "80%") {
		t.Fatalf("page weather missing: %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), key) {
		t.Fatal("API key leaked into response")
	}
	if !strings.Contains(rr.Body.String(), `class="next-up-weather"`) || !strings.Contains(rr.Body.String(), "Rain possible") || !strings.Contains(rr.Body.String(), "49° high · feels 42° low") || !strings.Contains(rr.Body.String(), "Warm coat &amp; layers · rain shell · wind layer") {
		t.Fatal("next-up event weather and clothing advice missing from rendered card")
	}
}

func TestAddEventWeatherCoversOutingAndReturnTrip(t *testing.T) {
	loc := chicago(t)
	now := dateIn(t, "2026-10-22 09:00")
	forecast := []weatherSlot{
		{At: dateIn(t, "2026-10-22 10:00"), TempValue: 68, FeelsValue: 65, Condition: "clear sky"},
		{At: dateIn(t, "2026-10-22 13:00"), TempValue: 70, FeelsValue: 67, Condition: "cloudy"},
		{At: dateIn(t, "2026-10-22 16:00"), TempValue: 58, FeelsValue: 54, Condition: "cloudy"},
		{At: dateIn(t, "2026-10-22 19:00"), TempValue: 54, FeelsValue: 48, PopValue: .8, RainValue: 1, WindValue: 24, Condition: "rain"},
		{At: dateIn(t, "2026-10-22 22:00"), TempValue: 52, FeelsValue: 44, PopValue: .7, RainValue: 1, WindValue: 21, Condition: "rain"},
	}
	weather := weatherView{Current: weatherSlot{At: now, TempValue: 75, FeelsValue: 70}, Forecast: forecast}
	event := Event{Start: dateIn(t, "2026-10-22 16:30").Format(time.RFC3339), End: dateIn(t, "2026-10-22 18:00").Format(time.RFC3339), TravelMinutes: 20}
	addEventWeather(&event, weather, now, loc)
	if event.WeatherAt != "WHILE OUT" || event.WeatherTemp != "58° high · feels 48° low" || event.WeatherCondition != "Rain possible" || event.WeatherWear != "Jacket & layers · rain shell · wind layer" {
		t.Fatalf("timed event weather = %+v", event)
	}
	allDay := Event{Start: dateIn(t, "2026-10-22 00:00").Format(time.RFC3339), End: dateIn(t, "2026-10-23 00:00").Format(time.RFC3339), AllDay: true}
	addEventWeather(&allDay, weather, now, loc)
	if allDay.WeatherTemp != "75° high · feels 44° low" || allDay.WeatherWear != "Jacket & layers · rain shell · wind layer" {
		t.Fatalf("all-day event weather = %+v", allDay)
	}
}

func TestWeatherFailureNoSecretLeakAndStaleCache(t *testing.T) {
	key := "do-not-leak-openweather-token"
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "invalid "+key, 401)
			return
		}
		switch r.URL.Path {
		case "/data/2.5/forecast":
			fmt.Fprintf(w, `{"list":[{"dt":1792602000,"main":{"temp":40,"feels_like":33},"weather":[{"main":"Clouds"}],"wind":{"speed":8}}]}`)
		case "/data/2.5/weather":
			fmt.Fprint(w, `{"main":{"temp":42,"feels_like":38},"weather":[{"main":"Clouds"}],"wind":{"speed":5}}`)
		case "/data/2.5/air_pollution":
			fmt.Fprint(w, `{"list":[]}`)
		}
	}))
	defer server.Close()
	now := dateIn(t, "2026-10-21 10:00")
	wf := &weatherFeed{key: key, lat: 40.7, lon: -89.5, place: "Peoria area", tz: chicago(t), base: server.URL, client: server.Client()}
	first := wf.snapshot(now)
	if first.Badge != "LIVE WEATHER" {
		t.Fatalf("first %+v", first)
	}
	fail.Store(true)
	stale := wf.snapshot(now.Add(21 * time.Minute))
	if stale.Badge != "CACHED WEATHER" || len(stale.Slots) != len(first.Slots) {
		t.Fatalf("stale %+v", stale)
	}
	if strings.Contains(stale.Message, key) {
		t.Fatal("key leaked from provider response")
	}
	// An initial failure should say how to fix the key, not display it.
	second := &weatherFeed{key: key, lat: 40.7, lon: -89.5, place: "Peoria", tz: chicago(t), base: server.URL, client: server.Client()}
	bad := second.snapshot(now)
	if bad.Badge != "WEATHER UNAVAILABLE" || !strings.Contains(bad.Message, "rejected") || strings.Contains(bad.Message, key) {
		t.Fatalf("bad forecast error %+v", bad)
	}
}

func TestWeatherConfig(t *testing.T) {
	t.Setenv("PLAY_OUTSIDE_OPEN_WEATHER_API_KEY", "")
	t.Setenv("OPEN_WEATHER_API_KEY", "")
	wf, err := newWeatherFeed(chicago(t))
	if err != nil || wf != nil {
		t.Fatalf("expected unconfigured weather; got %v %v", wf, err)
	}
	if demoWeather().Badge != "WEATHER NOT CONNECTED" {
		t.Fatal("no placeholder warning")
	}
	t.Setenv("PLAY_OUTSIDE_OPEN_WEATHER_API_KEY", "test-secret")
	t.Setenv("WEATHER_LATITUDE", "forty")
	_, err = newWeatherFeed(chicago(t))
	if err == nil || !strings.Contains(err.Error(), "WEATHER_LATITUDE") {
		t.Fatalf("bad location validation %v", err)
	}
	t.Setenv("WEATHER_LATITUDE", "40.6936")
	t.Setenv("WEATHER_LONGITUDE", "-89.5890")
	wf, err = newWeatherFeed(chicago(t))
	if err != nil || wf == nil || wf.key != "test-secret" {
		t.Fatalf("expected configured weather: %v", err)
	}
}

func TestWeatherAlias(t *testing.T) {
	t.Setenv("PLAY_OUTSIDE_OPEN_WEATHER_API_KEY", "")
	t.Setenv("OPEN_WEATHER_API_KEY", "alias-key")
	wf, err := newWeatherFeed(chicago(t))
	if err != nil || wf == nil || wf.key != "alias-key" {
		t.Fatalf("alias failed %v", err)
	}
}
