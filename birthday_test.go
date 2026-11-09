package main

import (
	"testing"
	"time"
)

func TestBirthdayMessageMatchesEachAnnualBirthday(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	birthdays := []struct {
		name    string
		month   time.Month
		day     int
		message string
	}{
		{name: "Wyatt", month: time.November, day: 9, message: "Happy birthday, Wyatt!"},
		{name: "Ayla", month: time.October, day: 18, message: "Happy birthday, Ayla!"},
		{name: "Rhiannon", month: time.March, day: 4, message: "Happy birthday, Rhiannon!"},
		{name: "Waylon", month: time.March, day: 26, message: "Happy birthday, Waylon!"},
	}

	for _, birthday := range birthdays {
		t.Run(birthday.name, func(t *testing.T) {
			for _, year := range []int{2026, 2031} {
				at := time.Date(year, birthday.month, birthday.day, 12, 0, 0, 0, loc)
				if got := birthdayMessage(at, loc); got != birthday.message {
					t.Errorf("birthdayMessage(%s) = %q, want %q", at.Format("2006-01-02"), got, birthday.message)
				}
			}

			birthdayDay := time.Date(2026, birthday.month, birthday.day, 12, 0, 0, 0, loc)
			for _, day := range []time.Time{birthdayDay.AddDate(0, 0, -1), birthdayDay.AddDate(0, 0, 1)} {
				if got := birthdayMessage(day, loc); got != "" {
					t.Errorf("birthdayMessage(%s) = %q, want no birthday", day.Format("2006-01-02"), got)
				}
			}
		})
	}
}

func TestBirthdayMessageUsesChicagoDateAcrossUTCBoundaries(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "before Rhiannon's local midnight", at: time.Date(2026, time.March, 4, 5, 59, 0, 0, time.UTC), want: ""},
		{name: "Rhiannon at local midnight", at: time.Date(2026, time.March, 4, 6, 0, 0, 0, time.UTC), want: "Happy birthday, Rhiannon!"},
		{name: "before Waylon's daylight-time midnight", at: time.Date(2026, time.March, 26, 4, 59, 0, 0, time.UTC), want: ""},
		{name: "Waylon at daylight-time midnight", at: time.Date(2026, time.March, 26, 5, 0, 0, 0, time.UTC), want: "Happy birthday, Waylon!"},
		{name: "before Wyatt's local midnight", at: time.Date(2026, time.November, 9, 5, 59, 0, 0, time.UTC), want: ""},
		{name: "Wyatt at local midnight", at: time.Date(2026, time.November, 9, 6, 0, 0, 0, time.UTC), want: "Happy birthday, Wyatt!"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := birthdayMessage(tc.at, loc); got != tc.want {
				t.Errorf("birthdayMessage(%s) = %q, want %q", tc.at.Format(time.RFC3339), got, tc.want)
			}
		})
	}
}
