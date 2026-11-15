package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	hc "go-backend/internal/apps/sanskaar/hinducalendar"
)

// Pure CSV -> []hinducalendar.Event logic (no I/O besides the reader), unit-tested in parse_test.go.

const (
	timeLayout = "2006-01-02 15:04" // the CSV's "(UTC)" columns
	iconPrefix = "hindu-events/icons/"
	utf8BOM    = "\xef\xbb\xbf"
)

// Columns the generator needs. Columns are matched by header name, so reordering/adding columns
// in the source CSV is safe: a missing required column is an error, an unknown one a warning.
var requiredColumns = []string{
	"Date", "Event (Hindi)", "Event (English)", "Description (Hindi)", "Description (English)",
	"Category (English)", "Tithi (English)", "Tithi Start (UTC)", "Tithi End (UTC)",
	"Puja Start (UTC)", "Puja End (UTC)", "Nishita Start (UTC)", "Moonrise (UTC)", "Sankranti Moment (UTC)",
	"Sunrise (UTC)", "Sunset (UTC)", "Parana Start (UTC)", "Parana End (UTC)",
	"Eclipse Start (UTC)", "Eclipse Peak (UTC)", "Eclipse End (UTC)", "Image URL",
}

// Columns that repeat information derivable from the stored fields. They are not stored; when
// present, every row is cross-checked against hinducalendar's label tables.
var derivedColumns = []string{
	"Year", "Month (Hindi)", "Month (English)", "Day", "Weekday (Hindi)", "Weekday (English)",
	"Category (Hindi)", "Tithi (Hindi)",
}

// Row is one CSV data row keyed by header name.
type Row struct {
	Line   int // 1-based line in the file (header is line 1)
	Fields map[string]string
}

func (r Row) get(col string) string { return r.Fields[col] }

// Report summarizes a generation run.
type Report struct {
	Rows         int
	Events       int
	WithoutTithi int
	Warnings     []string
	Icons        map[string]string // R2 key -> source URL
}

// ReadCSV reads the CSV by header name. It strips a UTF-8 BOM and trims every cell.
func ReadCSV(r io.Reader) (rows []Row, warnings []string, err error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	records, err := cr.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("reading CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, nil, fmt.Errorf("CSV is empty")
	}
	header := records[0]
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], utf8BOM)
	}
	index := make(map[string]int, len(header))
	for i, h := range header {
		index[strings.TrimSpace(h)] = i
	}
	var missing []string
	for _, c := range requiredColumns {
		if _, ok := index[c]; !ok {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return nil, nil, fmt.Errorf("CSV is missing required columns: %s", strings.Join(missing, ", "))
	}
	known := map[string]bool{}
	for _, c := range requiredColumns {
		known[c] = true
	}
	for _, c := range derivedColumns {
		known[c] = true
	}
	for _, h := range header {
		if h = strings.TrimSpace(h); !known[h] {
			warnings = append(warnings, fmt.Sprintf("unknown column %q is ignored", h))
		}
	}
	for i, rec := range records[1:] {
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue // blank line
		}
		f := make(map[string]string, len(index))
		for name, col := range index {
			if col < len(rec) {
				f[name] = strings.TrimSpace(rec[col])
			}
		}
		rows = append(rows, Row{Line: i + 2, Fields: f})
	}
	return rows, warnings, nil
}

