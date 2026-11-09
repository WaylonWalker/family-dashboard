package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestFederalHolidaySchedule2026(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"new-years-day":        "2026-01-01",
		"mlk-day":              "2026-01-19",
		"washingtons-birthday": "2026-02-16",
		"memorial-day":         "2026-05-25",
		"juneteenth":           "2026-06-19",
		"independence-day":     "2026-07-03",
		"labor-day":            "2026-09-07",
		"columbus-day":         "2026-10-12",
		"veterans-day":         "2026-11-11",
		"thanksgiving-day":     "2026-11-26",
		"christmas-day":        "2026-12-25",
	}

	schedule := federalHolidaySchedule(2026, loc)
	if len(schedule) != len(want) {
		t.Fatalf("federalHolidaySchedule(2026) returned %d holidays, want %d", len(schedule), len(want))
	}
	for _, holiday := range schedule {
		date, ok := want[holiday.Key]
		if !ok {
			t.Errorf("unexpected holiday key %q", holiday.Key)
			continue
		}
		delete(want, holiday.Key)
		if got := holiday.Date.In(loc).Format("2006-01-02"); got != date {
			t.Errorf("%s date = %s, want %s", holiday.Key, got, date)
		}
	}
	for key := range want {
		t.Errorf("federal holiday schedule is missing %s", key)
	}
}

func TestFederalHolidaySchedule2027ObservesWeekendDates(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"juneteenth":       "2027-06-18",
		"independence-day": "2027-07-05",
		"christmas-day":    "2027-12-24",
	}
	for _, holiday := range federalHolidaySchedule(2027, loc) {
		if date, ok := want[holiday.Key]; ok {
			if got := holiday.Date.In(loc).Format("2006-01-02"); got != date {
				t.Errorf("%s observed date = %s, want %s", holiday.Key, got, date)
			}
			delete(want, holiday.Key)
		}
	}
	for key := range want {
		t.Errorf("federal holiday schedule is missing observed-date check for %s", key)
	}

	newYearEve := time.Date(2021, time.December, 31, 12, 0, 0, 0, loc)
	newYearsObserved := false
	for _, holiday := range federalHolidaySchedule(2022, loc) {
		if holiday.Key == "new-years-day" {
			newYearsObserved = true
			if got := holiday.Date.In(loc).Format("2006-01-02"); got != "2021-12-31" {
				t.Errorf("2022 New Year's Day observed date = %s, want 2021-12-31", got)
			}
		}
	}
	if !newYearsObserved {
		t.Fatal("2022 schedule is missing New Year's Day")
	}
	if got := federalHolidayForDate(newYearEve, loc); got == nil || got.Key != "new-years-day" {
		t.Errorf("federalHolidayForDate(%s) = %#v, want New Year's Day observed from 2022", newYearEve.Format("2006-01-02"), got)
	}
}

func TestFederalHolidayUsesDashboardTimezone(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	beforeLocalNewYear := time.Date(2026, time.January, 1, 5, 59, 0, 0, time.UTC)
	if got := federalHolidayForDate(beforeLocalNewYear, loc); got != nil {
		t.Errorf("federalHolidayForDate before Chicago midnight = %q, want none", got.Title)
	}
	atLocalNewYear := time.Date(2026, time.January, 1, 6, 0, 0, 0, time.UTC)
	if got := federalHolidayForDate(atLocalNewYear, loc); got == nil || got.Key != "new-years-day" {
		t.Errorf("federalHolidayForDate at Chicago midnight = %#v, want New Year's Day", got)
	}
}

