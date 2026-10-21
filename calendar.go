package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// calendarFeed is a read-only, server-side calendar subscription. In particular,
// no part of its URL should ever be included in rendered HTML or logged.
type calendarFeed struct {
	url     string
	client  *http.Client
	tz      *time.Location
	mu      sync.Mutex
	events  []calendarItem
	fetched time.Time
	next    time.Time
	problem string
}

type calendarItem struct {
	uid, title string
	start, end time.Time
	allDay     bool
}

type calProperty struct {
	value  string
	params map[string]string
}
type calRecord map[string][]calProperty

func newCalendarFeed(secretURL string, tz *time.Location) (*calendarFeed, error) {
	if secretURL == "" {
		return nil, nil
	}
	u, err := url.Parse(secretURL)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "calendar.google.com") || u.User != nil || u.Port() != "" || !strings.HasPrefix(u.Path, "/calendar/ical/") || !strings.HasSuffix(u.Path, "/basic.ics") {
		return nil, errors.New("GOOGLE_CALENDAR_ICAL_URL must be the private HTTPS iCal address from calendar.google.com")
	}
	return &calendarFeed{url: secretURL, tz: tz, client: &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 || req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Hostname(), "calendar.google.com") {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}}, nil
}

// snapshot synchronizes only when the interval expires. Failed requests are
// retried on a short cooldown; the previous known-good snapshot remains visible.
func (f *calendarFeed) snapshot(now time.Time) ([]calendarItem, string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !now.Before(f.next) {
		entries, err := f.download(now)
		if err != nil {
			f.problem = "Calendar unavailable: " + err.Error()
			f.next = now.Add(time.Minute)
		} else {
			f.events = entries
			f.fetched = now
			f.problem = ""
			f.next = now.Add(15 * time.Minute)
		}
	}
	result := append([]calendarItem(nil), f.events...)
	if f.problem != "" {
		if f.fetched.IsZero() {
			return nil, "Google Calendar · connection failed", f.problem
		}
		return result, "Google Calendar · last synced " + f.fetched.In(f.tz).Format("3:04 PM"), f.problem
	}
	return result, "Google Calendar · synced " + f.fetched.In(f.tz).Format("3:04 PM"), ""
}

func (f *calendarFeed) download(now time.Time) ([]calendarItem, error) {
	req, err := http.NewRequest(http.MethodGet, f.url, nil)
	if err != nil {
		return nil, errors.New("invalid calendar request")
	}
	req.Header.Set("Accept", "text/calendar")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, errors.New("calendar request failed")
	} // never include secret URL from net/http error
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("calendar returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20+1))
	if err != nil || len(body) > 4<<20 {
		return nil, errors.New("calendar response too large or unreadable")
	}
	return parseICalendar(body, now, f.tz)
}

func unfoldICal(raw []byte) ([]string, error) {
	if len(raw) == 0 || len(raw) > 4<<20 {
		return nil, errors.New("invalid calendar size")
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') && len(lines) > 0 {
			lines[len(lines)-1] += line[1:]
			continue
		}
		lines = append(lines, line)
	}
	return lines, scanner.Err()
}