// iconKey is the R2 object key of an icon: a fixed prefix plus the source URL's file name
// (without any query string), so the same icon always gets the same key.
func iconKey(src string) string {
	return iconPrefix + path.Base(strings.SplitN(src, "?", 2)[0])
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify lowercases s and turns every run of non-alphanumerics into a single '-'.
func Slugify(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func parseTime(s string) (time.Time, error) {
	return time.ParseInLocation(timeLayout, s, time.UTC)
}

// rowErrs accumulates the errors for one row.
type rowErrs struct {
	line int
	errs []string
}

func (r *rowErrs) add(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf("line %d: %s", r.line, fmt.Sprintf(format, args...)))
}

// BuildEvents turns CSV rows into validated, sorted events plus a report. It returns an error
// listing every problem found, or nil when the rows are good.
func BuildEvents(rows []Row) ([]hc.Event, *Report, error) {
	rep := &Report{Rows: len(rows), Icons: map[string]string{}}
	var problems []string
	events := make([]hc.Event, 0, len(rows))
	tithiStarts := make([]tithiPoint, 0, len(rows))

	for _, row := range rows {
		re := &rowErrs{line: row.Line}
		ev, warns := buildEvent(row, re)
		rep.Warnings = append(rep.Warnings, warns...)
		if ev.Tithi == nil {
			rep.WithoutTithi++
		}
		crossCheck(row, &ev, re)

		if src := row.get("Image URL"); src != "" {
			key := iconKey(src)
			if prev, ok := rep.Icons[key]; ok && prev != src {
				re.add("icon file name %q is used by two different URLs (%s and %s)", path.Base(key), prev, src)
			}
			rep.Icons[key] = src
		}
		problems = append(problems, re.errs...)
		events = append(events, ev)
		if ev.Tithi != nil && ev.Tithi.LunarMonth != "" {
			tithiStarts = append(tithiStarts, tithiPoint{start: ev.Tithi.StartAt, paksha: ev.Tithi.Paksha, month: ev.Tithi.LunarMonth, id: ev.ID})
		}
	}

	problems = append(problems, datasetChecks(rows, events, tithiStarts)...)

	hc.Sort(events)
	if err := hc.Validate(events); err != nil {
		problems = append(problems, err.Error())
	}
	rep.Events = len(events)
	if len(problems) > 0 {
		return nil, rep, fmt.Errorf("%d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	return events, rep, nil
}

func buildEvent(row Row, re *rowErrs) (hc.Event, []string) {
	var ev hc.Event
	var warns []string

	if _, err := time.Parse(hc.DateLayout, row.get("Date")); err != nil {
		re.add("invalid Date %q, expected YYYY-MM-DD", row.get("Date"))
	}
	nameEN, nameHI := row.get("Event (English)"), row.get("Event (Hindi)")
	if nameEN == "" || nameHI == "" {
		re.add("event name is required in Hindi and English")
	}
	ev.Date = row.get("Date")
	ev.ID = ev.Date + "_" + Slugify(nameEN)
	ev.Name = hc.Text{Hi: nameHI, En: nameEN}
	ev.Description = hc.Text{Hi: row.get("Description (Hindi)"), En: row.get("Description (English)")}

	cat, ok := hc.CategoryFromEnglish(row.get("Category (English)"))
	if !ok {
		re.add("unknown category %q", row.get("Category (English)"))
	}
	ev.Category = cat

	if src := row.get("Image URL"); src == "" {
		re.add("Image URL is required")
	} else {
		ev.IconFileKey = iconKey(src)
	}

	times := func(col string) *time.Time {
		s := row.get(col)
		if s == "" {
			return nil
		}
		t, err := parseTime(s)
		if err != nil {
			re.add("%s: invalid time %q, expected YYYY-MM-DD HH:MM", col, s)
			return nil
		}
		return &t
	}
	pair := func(startCol, endCol string) *hc.Window {
		s, e := times(startCol), times(endCol)
		if s == nil && e == nil {
			return nil
		}
		if s == nil || e == nil {
			re.add("%s and %s must both be set or both empty", startCol, endCol)
			return nil
		}
		return &hc.Window{StartAt: *s, EndAt: *e}
	}

	if t := times("Sunrise (UTC)"); t != nil {
		ev.SunriseAt = *t
	} else {
		re.add("Sunrise (UTC) is required")
	}
	if t := times("Sunset (UTC)"); t != nil {
		ev.SunsetAt = *t
	} else {
		re.add("Sunset (UTC) is required")
	}
	ev.Puja = pair("Puja Start (UTC)", "Puja End (UTC)")
	ev.Parana = pair("Parana Start (UTC)", "Parana End (UTC)")
	ev.NishitaStartAt = times("Nishita Start (UTC)")
	ev.MoonriseAt = times("Moonrise (UTC)")
	ev.SankrantiAt = times("Sankranti Moment (UTC)")

	es, ep, ee := times("Eclipse Start (UTC)"), times("Eclipse Peak (UTC)"), times("Eclipse End (UTC)")
	switch {
	case es == nil && ep == nil && ee == nil:
	case es != nil && ep != nil && ee != nil:
		ev.Eclipse = &hc.Eclipse{StartAt: *es, PeakAt: *ep, EndAt: *ee}
	default:
		re.add("Eclipse Start/Peak/End must all be set or all empty")
	}

	// Tithi
	text := row.get("Tithi (English)")
	ts, te := times("Tithi Start (UTC)"), times("Tithi End (UTC)")
	switch {
	case text == "":
		if ts != nil || te != nil {
			re.add("Tithi Start/End are set but Tithi (English) is empty")
		}
	case ts == nil || te == nil:
		re.add("Tithi (English) is set but Tithi Start/End are not both set")
	default:
		t, warn := parseTithi(text, cat, re)
		if warn != "" {
			warns = append(warns, fmt.Sprintf("line %d: %s", row.Line, warn))
		}
		if t != nil {
			t.StartAt, t.EndAt = *ts, *te
			ev.Tithi = t
		}
	}
	return ev, warns
}

// parseTithi parses "Month, Paksha Tithi" (e.g. "Kartika, Krishna Ashtami"), or on eclipse rows
// "occurs during Amavasya|Purnima" (any letter case; a warning is returned if it is lowercase).
func parseTithi(text, category string, re *rowErrs) (*hc.Tithi, string) {
	if !strings.Contains(text, ",") {
		paksha, ok := hc.EclipsePakshaFromEnglish(text)
		if !ok || category != hc.CategoryEclipse {
			re.add("Tithi (English) %q is not \"Month, Paksha Tithi\" and not an eclipse \"occurs during Amavasya|Purnima\"", text)
			return nil, ""
		}
		warn := ""
		if text[0] >= 'a' && text[0] <= 'z' {
			warn = fmt.Sprintf("Tithi (English) %q starts with a lowercase letter; the API shows it as \"Occurs during ...\"", text)
		}
		return &hc.Tithi{Paksha: paksha, Number: 15}, warn
	}
	monthPart, rest, _ := strings.Cut(text, ", ")
	month, ok := hc.LunarMonthFromEnglish(monthPart)
	if !ok {
		re.add("Tithi (English) %q: unknown lunar month %q", text, monthPart)
		return nil, ""
	}
	pakshaPart, tithiPart, found := strings.Cut(rest, " ")
	paksha, pok := hc.PakshaFromEnglish(pakshaPart)
	if !found || !pok {
		re.add("Tithi (English) %q: expected \"Month, Shukla|Krishna Tithi\"", text)
		return nil, ""
	}
	number, nok := hc.TithiNumberFromEnglish(tithiPart, paksha)
	if !nok {
		re.add("Tithi (English) %q: unknown tithi %q for the %s paksha", text, tithiPart, paksha)
		return nil, ""
	}
	return &hc.Tithi{LunarMonth: month, Paksha: paksha, Number: number}, ""
}

// crossCheck compares the derived CSV columns with what the label tables compute from the
// stored fields. Columns that are not in the CSV are skipped.
func crossCheck(row Row, ev *hc.Event, re *rowErrs) {
	d, err := time.Parse(hc.DateLayout, ev.Date)
	if err != nil {
		return
	}
	check := func(col, want string) {
		if got, present := row.Fields[col]; present && got != want {
			re.add("%s is %q but the date/fields imply %q", col, got, want)
		}
	}
	check("Year", strconv.Itoa(d.Year()))
	check("Day", strconv.Itoa(d.Day()))
	check("Month (English)", hc.MonthLabel(hc.LangEn, d.Month()))
	check("Month (Hindi)", hc.MonthLabel(hc.LangHi, d.Month()))
	check("Weekday (English)", hc.WeekdayLabel(hc.LangEn, d.Weekday()))
	check("Weekday (Hindi)", hc.WeekdayLabel(hc.LangHi, d.Weekday()))
	check("Category (Hindi)", hc.CategoryLabel(hc.LangHi, ev.Category))
	if ev.Tithi != nil {
		check("Tithi (Hindi)", hc.TithiLabel(hc.LangHi, ev.Category, *ev.Tithi))
		wantEN := hc.TithiLabel(hc.LangEn, ev.Category, *ev.Tithi)
		if got := row.get("Tithi (English)"); !strings.EqualFold(got, wantEN) {
			re.add("Tithi (English) is %q but the parsed fields imply %q", got, wantEN)
		}
	}
}

type tithiPoint struct {
	start  time.Time
	paksha string
	month  string
	id     string
}

// datasetChecks are the checks that need more than one row.
func datasetChecks(rows []Row, events []hc.Event, tithis []tithiPoint) []string {
	var out []string

	// Sunrise/sunset must agree for every row on the same date.
	type sun struct{ rise, set time.Time }
	bySun := map[string]sun{}
	byDateID := map[string]string{}
	for i := range events {
		e := &events[i]
		s := sun{e.SunriseAt, e.SunsetAt}
		if prev, ok := bySun[e.Date]; ok && prev != s {
			out = append(out, fmt.Sprintf("%s: rows on the same date disagree on sunrise/sunset (%s vs %s)", e.Date, byDateID[e.Date], e.ID))
		}
		bySun[e.Date] = s
		byDateID[e.Date] = e.ID
	}

	// Event date must fall inside its tithi window when read in IST.
	for i := range events {
		e := &events[i]
		if e.Tithi == nil {
			continue
		}
		day := e.Date
		if s, en := e.Tithi.StartAt.In(hc.IST).Format(hc.DateLayout), e.Tithi.EndAt.In(hc.IST).Format(hc.DateLayout); day < s || day > en {
			out = append(out, fmt.Sprintf("%s: date is outside its tithi window in IST (%s .. %s)", e.ID, s, en))
		}
	}

	// Names must map 1:1 between Hindi and English.
	enToHi, hiToEn := map[string]string{}, map[string]string{}
	for _, r := range rows {
		en, hi := r.get("Event (English)"), r.get("Event (Hindi)")
		if prev, ok := enToHi[en]; ok && prev != hi {
			out = append(out, fmt.Sprintf("English name %q has two Hindi names: %q and %q", en, prev, hi))
		}
		if prev, ok := hiToEn[hi]; ok && prev != en {
			out = append(out, fmt.Sprintf("Hindi name %q has two English names: %q and %q", hi, prev, en))
		}
		enToHi[en], hiToEn[hi] = hi, en
	}

	// Purnimanta consistency: a Krishna paksha belongs to the same lunar month as the Shukla
	// paksha that follows it, so each Krishna row's month must equal the next Shukla row's.
	sort.SliceStable(tithis, func(i, j int) bool { return tithis[i].start.Before(tithis[j].start) })
	for i, p := range tithis {
		if p.paksha != hc.PakshaKrishna {
			continue
		}
		for _, q := range tithis[i+1:] {
			if q.paksha == hc.PakshaShukla {
				if q.month != p.month {
					out = append(out, fmt.Sprintf("%s says lunar month %s (krishna) but the next shukla row %s says %s", p.id, p.month, q.id, q.month))
				}
				break
			}
		}
	}
	return out
}

// Merge combines existing file contents with newly generated events.
//   - "replace": the result is exactly the incoming events.
//   - "merge":   the incoming events' date range [min, max] is replaced wholesale and every
//     existing event outside that range is kept.
func Merge(existing, incoming []hc.Event, mode string) ([]hc.Event, error) {
	if len(incoming) == 0 {
		return nil, fmt.Errorf("no events generated from the CSV")
	}
	var out []hc.Event
	switch mode {
	case "replace":
		out = append(out, incoming...)
	case "merge":
		lo, hi := incoming[0].Date, incoming[0].Date
		for _, e := range incoming {
			if e.Date < lo {
				lo = e.Date
			}
			if e.Date > hi {
				hi = e.Date
			}
		}
		for _, e := range existing {
			if e.Date < lo || e.Date > hi {
				out = append(out, e)
			}
		}
		out = append(out, incoming...)
	default:
		return nil, fmt.Errorf("unknown -mode %q (use merge or replace)", mode)
	}
	hc.Sort(out)
	if err := hc.Validate(out); err != nil {
		return nil, err
	}
	return out, nil
}
