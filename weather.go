package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Weather is fetched only by the server. Never render or log the API key.
// OpenWeather's free 5-day forecast is supplied in three-hour steps.
const weatherAPIBase = "https://api.openweathermap.org"

type weatherSlot struct {
	At                                                        time.Time
	Day, Hour, Icon, Condition, Temp, Feels, Rain, Wind, Wear string
	TempValue, FeelsValue, WindValue, PopValue                float64
	RainValue, SnowValue                                      float64
}
type weatherView struct {
	Badge, Note, Message, Location, Air string
	Current                             weatherSlot
	Slots                               []weatherSlot
	Forecast                            []weatherSlot
}
type tomorrowWeather struct {
	Available bool
	High      string
	Low       string
	Condition string
	Rain      string
	Icon      string
}
type weatherFeed struct {
	key      string
	lat, lon float64
	place    string
	tz       *time.Location
	base     string
	client   *http.Client
	mu       sync.Mutex
	next     time.Time
	value    weatherView
	hasGood  bool
}

type owConditions struct {
	DT   int64 `json:"dt"`
	Main struct {
		Temp  float64 `json:"temp"`
		Feels float64 `json:"feels_like"`
	} `json:"main"`
	Weather []struct {
		Main        string `json:"main"`
		Description string `json:"description"`
	} `json:"weather"`
	Wind struct {
		Speed float64 `json:"speed"`
		Gust  float64 `json:"gust"`
	} `json:"wind"`
	Pop  float64 `json:"pop"`
	Rain struct {
		ThreeHour float64 `json:"3h"`
		OneHour   float64 `json:"1h"`
	} `json:"rain"`
	Snow struct {
		ThreeHour float64 `json:"3h"`
		OneHour   float64 `json:"1h"`
	} `json:"snow"`
}
type owForecast struct {
	List []owConditions `json:"list"`
}
type owAir struct {
	List []struct {
		Main struct {
			AQI int `json:"aqi"`
		} `json:"main"`
	} `json:"list"`
}