func readCalRecords(raw []byte) ([]calRecord, error) {
	lines, err := unfoldICal(raw)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 || !strings.EqualFold(lines[0], "BEGIN:VCALENDAR") {
		return nil, errors.New("not an iCalendar feed")
	}
	var records []calRecord
	var current calRecord
	for _, line := range lines {
		switch strings.ToUpper(line) {
		case "BEGIN:VEVENT":
			current = make(calRecord)
			continue
		case "END:VEVENT":
			if current != nil {
				records = append(records, current)
				current = nil
			}
			continue
		}
		if current == nil {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		keyPart := strings.Split(line[:colon], ";")
		key := strings.ToUpper(keyPart[0])
		prop := calProperty{value: line[colon+1:], params: map[string]string{}}
		for _, part := range keyPart[1:] {
			pair := strings.SplitN(part, "=", 2)
			if len(pair) == 2 {
				prop.params[strings.ToUpper(pair[0])] = strings.Trim(pair[1], "\"")
			}
		}
		current[key] = append(current[key], prop)
	}
	return records, nil
}
func (r calRecord) first(key string) calProperty {
	if p := r[key]; len(p) > 0 {
		return p[0]
	}
	return calProperty{}
}
func icalUnescape(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case 'n', 'N':
				out.WriteByte(' ')
			case ',', ';', '\\':
				out.WriteByte(s[i])
			default:
				out.WriteByte(s[i])
			}
		} else {
			out.WriteByte(s[i])
		}
	}
	return out.String()
}
func parseCalTime(p calProperty, home *time.Location) (time.Time, bool, error) {
	if p.value == "" {
		return time.Time{}, false, errors.New("missing datetime")
	}
	allDay := p.params["VALUE"] == "DATE" || len(p.value) == 8
	if allDay {
		t, e := time.ParseInLocation("20060102", p.value, home)
		return t, true, e
	}
	tz := home
	if id := p.params["TZID"]; id != "" {
		var e error
		tz, e = time.LoadLocation(id)
		if e != nil {
			return time.Time{}, false, fmt.Errorf("unknown timezone")
		}
	}
	if strings.HasSuffix(p.value, "Z") {
		t, e := time.Parse("20060102T150405Z", p.value)
		return t, false, e
	}
	for _, layout := range []string{"20060102T150405", "20060102T1504"} {
		if t, e := time.ParseInLocation(layout, p.value, tz); e == nil {
			return t, false, nil
		}
	}
	return time.Time{}, false, errors.New("invalid datetime")
}
func parseCalTimeList(props []calProperty, home *time.Location) []time.Time {
	var result []time.Time
	for _, p := range props {
		for _, part := range strings.Split(p.value, ",") {
			one := p
			one.value = part
			if t, _, err := parseCalTime(one, home); err == nil {
				result = append(result, t)
			}
		}
	}
	return result
}

// simple recurrence expansion intentionally supports the most common Google
// series: DAILY/WEEKLY/MONTHLY/YEARLY plus BYDAY, BYMONTH and BYMONTHDAY,
// COUNT, UNTIL and exclusions. Unsupported rule clauses fail visibly rather
// than silently displaying an incomplete or incorrect event series.
type rule struct {
	freq                string
	interval, count     int
	until               time.Time
	byDay               []string
	byMonth, byMonthDay []int
	weekStart           time.Weekday
}