func TestHolidayPreviewOnlyOverridesOnLocalDevRequests(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 9, 12, 0, 0, 0, loc)
	a := &app{devMode: true, tz: loc, now: func() time.Time { return now }}
	page := Page{BirthdayMessage: "Happy birthday, Wyatt!"}
	request := httptest.NewRequest("GET", "/?holiday-preview=christmas-day", nil)
	request.RemoteAddr = "127.0.0.1:5678"
	a.holidayPreview(request, &page, false, now)
	if page.BirthdayMessage != "" {
		t.Errorf("forced holiday left birthday message %q", page.BirthdayMessage)
	}
	if page.Holiday == nil || page.Holiday.Key != "christmas-day" {
		t.Errorf("forced holiday = %#v, want Christmas Day", page.Holiday)
	}
	if len(page.HolidayOptions) != 11 {
		t.Errorf("local preview offered %d holidays, want 11", len(page.HolidayOptions))
	}
	if page.HolidayPreview != "christmas-day" {
		t.Errorf("selected preview = %q, want christmas-day", page.HolidayPreview)
	}

	remotePage := Page{BirthdayMessage: "Happy birthday, Wyatt!"}
	remoteRequest := httptest.NewRequest("GET", "/?holiday-preview=christmas-day&holiday-step=next", nil)
	remoteRequest.RemoteAddr = "192.0.2.4:5678"
	a.holidayPreview(remoteRequest, &remotePage, false, now)
	if remotePage.Holiday != nil || remotePage.BirthdayMessage == "" || len(remotePage.HolidayOptions) != 0 {
		t.Errorf("remote request changed preview content: holiday=%#v birthday=%q options=%d", remotePage.Holiday, remotePage.BirthdayMessage, len(remotePage.HolidayOptions))
	}

	productionApp := &app{devMode: false, tz: loc, now: func() time.Time { return now }}
	productionPage := Page{BirthdayMessage: "Happy birthday, Wyatt!"}
	localRequest := httptest.NewRequest("GET", "/?holiday-preview=auto&holiday-step=next", nil)
	localRequest.RemoteAddr = "127.0.0.1:5678"
	productionApp.holidayPreview(localRequest, &productionPage, false, now)
	if productionPage.Holiday != nil || productionPage.BirthdayMessage == "" || len(productionPage.HolidayOptions) != 0 {
		t.Errorf("production request changed preview content: holiday=%#v birthday=%q options=%d", productionPage.Holiday, productionPage.BirthdayMessage, len(productionPage.HolidayOptions))
	}
}

func TestHolidayPreviewStepsAndWraps(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 9, 12, 0, 0, 0, loc)
	a := &app{devMode: true, tz: loc, now: func() time.Time { return now }}
	for _, tc := range []struct {
		name  string
		query string
		want  string
		focus string
	}{
		{name: "next holiday", query: "holiday-preview=memorial-day&holiday-step=next", want: "juneteenth", focus: "next"},
		{name: "previous holiday", query: "holiday-preview=memorial-day&holiday-step=previous", want: "washingtons-birthday", focus: "previous"},
		{name: "next wraps after Christmas", query: "holiday-preview=christmas-day&holiday-step=next", want: "new-years-day", focus: "next"},
		{name: "previous wraps before New Year's Day", query: "holiday-preview=new-years-day&holiday-step=previous", want: "christmas-day", focus: "previous"},
		{name: "auto starts at first holiday", query: "holiday-preview=auto&holiday-step=next", want: "new-years-day", focus: "next"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := Page{BirthdayMessage: "Happy birthday, Wyatt!"}
			request := httptest.NewRequest("GET", "/?"+tc.query, nil)
			request.RemoteAddr = "127.0.0.1:5678"
			a.holidayPreview(request, &page, false, now)
			if page.Holiday == nil || page.Holiday.Key != tc.want {
				t.Errorf("stepped holiday = %#v, want %q", page.Holiday, tc.want)
			}
			if page.HolidayPreview != tc.want || page.BirthdayMessage != "" {
				t.Errorf("step selected %q with birthday %q, want %q and no birthday", page.HolidayPreview, page.BirthdayMessage, tc.want)
			}
			if !page.HolidayPreviewSettingsOpen || page.HolidayPreviewFocus != tc.focus {
				t.Errorf("step did not keep the preview drawer open with focus on its control: open=%t focus=%q", page.HolidayPreviewSettingsOpen, page.HolidayPreviewFocus)
			}
		})
	}
}

func TestHolidayPreviewCanClearCelebration(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 9, 12, 0, 0, 0, loc)
	a := &app{devMode: true, tz: loc, now: func() time.Time { return now }}
	page := Page{BirthdayMessage: "Happy birthday, Wyatt!", Holiday: federalHolidayForDate(now, loc)}
	request := httptest.NewRequest("GET", "/?holiday-preview=none", nil)
	request.RemoteAddr = "[::1]:5678"
	a.holidayPreview(request, &page, false, now)
	if page.Holiday != nil || page.BirthdayMessage != "" || page.HolidayPreview != "none" {
		t.Errorf("none preview = holiday %#v, birthday %q, selected %q", page.Holiday, page.BirthdayMessage, page.HolidayPreview)
	}
}
