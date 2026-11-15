package hinducalendar

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// ev builds a valid minimal event on the given date.
func ev(date, slug, category string) Event {
	return Event{
		ID: date + "_" + slug, Date: date, Category: category,
		IconFileKey: "hindu-events/icons/x.png",
		Name:        Text{Hi: "नाम", En: slug}, Description: Text{Hi: "विवरण", En: "desc"},
		SunriseAt: utc(date + "T01:00:00Z"), SunsetAt: utc(date + "T12:00:00Z"),
	}
}

func mustStore(t *testing.T, events ...Event) *Store {
	t.Helper()
	data, err := Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// ---- the committed data file ----

func TestEmbeddedDataIsValid(t *testing.T) {
	s := MustLoad()
	if s.Len() < 317 {
		t.Fatalf("embedded file has only %d events", s.Len())
	}
	if s.FirstDate() > "2026-11-01" || s.LastDate() < "2027-12-31" {
		t.Errorf("coverage %s..%s does not include 2026-11-01..2027-12-31", s.FirstDate(), s.LastDate())
	}
	e, ok := s.Get("2026-11-05_rama-ekadashi")
	if !ok {
		t.Fatal("2026-11-05_rama-ekadashi missing")
	}
	if e.Category != "ekadashi" || e.Name.En != "Rama Ekadashi" || e.Name.Hi != "रमा एकादशी" {
		t.Errorf("rama ekadashi = %+v", e)
	}
	if e.Tithi == nil || e.Parana == nil {
		t.Errorf("rama ekadashi should have a tithi and parana window: %+v", e)
	}
	if got := TithiLabel(LangHi, e.Category, *e.Tithi); got != "कार्तिक, कृष्ण एकादशी" {
		t.Errorf("tithi label = %q", got)
	}

	// Spot checks on the shape of the real data.
	if v, ok := s.Get("2026-11-16_vrishchika-sankranti"); !ok || v.Tithi != nil || v.SankrantiAt == nil {
		t.Errorf("vrishchika sankranti = %+v", v)
	}
	var eclipses, withEclipseTimes int
	for i := range s.events {
		x := &s.events[i]
		if !strings.HasPrefix(x.IconFileKey, "hindu-events/icons/") {
			t.Errorf("%s: icon key %q", x.ID, x.IconFileKey)
		}
		if x.Category == "eclipse" {
			eclipses++
			if x.Eclipse != nil {
				withEclipseTimes++
			}
		}
	}
	if eclipses != 4 || withEclipseTimes != 2 {
		t.Errorf("eclipses = %d (with times %d), want 4 and 2", eclipses, withEclipseTimes)
	}
}

func TestEmbeddedDataRoundTripsByteForByte(t *testing.T) {
	s := MustLoad()
	got, err := Marshal(s.events)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, embeddedData) {
		t.Error("data file is not in canonical form (sorted, 2-space indent, trailing newline); re-run the generator or format it the same way")
	}
}

// ---- validation ----

func TestValidateAcceptsMinimalEvent(t *testing.T) {
	if err := Validate([]Event{ev("2027-01-01", "new-year", "festival")}); err != nil {
		t.Fatal(err)
	}
	if err := Validate(nil); err != nil {
		t.Fatalf("empty data should be valid: %v", err)
	}
}