func parseRule(s string, home *time.Location) (rule, error) {
	r := rule{interval: 1, weekStart: time.Monday}
	for _, field := range strings.Split(s, ";") {
		a := strings.SplitN(field, "=", 2)
		if len(a) != 2 {
			return r, errors.New("bad RRULE")
		}
		k, v := strings.ToUpper(a[0]), strings.ToUpper(a[1])
		switch k {
		case "FREQ":
			r.freq = v
		case "INTERVAL":
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 {
				return r, errors.New("bad interval")
			}
			r.interval = n
		case "COUNT":
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 {
				return r, errors.New("bad count")
			}
			r.count = n
		case "UNTIL":
			t, _, e := parseCalTime(calProperty{value: v, params: map[string]string{}}, home)
			if e != nil {
				return r, e
			}
			r.until = t
		case "BYDAY":
			r.byDay = strings.Split(v, ",")
		case "BYMONTH", "BYMONTHDAY":
			var nums []int
			for _, s := range strings.Split(v, ",") {
				n, e := strconv.Atoi(s)
				if e != nil {
					return r, errors.New("invalid RRULE number")
				}
				nums = append(nums, n)
			}
			if k == "BYMONTH" {
				r.byMonth = nums
			} else {
				r.byMonthDay = nums
			}
		case "WKST":
			w, ok := weekDay(v)
			if !ok {
				return r, errors.New("unsupported WKST")
			}
			r.weekStart = w
		default:
			return r, fmt.Errorf("unsupported RRULE field %s", k)
		}
	}
	switch r.freq {
	case "DAILY", "WEEKLY", "MONTHLY", "YEARLY":
	default:
		return r, errors.New("unsupported recurrence frequency")
	}
	return r, nil
}
func weekDay(s string) (time.Weekday, bool) {
	switch s {
	case "SU":
		return time.Sunday, true
	case "MO":
		return time.Monday, true
	case "TU":
		return time.Tuesday, true
	case "WE":
		return time.Wednesday, true
	case "TH":
		return time.Thursday, true
	case "FR":
		return time.Friday, true
	case "SA":
		return time.Saturday, true
	}
	return time.Sunday, false
}
func hasInt(xs []int, n int) bool {
	for _, x := range xs {
		if x == n {
			return true
		}
	}
	return false
}
func dayMatches(day time.Time, start time.Time, spec string) bool {
	if len(spec) < 2 {
		return false
	}
	weekday, ok := weekDay(spec[len(spec)-2:])
	if !ok || day.Weekday() != weekday {
		return false
	}
	prefix := spec[:len(spec)-2]
	if prefix == "" {
		return true
	}
	n, e := strconv.Atoi(prefix)
	if e != nil || n == 0 {
		return false
	}
	if n > 0 {
		return (day.Day()-1)/7+1 == n
	}
	lastDay := time.Date(day.Year(), day.Month()+1, 0, 12, 0, 0, 0, day.Location()).Day()
	return -(lastDay-day.Day())/7-1 == n
}
func midnight(t time.Time, loc *time.Location) time.Time {
	z := t.In(loc)
	return time.Date(z.Year(), z.Month(), z.Day(), 0, 0, 0, 0, loc)
}
func matchesRule(day, timeStart time.Time, r rule) bool {
	dayStart := midnight(day, timeStart.Location())
	day0 := midnight(timeStart, timeStart.Location())
	if dayStart.Before(day0) {
		return false
	}
	// Calendar-day differences use UTC dates to avoid DST 23/25-hour days.
	difference := int(time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), 0, 0, 0, 0, time.UTC).Sub(time.Date(day0.Year(), day0.Month(), day0.Day(), 0, 0, 0, 0, time.UTC)).Hours() / 24)
	months := (day.Year()-timeStart.Year())*12 + int(day.Month()-timeStart.Month())
	switch r.freq {
	case "DAILY":
		if difference%r.interval != 0 {
			return false
		}
	case "WEEKLY":
		// Align week boundaries to WKST.
		delta := (int(day0.Weekday()) - int(r.weekStart) + 7) % 7
		if ((difference+delta)/7)%r.interval != 0 {
			return false
		}
	case "MONTHLY":
		if months%r.interval != 0 {
			return false
		}
	case "YEARLY":
		if (day.Year()-timeStart.Year())%r.interval != 0 {
			return false
		}
	}
	if len(r.byMonth) > 0 && !hasInt(r.byMonth, int(day.Month())) {
		return false
	}
	if len(r.byMonth) == 0 && r.freq == "YEARLY" && day.Month() != timeStart.Month() {
		return false
	}
	if len(r.byMonthDay) > 0 {
		last := time.Date(day.Year(), day.Month()+1, 0, 12, 0, 0, 0, day.Location()).Day()
		match := false
		for _, n := range r.byMonthDay {
			if n == day.Day() || (n < 0 && last+n+1 == day.Day()) {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	} else if len(r.byDay) == 0 && (r.freq == "MONTHLY" || r.freq == "YEARLY") && day.Day() != timeStart.Day() {
		return false
	}
	if len(r.byDay) > 0 {
		good := false
		for _, w := range r.byDay {
			if dayMatches(day, timeStart, w) {
				good = true
				break
			}
		}
		if !good {
			return false
		}
	}
	if len(r.byDay) == 0 && r.freq == "WEEKLY" && day.Weekday() != timeStart.Weekday() {
		return false
	}
	return true
}
func expandRule(start time.Time, r rule, from, to time.Time) ([]time.Time, error) {
	var out []time.Time
	day := midnight(start, start.Location())
	count := 0
	// Cap historical iteration to prevent malicious feeds consuming CPU.
	if day.Before(to.AddDate(-100, 0, 0)) {
		return nil, errors.New("recurring series starts more than 100 years ago")
	}
	for steps := 0; day.Before(to) && steps < 37000; steps++ {
		occurrence := time.Date(day.Year(), day.Month(), day.Day(), start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), start.Location())
		if !r.until.IsZero() && occurrence.After(r.until) {
			break
		}
		if matchesRule(day, start, r) {
			count++
			if !occurrence.Before(from) && occurrence.Before(to) {
				out = append(out, occurrence)
			}
			if r.count > 0 && count >= r.count {
				break
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return out, nil
}
func addUnique(out map[string]calendarItem, x calendarItem, from, to time.Time) {
	if x.end.After(from) && x.start.Before(to) {
		key := x.uid + "/" + x.start.UTC().Format(time.RFC3339Nano)
		out[key] = x
	}
}

func parseICalendar(raw []byte, now time.Time, home *time.Location) ([]calendarItem, error) {
	records, err := readCalRecords(raw)
	if err != nil {
		return nil, err
	}
	from := midnight(now, home)
	to := from.AddDate(0, 0, 7)
	// Include earlier starts when multi-day events continue into the selected week.
	searchFrom := from.AddDate(0, 0, -30)
	type parsed struct {
		item            calendarItem
		recur           string
		override        time.Time
		exdates, rdates []time.Time
		cancelled       bool
	}
	var entries []parsed
	for _, rec := range records {
		start, allDay, e := parseCalTime(rec.first("DTSTART"), home)
		if e != nil {
			continue
		}
		end := start.Add(time.Hour)
		if allDay {
			end = start.AddDate(0, 0, 1)
		}
		if p := rec.first("DTEND"); p.value != "" {
			if t, _, e := parseCalTime(p, home); e == nil {
				end = t
			}
		}
		if !end.After(start) {
			continue
		}
		title := strings.TrimSpace(icalUnescape(rec.first("SUMMARY").value))
		if title == "" {
			title = "Busy"
		}
		item := calendarItem{uid: rec.first("UID").value, title: title, start: start, end: end, allDay: allDay}
		var overridden time.Time
		if p := rec.first("RECURRENCE-ID"); p.value != "" {
			overridden, _, _ = parseCalTime(p, home)
		}
		entries = append(entries, parsed{item: item, recur: rec.first("RRULE").value, override: overridden,
			exdates: parseCalTimeList(rec["EXDATE"], home), rdates: parseCalTimeList(rec["RDATE"], home),
			cancelled: strings.EqualFold(rec.first("STATUS").value, "CANCELLED")})
	}
	overrides := make(map[string]parsed)
	for _, entry := range entries {
		if !entry.override.IsZero() {
			overrides[entry.item.uid+"/"+entry.override.UTC().Format(time.RFC3339Nano)] = entry
		}
	}
	out := make(map[string]calendarItem)
	for _, entry := range entries {
		if !entry.override.IsZero() || entry.cancelled {
			continue
		}
		duration := entry.item.end.Sub(entry.item.start)
		allDaySpan := 0
		if entry.item.allDay {
			a := midnight(entry.item.start, entry.item.start.Location())
			b := midnight(entry.item.end, entry.item.start.Location())
			for a.Before(b) && allDaySpan < 366 {
				allDaySpan++
				a = a.AddDate(0, 0, 1)
			}
		}
		starts := []time.Time{entry.item.start}
		if entry.recur != "" {
			rule, err := parseRule(entry.recur, entry.item.start.Location())
			if err != nil {
				return nil, err
			}
			starts, err = expandRule(entry.item.start, rule, searchFrom, to)
			if err != nil {
				return nil, err
			}
		}
		starts = append(starts, entry.rdates...)
		excluded := make(map[string]bool)
		for _, ex := range entry.exdates {
			excluded[ex.UTC().Format(time.RFC3339Nano)] = true
		}
		for _, st := range starts {
			id := entry.item.uid + "/" + st.UTC().Format(time.RFC3339Nano)
			if excluded[st.UTC().Format(time.RFC3339Nano)] {
				continue
			}
			if _, ok := overrides[id]; ok {
				continue
			}
			item := entry.item
			item.start = st
			item.end = st.Add(duration)
			if item.allDay && allDaySpan > 0 {
				item.end = st.AddDate(0, 0, allDaySpan)
			}
			addUnique(out, item, from, to)
		}
	}
	for _, entry := range entries {
		if !entry.override.IsZero() && !entry.cancelled {
			addUnique(out, entry.item, from, to)
		}
	}
	result := make([]calendarItem, 0, len(out))
	for _, x := range out {
		result = append(result, x)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].start.Equal(result[j].start) {
			return result[i].title < result[j].title
		}
		return result[i].start.Before(result[j].start)
	})
	if len(result) > 40 {
		result = result[:40]
	}
	return result, nil
}