func newWeatherFeed(tz *time.Location) (*weatherFeed, error) {
	// Reuse the name already configured for Play Outside; generic alias is optional.
	key := strings.TrimSpace(os.Getenv("PLAY_OUTSIDE_OPEN_WEATHER_API_KEY"))
	if key == "" {
		key = strings.TrimSpace(os.Getenv("OPEN_WEATHER_API_KEY"))
	}
	if key == "" {
		return nil, nil
	}
	lat, lon := 40.6936, -89.5890 // Peoria, IL city center; override in .env.
	if s := strings.TrimSpace(os.Getenv("WEATHER_LATITUDE")); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < -90 || v > 90 {
			return nil, errors.New("WEATHER_LATITUDE must be a number from -90 to 90; for Peoria use 40.6936 (edit .env and restart just serve)")
		}
		lat = v
	}
	if s := strings.TrimSpace(os.Getenv("WEATHER_LONGITUDE")); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < -180 || v > 180 {
			return nil, errors.New("WEATHER_LONGITUDE must be a number from -180 to 180; for Peoria use -89.5890 (edit .env and restart just serve)")
		}
		lon = v
	}
	place := strings.TrimSpace(os.Getenv("WEATHER_LOCATION_NAME"))
	if place == "" {
		place = "Peoria area"
	}
	return &weatherFeed{key: key, lat: lat, lon: lon, place: place, tz: tz, base: weatherAPIBase, client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (wf *weatherFeed) get(ctx context.Context, path string, out any) error {
	q := url.Values{"lat": {strconv.FormatFloat(wf.lat, 'f', 5, 64)}, "lon": {strconv.FormatFloat(wf.lon, 'f', 5, 64)}, "appid": {wf.key}, "units": {"imperial"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wf.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return errors.New("could not prepare weather request")
	}
	resp, err := tracedRequest(ctx, "weather.fetch", wf.client, req)
	if err != nil {
		return errors.New("weather service unreachable; check your connection and retry")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return errors.New("OpenWeatherMap rejected the API key; verify PLAY_OUTSIDE_OPEN_WEATHER_API_KEY in .env (new keys can take time to activate)")
	case http.StatusTooManyRequests:
		return errors.New("OpenWeatherMap rate limit reached; cached weather will be kept if available")
	default:
		return fmt.Errorf("OpenWeatherMap returned HTTP %d; check the provider status and coordinates", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return errors.New("OpenWeatherMap returned an unreadable weather response")
	}
	return nil
}

func (wf *weatherFeed) fetch(now time.Time) (weatherView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	return wf.fetchContext(ctx, now)
}

func (wf *weatherFeed) fetchContext(ctx context.Context, now time.Time) (weatherView, error) {
	var forecast owForecast
	if err := wf.get(ctx, "/data/2.5/forecast", &forecast); err != nil {
		return weatherView{}, err
	}
	if len(forecast.List) == 0 {
		return weatherView{}, errors.New("OpenWeatherMap returned an empty forecast")
	}
	var current owConditions
	if err := wf.get(ctx, "/data/2.5/weather", &current); err != nil {
		return weatherView{}, err
	}
	view := weatherView{Badge: "LIVE WEATHER", Location: wf.place, Note: "3-hour forecast · updated " + now.In(wf.tz).Format("3:04 PM")}
	// The current air pollution endpoint uses an index from 1 to 5, NOT the US AQI scale.
	var air owAir
	if err := wf.get(ctx, "/data/2.5/air_pollution", &air); err == nil && len(air.List) > 0 {
		if aqi := air.List[0].Main.AQI; aqi >= 1 && aqi <= 5 {
			view.Air = fmt.Sprintf("Air quality %d/5", aqi)
			if aqi >= 4 {
				view.Message = "Air quality is poor; consider limiting strenuous outdoor activity."
			}
		}
	}
	view.Current = makeWeatherSlot(current, "Today", "Now", false)
	view.Current.At = now
	view.Slots = append(view.Slots, view.Current)
	for _, p := range forecast.List {
		at := time.Unix(p.DT, 0).In(wf.tz)
		if !at.After(now) {
			continue
		}
		day := "Today"
		if at.YearDay() != now.In(wf.tz).YearDay() || at.Year() != now.In(wf.tz).Year() {
			day = at.Format("Mon Jan 2")
		}
		slot := makeWeatherSlot(p, day, at.Format("3 PM"), true)
		slot.At = at
		view.Forecast = append(view.Forecast, slot)
		if at.Hour() >= 6 && at.Hour() <= 21 && len(view.Slots) < 6 {
			view.Slots = append(view.Slots, slot)
		}
	}
	return view, nil
}

func weatherForTomorrow(weather weatherView, tomorrow time.Time, loc *time.Location) tomorrowWeather {
	var slots []weatherSlot
	for _, slot := range weather.Forecast {
		if midnight(slot.At.In(loc), loc).Equal(midnight(tomorrow, loc)) {
			slots = append(slots, slot)
		}
	}
	if len(slots) == 0 {
		return tomorrowWeather{}
	}
	high, low, rain := slots[0].TempValue, slots[0].TempValue, 0.0
	condition := slots[0]
	conditionDistance := 24 * time.Hour
	for _, slot := range slots {
		if slot.TempValue > high {
			high = slot.TempValue
		}
		if slot.TempValue < low {
			low = slot.TempValue
		}
		if slot.PopValue > rain {
			rain = slot.PopValue
		}
		daytimeDistance := time.Duration(math.Abs(float64(slot.At.In(loc).Hour()-15))) * time.Hour
		if daytimeDistance < conditionDistance {
			condition, conditionDistance = slot, daytimeDistance
		}
	}
	return tomorrowWeather{
		Available: true,
		High:      fmt.Sprintf("%.0f°", high),
		Low:       fmt.Sprintf("%.0f°", low),
		Condition: condition.Condition,
		Rain:      fmt.Sprintf("%d%% rain", int(math.Round(math.Min(1, math.Max(0, rain))*100))),
		Icon:      condition.Icon,
	}
}

func addEventWeather(event *Event, weather weatherView, now time.Time, loc *time.Location) {
	if event == nil {
		return
	}
	start, err := time.Parse(time.RFC3339, event.Start)
	if err != nil {
		return
	}
	if event.AllDay {
		start = time.Date(start.In(loc).Year(), start.In(loc).Month(), start.In(loc).Day(), 0, 0, 0, 0, loc)
	}
	end, err := time.Parse(time.RFC3339, event.End)
	if err != nil || end.Before(start) {
		end = start
	}
	travel := time.Duration(event.TravelMinutes) * time.Minute
	from, until := start.Add(-travel), end.Add(travel)
	if from.Before(now) {
		from = now
	}
	if until.Before(from) {
		return
	}
	candidates := make([]weatherSlot, 0, len(weather.Forecast)+1)
	if !weather.Current.At.IsZero() {
		candidates = append(candidates, weather.Current)
	}
	candidates = append(candidates, weather.Forecast...)
	startIndex, endIndex := -1, -1
	var startDistance, endDistance time.Duration
	for i := range candidates {
		toStart := candidates[i].At.Sub(from)
		if toStart < 0 {
			toStart = -toStart
		}
		toEnd := candidates[i].At.Sub(until)
		if toEnd < 0 {
			toEnd = -toEnd
		}
		if startIndex < 0 || toStart < startDistance {
			startIndex, startDistance = i, toStart
		}
		if endIndex < 0 || toEnd < endDistance {
			endIndex, endDistance = i, toEnd
		}
	}
	// Include the nearest forecast at both ends of the outing plus every
	// forecast inside it. A three-hour forecast is useful up to two hours away;
	// beyond that, omit the advice instead of implying full coverage.
	if startIndex < 0 || endIndex < 0 || startDistance > 2*time.Hour || endDistance > 2*time.Hour {
		return
	}
	selected := make(map[int]bool)
	selected[startIndex], selected[endIndex] = true, true
	for i := range candidates {
		if !candidates[i].At.Before(from) && !candidates[i].At.After(until) {
			selected[i] = true
		}
	}
	var highTemp, lowestFeels, strongestWind, rainChance, rainAmount, snowAmount float64
	first := true
	for i, slot := range candidates {
		if !selected[i] {
			continue
		}
		if first {
			highTemp, lowestFeels = slot.TempValue, slot.FeelsValue
			first = false
		} else {
			highTemp = math.Max(highTemp, slot.TempValue)
			lowestFeels = math.Min(lowestFeels, slot.FeelsValue)
		}
		strongestWind = math.Max(strongestWind, slot.WindValue)
		rainChance = math.Max(rainChance, slot.PopValue)
		rainAmount = math.Max(rainAmount, slot.RainValue)
		snowAmount = math.Max(snowAmount, slot.SnowValue)
	}
	if first {
		return
	}
	event.WeatherAt = "WHILE OUT"
	event.WeatherTemp = fmt.Sprintf("%.0f° high · feels %.0f° low", highTemp, lowestFeels)
	event.WeatherWear = clothing(lowestFeels, strongestWind, rainChance, rainAmount, snowAmount)
	switch {
	case snowAmount > 0:
		event.WeatherIcon, event.WeatherCondition = "snow", "Snow possible"
	case rainChance >= 0.35 || rainAmount > 0.5:
		event.WeatherIcon, event.WeatherCondition = "rain", "Rain possible"
	case strongestWind >= 20:
		event.WeatherIcon, event.WeatherCondition = "wind", "Windy"
	default:
		for i, slot := range candidates {
			if selected[i] {
				event.WeatherIcon = slot.Icon
				if event.WeatherIcon == "" {
					event.WeatherIcon = weatherIcon(slot.Condition)
				}
				event.WeatherCondition = slot.Condition
				break
			}
		}
	}
}

func weatherIcon(s string) string {
	// Return an SVG symbol ID, not an emoji. Older Android photo-frame fonts
	// frequently show empty square glyphs for weather emoji.
	switch strings.ToLower(s) {
	case "thunderstorm":
		return "storm"
	case "drizzle", "rain":
		return "rain"
	case "snow":
		return "snow"
	case "clear":
		return "sun"
	case "clouds":
		return "cloud"
	default:
		return "haze"
	}
}

func clothing(feels, wind, pop, rain, snow float64) string {
	var base string
	switch {
	case feels < -10:
		base = "Dangerous cold; stay inside"
	case feels < 10:
		base = "Insulated coat, hat & gloves"
	case feels < 32:
		base = "Heavy coat, hat & gloves"
	case feels < 44:
		base = "Warm coat & layers"
	case feels < 54:
		base = "Jacket & layers"
	case feels < 64:
		base = "Light jacket"
	case feels < 74:
		base = "Long sleeves or tee"
	case feels < 88:
		base = "Light, breathable clothes"
	case feels < 105:
		base = "Light clothes; shade & water"
	default:
		base = "Extreme heat; limit outdoor time"
	}
	if snow > 0 {
		base += " · snow boots"
	} else if pop >= 0.55 || rain > 0.5 {
		base += " · rain shell"
	}
	if wind >= 20 {
		base += " · wind layer"
	}
	return base
}

func makeWeatherSlot(c owConditions, day, hour string, forecast bool) weatherSlot {
	name, description := "Weather", "Conditions unavailable"
	if len(c.Weather) > 0 {
		name = c.Weather[0].Main
		description = c.Weather[0].Description
	}
	rain := "—"
	if forecast {
		rain = fmt.Sprintf("%.0f%%", math.Min(100, math.Max(0, c.Pop*100)))
	}
	rainAmount := math.Max(c.Rain.OneHour, c.Rain.ThreeHour)
	snowAmount := math.Max(c.Snow.OneHour, c.Snow.ThreeHour)
	if strings.EqualFold(name, "Rain") || strings.EqualFold(name, "Drizzle") || strings.EqualFold(name, "Thunderstorm") {
		rainAmount = math.Max(rainAmount, 1)
	}
	if strings.EqualFold(name, "Snow") {
		snowAmount = math.Max(snowAmount, 1)
	}
	wind := math.Max(c.Wind.Speed, c.Wind.Gust)
	return weatherSlot{Day: day, Hour: hour, Icon: weatherIcon(name), Condition: description, Temp: fmt.Sprintf("%.0f°", c.Main.Temp), Feels: fmt.Sprintf("%.0f°", c.Main.Feels), Rain: rain, Wind: fmt.Sprintf("%.0f mph", c.Wind.Speed), Wear: clothing(c.Main.Feels, wind, c.Pop, rainAmount, snowAmount), TempValue: c.Main.Temp, FeelsValue: c.Main.Feels, WindValue: wind, PopValue: c.Pop, RainValue: rainAmount, SnowValue: snowAmount}
}

func demoWeather() weatherView {
	// Intentionally no invented temperatures. The layout has a setup prompt.
	return weatherView{Badge: "WEATHER NOT CONNECTED", Location: "Peoria area", Note: "Connect Play Outside's OpenWeatherMap key", Message: "Copy PLAY_OUTSIDE_OPEN_WEATHER_API_KEY into your local .env and restart just serve. The timeline will then show real three-hour forecasts."}
}

func (wf *weatherFeed) snapshot(now time.Time) weatherView {
	return wf.snapshotContext(context.Background(), now)
}

func (wf *weatherFeed) snapshotContext(ctx context.Context, now time.Time) weatherView {
	if wf == nil {
		return demoWeather()
	}
	wf.mu.Lock()
	defer wf.mu.Unlock()
	if now.Before(wf.next) {
		return wf.value
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	view, err := wf.fetchContext(fetchCtx, now)
	cancel()
	if err != nil {
		wf.next = now.Add(2 * time.Minute)
		if wf.hasGood {
			wf.value.Message = "Weather temporarily unavailable; showing the last successful forecast."
			wf.value.Badge = "CACHED WEATHER"
			return wf.value
		}
		wf.value = weatherView{Badge: "WEATHER UNAVAILABLE", Location: wf.place, Note: "Unable to load forecast", Message: err.Error()}
		return wf.value
	}
	wf.value = view
	wf.hasGood = true
	wf.next = now.Add(20 * time.Minute)
	return wf.value
}