func TestValidateRules(t *testing.T) {
	win := func(a, b string) *Window { return &Window{StartAt: utc(a), EndAt: utc(b)} }
	cases := []struct {
		name   string
		mutate func(e *Event)
		want   string
	}{
		{"id format", func(e *Event) { e.ID = "diwali" }, "id must look like"},
		{"id uppercase", func(e *Event) { e.ID = "2027-01-01_New-Year" }, "id must look like"},
		{"id date prefix", func(e *Event) { e.ID = "2027-01-02_new-year" }, "does not match date"},
		{"bad date", func(e *Event) { e.Date = "01/01/2027"; e.ID = "01/01/2027_x" }, "invalid date"},
		{"category", func(e *Event) { e.Category = "party" }, "unknown category"},
		{"icon", func(e *Event) { e.IconFileKey = "" }, "icon_file_key is required"},
		{"name hi", func(e *Event) { e.Name.Hi = "" }, "name needs both"},
		{"description en", func(e *Event) { e.Description.En = "" }, "description needs both"},
		{"sunrise missing", func(e *Event) { e.SunriseAt = time.Time{} }, "sunrise_at is required"},
		{"sunset before sunrise", func(e *Event) { e.SunsetAt = e.SunriseAt.Add(-time.Hour) }, "sunset_at must be after"},
		{"non-UTC instant", func(e *Event) { e.SunsetAt = e.SunsetAt.In(time.FixedZone("IST", 19800)) }, "must be in UTC"},
		{"tithi paksha", func(e *Event) {
			e.Tithi = &Tithi{Paksha: "grey", Number: 3, StartAt: utc("2027-01-01T00:00:00Z"), EndAt: utc("2027-01-01T05:00:00Z")}
		}, "tithi.paksha"},
		{"tithi number", func(e *Event) {
			e.Tithi = &Tithi{Paksha: "shukla", Number: 16, StartAt: utc("2027-01-01T00:00:00Z"), EndAt: utc("2027-01-01T05:00:00Z")}
		}, "tithi.number"},
		{"tithi month", func(e *Event) {
			e.Tithi = &Tithi{LunarMonth: "smarch", Paksha: "shukla", Number: 3, StartAt: utc("2027-01-01T00:00:00Z"), EndAt: utc("2027-01-01T05:00:00Z")}
		}, "tithi.lunar_month"},
		{"tithi month required", func(e *Event) {
			e.Tithi = &Tithi{Paksha: "shukla", Number: 3, StartAt: utc("2027-01-01T00:00:00Z"), EndAt: utc("2027-01-01T05:00:00Z")}
		}, "tithi.lunar_month is required"},
		{"tithi month may be absent only as eclipse number 15", func(e *Event) {
			e.Category = "eclipse"
			e.Tithi = &Tithi{Paksha: "shukla", Number: 3, StartAt: utc("2027-01-01T00:00:00Z"), EndAt: utc("2027-01-01T05:00:00Z")}
		}, "tithi.lunar_month is required"},
		{"eclipse timings on a non-eclipse row", func(e *Event) {
			e.Eclipse = &Eclipse{StartAt: utc("2027-01-01T04:00:00Z"), PeakAt: utc("2027-01-01T05:00:00Z"), EndAt: utc("2027-01-01T06:00:00Z")}
		}, "only allowed on eclipse rows"},
		{"tithi window", func(e *Event) {
			e.Tithi = &Tithi{Paksha: "shukla", Number: 3, StartAt: utc("2027-01-01T05:00:00Z"), EndAt: utc("2027-01-01T05:00:00Z")}
		}, "tithi.end_at must be after"},
		{"puja window", func(e *Event) { e.Puja = win("2027-01-01T05:00:00Z", "2027-01-01T04:00:00Z") }, "puja.end_at must be after"},
		{"parana window", func(e *Event) { e.Parana = win("2027-01-02T05:00:00Z", "2027-01-02T05:00:00Z") }, "parana.end_at must be after"},
		{"eclipse order", func(e *Event) {
			e.Eclipse = &Eclipse{StartAt: utc("2027-01-01T05:00:00Z"), PeakAt: utc("2027-01-01T04:00:00Z"), EndAt: utc("2027-01-01T06:00:00Z")}
		}, "start_at < peak_at < end_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := ev("2027-01-01", "new-year", "festival")
			tc.mutate(&e)
			err := Validate([]Event{e})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestValidateOrderingAndUniqueness(t *testing.T) {
	a, b := ev("2027-01-01", "a", "festival"), ev("2027-01-02", "b", "festival")
	if err := Validate([]Event{b, a}); err == nil || !strings.Contains(err.Error(), "sorted") {
		t.Errorf("unsorted rows: %v", err)
	}
	if err := Validate([]Event{a, a}); err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Errorf("duplicate ids: %v", err)
	}
	// Same date: category display order wins (festival before vrat), then id.
	fest, vrat := ev("2027-01-01", "z-fest", "festival"), ev("2027-01-01", "a-vrat", "vrat")
	if err := Validate([]Event{fest, vrat}); err != nil {
		t.Errorf("festival before vrat should be valid: %v", err)
	}
	if err := Validate([]Event{vrat, fest}); err == nil {
		t.Error("vrat before festival should be rejected")
	}
}

func TestLoadRejectsBadFiles(t *testing.T) {
	if _, err := Load([]byte(`not json`)); err == nil {
		t.Error("invalid JSON accepted")
	}
	if _, err := Load([]byte(`[{"id":"2027-01-01_x","surprise":1}]`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("unknown field accepted: %v", err)
	}
	if _, err := Load([]byte(`[{"id":"nope"}]`)); err == nil {
		t.Error("invalid event accepted")
	}
	// A non-UTC offset in a hand-edited file is rejected, not silently converted.
	bad := `[{"id":"2027-01-01_x","date":"2027-01-01","category":"festival","icon_file_key":"k",
	  "name":{"hi":"न","en":"x"},"description":{"hi":"व","en":"d"},
	  "sunrise_at":"2027-01-01T06:30:00+05:30","sunset_at":"2027-01-01T12:00:00Z"}]`
	if _, err := Load([]byte(bad)); err == nil || !strings.Contains(err.Error(), "UTC") {
		t.Errorf("non-UTC time accepted: %v", err)
	}
	// Anything after the event array means the file is damaged (e.g. two arrays pasted together).
	for _, in := range []string{`[] trailing garbage`, `[]{"x":1}`, `[][]`} {
		if _, err := Load([]byte(in)); err == nil || !strings.Contains(err.Error(), "after the event array") {
			t.Errorf("Load(%q) = %v, want a trailing-data error", in, err)
		}
	}
	// An empty list is a valid file for Load (the embedded file is additionally required to be
	// non-empty by MustLoad, see TestEmbeddedDataIsValid).
	if s, err := Load([]byte(`[]`)); err != nil || s.Len() != 0 {
		t.Errorf("empty list should load: %v", err)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	e := ev("2027-02-21", "chandra-grahan", "eclipse")
	e.Tithi = &Tithi{Paksha: "shukla", Number: 15, StartAt: utc("2027-02-20T02:30:00Z"), EndAt: utc("2027-02-20T23:23:00Z")}
	e.Eclipse = &Eclipse{StartAt: utc("2027-02-20T21:13:00Z"), PeakAt: utc("2027-02-20T23:13:00Z"), EndAt: utc("2027-02-21T01:12:00Z")}
	m := utc("2027-02-20T20:00:00Z")
	e.MoonriseAt = &m
	data, err := Marshal([]Event{e})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"sunrise_at": "2027-02-21T01:00:00Z"`, `"moonrise_at": "2027-02-20T20:00:00Z"`, `"paksha": "shukla"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("encoded data lacks %s:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), `"lunar_month"`) || strings.Contains(string(data), `"puja"`) {
		t.Errorf("absent fields must be omitted:\n%s", data)
	}
	if !strings.Contains(string(data), "नाम") {
		t.Error("non-ASCII text must not be \\u-escaped")
	}
	s, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(e.ID)
	if got.Eclipse == nil || !got.Eclipse.PeakAt.Equal(e.Eclipse.PeakAt) || got.Tithi.LunarMonth != "" {
		t.Errorf("round trip = %+v", got)
	}
}

// ---- queries ----

func queryStore(t *testing.T) *Store {
	return mustStore(t,
		ev("2027-01-01", "a-festival", "festival"),
		ev("2027-01-01", "b-vrat", "vrat"),
		ev("2027-01-05", "c-ekadashi", "ekadashi"),
		ev("2027-01-09", "d-festival", "festival"),
		ev("2027-01-09", "e-vrat", "vrat"),
		ev("2027-01-20", "f-ekadashi", "ekadashi"),
	)
}

func idsOf(events []*Event) []string {
	var out []string
	for _, e := range events {
		out = append(out, e.ID)
	}
	return out
}

func TestOnOrAfter(t *testing.T) {
	s := queryStore(t)
	for date, want := range map[string]string{
		"2026-12-31": "2027-01-01_a-festival,2027-01-01_b-vrat", // before the data -> first date
		"2027-01-01": "2027-01-01_a-festival,2027-01-01_b-vrat", // today has events
		"2027-01-02": "2027-01-05_c-ekadashi",                   // next date with events
		"2027-01-09": "2027-01-09_d-festival,2027-01-09_e-vrat",
		"2027-01-20": "2027-01-20_f-ekadashi",
		"2027-01-21": "", // after the last date
	} {
		if got := strings.Join(idsOf(s.OnOrAfter(date)), ","); got != want {
			t.Errorf("OnOrAfter(%s) = %q, want %q", date, got, want)
		}
	}
}

func TestQuery(t *testing.T) {
	s := queryStore(t)
	check := func(name string, q Query, wantTotal int, wantIDs string) {
		t.Helper()
		if q.Limit == 0 {
			q.Limit = 100
		}
		page, total := s.Query(q)
		if total != wantTotal || strings.Join(idsOf(page), ",") != wantIDs {
			t.Errorf("%s: total=%d ids=%v, want total=%d ids=%s", name, total, idsOf(page), wantTotal, wantIDs)
		}
	}
	check("everything", Query{}, 6, "2027-01-01_a-festival,2027-01-01_b-vrat,2027-01-05_c-ekadashi,2027-01-09_d-festival,2027-01-09_e-vrat,2027-01-20_f-ekadashi")
	check("start date is inclusive", Query{StartDate: "2027-01-05"}, 4, "2027-01-05_c-ekadashi,2027-01-09_d-festival,2027-01-09_e-vrat,2027-01-20_f-ekadashi")
	check("end date is inclusive", Query{EndDate: "2027-01-05"}, 3, "2027-01-01_a-festival,2027-01-01_b-vrat,2027-01-05_c-ekadashi")
	check("window", Query{StartDate: "2027-01-02", EndDate: "2027-01-09"}, 3, "2027-01-05_c-ekadashi,2027-01-09_d-festival,2027-01-09_e-vrat")
	check("one category", Query{Categories: []string{"ekadashi"}}, 2, "2027-01-05_c-ekadashi,2027-01-20_f-ekadashi")
	check("two categories", Query{Categories: []string{"festival", "vrat"}, EndDate: "2027-01-09"}, 4, "2027-01-01_a-festival,2027-01-01_b-vrat,2027-01-09_d-festival,2027-01-09_e-vrat")
	check("page 1", Query{Limit: 2}, 6, "2027-01-01_a-festival,2027-01-01_b-vrat")
	check("page 2", Query{Offset: 2, Limit: 2}, 6, "2027-01-05_c-ekadashi,2027-01-09_d-festival")
	check("last partial page", Query{Offset: 5, Limit: 2}, 6, "2027-01-20_f-ekadashi")
	check("page past the end", Query{Offset: 10, Limit: 2}, 6, "")
	check("nothing matches", Query{StartDate: "2028-01-01"}, 0, "")
	check("empty window", Query{StartDate: "2027-01-10", EndDate: "2027-01-19"}, 0, "")
}

func TestStoreGetAndCoverage(t *testing.T) {
	s := queryStore(t)
	if e, ok := s.Get("2027-01-05_c-ekadashi"); !ok || e.Category != "ekadashi" {
		t.Errorf("Get = %+v, %v", e, ok)
	}
	if _, ok := s.Get("nope"); ok {
		t.Error("unknown id found")
	}
	if s.FirstDate() != "2027-01-01" || s.LastDate() != "2027-01-20" {
		t.Errorf("range = %s..%s", s.FirstDate(), s.LastDate())
	}
	empty := mustStore(t)
	if empty.FirstDate() != "" || empty.LastDate() != "" || empty.OnOrAfter("2027-01-01") != nil {
		t.Error("empty store should have no dates or events")
	}

	if _, warn := s.Coverage(utc("2027-01-01T00:00:00Z")); !warn {
		t.Error("data ending in 19 days should warn")
	}
	long := mustStore(t, ev("2027-01-01", "a", "festival"), ev("2028-01-01", "b", "festival"))
	summary, warn := long.Coverage(utc("2027-02-01T00:00:00Z"))
	if warn || summary != "hindu calendar loaded: 2 events, 2027-01-01..2028-01-01" {
		t.Errorf("coverage = %q, warn=%v", summary, warn)
	}
	if _, warn := long.Coverage(utc("2027-10-15T00:00:00Z")); !warn { // 78 days left
		t.Error("78 days left should warn")
	}
	if _, warn := empty.Coverage(time.Now()); !warn {
		t.Error("an empty calendar should warn")
	}
}
