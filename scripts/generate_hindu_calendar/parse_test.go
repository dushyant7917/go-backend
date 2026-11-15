package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	hc "go-backend/internal/apps/sanskaar/hinducalendar"
)

// testdata/feb_2027.csv holds every row of February 2027 from the real CSV (with its UTF-8 BOM):
// Ekadashi parana windows, Pradosh puja windows, Shivaratri nishita, Sankashti moonrise, a
// Sankranti with no tithi, and two eclipses (one with timings, one without).

func fixtureRows(t *testing.T) []Row {
	t.Helper()
	f, err := os.Open("testdata/feb_2027.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, warns, err := ReadCSV(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("unexpected header warnings: %v", warns)
	}
	return rows
}

func cloneRows(rows []Row) []Row {
	out := make([]Row, len(rows))
	for i, r := range rows {
		f := make(map[string]string, len(r.Fields))
		for k, v := range r.Fields {
			f[k] = v
		}
		out[i] = Row{Line: r.Line, Fields: f}
	}
	return out
}

func findRow(t *testing.T, rows []Row, nameEN string) *Row {
	t.Helper()
	for i := range rows {
		if rows[i].Fields["Event (English)"] == nameEN {
			return &rows[i]
		}
	}
	t.Fatalf("fixture has no row %q", nameEN)
	return nil
}

func eventByID(t *testing.T, events []hc.Event, id string) hc.Event {
	t.Helper()
	for _, e := range events {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no event %q", id)
	return hc.Event{}
}

func TestReadCSV_FixtureStripsBOM(t *testing.T) {
	rows := fixtureRows(t)
	if len(rows) != 18 {
		t.Fatalf("rows = %d, want 18", len(rows))
	}
	if rows[0].Fields["Date"] != "2027-02-02" {
		t.Errorf("first Date = %q (BOM not stripped from the header?)", rows[0].Fields["Date"])
	}
}

func TestReadCSV_MissingAndUnknownColumns(t *testing.T) {
	_, _, err := ReadCSV(strings.NewReader("Date,Event (English)\n2027-01-01,X\n"))
	if err == nil || !strings.Contains(err.Error(), "missing required columns") {
		t.Fatalf("want missing-columns error, got %v", err)
	}

	rows := fixtureRows(t)
	var header []string
	for k := range rows[0].Fields {
		header = append(header, k)
	}
	csvText := strings.Join(header, ",") + ",Surprise\n" + strings.Repeat(",", len(header)) + "\n"
	_, warns, err := ReadCSV(strings.NewReader(csvText))
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "Surprise") {
		t.Errorf("want one unknown-column warning, got %v", warns)
	}
}

func TestBuildEvents_Fixture(t *testing.T) {
	events, rep, err := BuildEvents(fixtureRows(t))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Events != 18 || rep.Rows != 18 {
		t.Errorf("rows/events = %d/%d, want 18/18", rep.Rows, rep.Events)
	}
	if rep.WithoutTithi != 2 { // Kumbha Sankranti, Masik Karthigai
		t.Errorf("WithoutTithi = %d, want 2", rep.WithoutTithi)
	}
	if len(rep.Warnings) != 2 { // the two lowercase "occurs during" eclipse rows
		t.Errorf("warnings = %v, want 2 lowercase-eclipse warnings", rep.Warnings)
	}
	if key := "hindu-events/icons/sj-icon-ekadashi.png"; rep.Icons[key] == "" {
		t.Errorf("icons missing %s: %v", key, rep.Icons)
	}

	// Sorted by date, then category order, then id.
	for i := 1; i < len(events); i++ {
		if hc.Validate(events[i-1:i+1]) != nil {
			t.Fatalf("events %d/%d out of order or invalid", i-1, i)
		}
	}

	ekadashi := eventByID(t, events, "2027-02-02_shattila-ekadashi")
	if ekadashi.Tithi == nil || ekadashi.Tithi.LunarMonth != "magha" || ekadashi.Tithi.Paksha != "krishna" || ekadashi.Tithi.Number != 11 {
		t.Errorf("shattila tithi = %+v", ekadashi.Tithi)
	}
	if ekadashi.Parana == nil || ekadashi.Parana.StartAt.Format(time.RFC3339) != "2027-02-03T01:38:00Z" {
		t.Errorf("shattila parana = %+v", ekadashi.Parana)
	}
	if ekadashi.SunriseAt.Format(time.RFC3339) != "2027-02-02T01:39:00Z" {
		t.Errorf("sunrise = %v", ekadashi.SunriseAt)
	}

	if p := eventByID(t, events, "2027-02-03_budha-pradosh-vrat"); p.Puja == nil || p.Category != "pradosh" {
		t.Errorf("pradosh = %+v", p)
	}
	if s := eventByID(t, events, "2027-02-04_masik-shivaratri"); s.NishitaStartAt == nil {
		t.Errorf("shivaratri missing nishita")
	}
	if s := eventByID(t, events, "2027-02-24_dwijapriya-sankashti"); s.MoonriseAt == nil {
		t.Errorf("sankashti missing moonrise")
	}
	kumbha := eventByID(t, events, "2027-02-13_kumbha-sankranti")
	if kumbha.Tithi != nil || kumbha.SankrantiAt == nil {
		t.Errorf("kumbha sankranti = %+v", kumbha)
	}

	// Lunar eclipse: tithi from the "occurs during Purnima" text (no month) + all three eclipse times.
	lunar := eventByID(t, events, "2027-02-21_chandra-grahan-upachchaya")
	if lunar.Tithi == nil || lunar.Tithi.LunarMonth != "" || lunar.Tithi.Paksha != "shukla" || lunar.Tithi.Number != 15 {
		t.Errorf("lunar eclipse tithi = %+v", lunar.Tithi)
	}
	if lunar.Eclipse == nil {
		t.Errorf("lunar eclipse missing timings")
	}
	// Solar eclipse with no visible timings still has a tithi window but no eclipse group.
	solar := eventByID(t, events, "2027-02-06_surya-grahan-valayakara")
	if solar.Eclipse != nil || solar.Tithi == nil || solar.Tithi.Paksha != "krishna" {
		t.Errorf("solar eclipse = %+v", solar)
	}
}

func TestBuildEvents_RowErrors(t *testing.T) {
	cases := []struct {
		name   string
		event  string // English name of the row to mutate
		mutate func(f map[string]string)
		want   string
	}{
		{"bad date", "Mauni Amavasya", func(f map[string]string) { f["Date"] = "2027-13-45" }, "invalid Date"},
		{"unknown category", "Mauni Amavasya", func(f map[string]string) { f["Category (English)"] = "Party" }, "unknown category"},
		{"missing icon", "Mauni Amavasya", func(f map[string]string) { f["Image URL"] = "" }, "Image URL is required"},
		{"bad time", "Mauni Amavasya", func(f map[string]string) { f["Sunrise (UTC)"] = "6am" }, "invalid time"},
		{"half puja window", "Budha Pradosh Vrat", func(f map[string]string) { f["Puja End (UTC)"] = "" }, "both be set or both empty"},
		{"partial eclipse", "Chandra Grahan (Upachchaya)", func(f map[string]string) { f["Eclipse End (UTC)"] = "" }, "all be set or all empty"},
		{"tithi text without times", "Mauni Amavasya", func(f map[string]string) { f["Tithi Start (UTC)"], f["Tithi End (UTC)"] = "", "" }, "not both set"},
		{"tithi times without text", "Kumbha Sankranti", func(f map[string]string) {
			f["Tithi Start (UTC)"], f["Tithi End (UTC)"] = "2027-02-13 01:00", "2027-02-13 02:00"
		}, "Tithi (English) is empty"},
		{"garbage tithi", "Mauni Amavasya", func(f map[string]string) { f["Tithi (English)"] = "soon" }, "not \"Month, Paksha Tithi\""},
		{"unknown lunar month", "Mauni Amavasya", func(f map[string]string) { f["Tithi (English)"] = "Smarch, Krishna Amavasya" }, "unknown lunar month"},
		{"purnima in krishna paksha", "Mauni Amavasya", func(f map[string]string) { f["Tithi (English)"] = "Magha, Krishna Purnima" }, "unknown tithi"},
		{"eclipse text on a non-eclipse row", "Mauni Amavasya", func(f map[string]string) { f["Tithi (English)"] = "occurs during Amavasya" }, "not \"Month, Paksha Tithi\""},
		{"derived weekday wrong", "Mauni Amavasya", func(f map[string]string) { f["Weekday (English)"] = "Funday" }, "Weekday (English)"},
		{"derived Hindi month wrong", "Mauni Amavasya", func(f map[string]string) { f["Month (Hindi)"] = "फरवरी" }, "Month (Hindi)"}, // no nukta
		{"derived category wrong", "Mauni Amavasya", func(f map[string]string) { f["Category (Hindi)"] = "उत्सव" }, "Category (Hindi)"},
		{"derived tithi Hindi wrong", "Mauni Amavasya", func(f map[string]string) { f["Tithi (Hindi)"] = "माघ, कृष्ण अष्टमी" }, "Tithi (Hindi)"},
		{"date outside tithi window", "Mauni Amavasya", func(f map[string]string) { f["Date"] = "2027-03-20" }, "outside its tithi window"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := cloneRows(fixtureRows(t))
			tc.mutate(findRow(t, rows, tc.event).Fields)
			_, _, err := BuildEvents(rows)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBuildEvents_DatasetChecks(t *testing.T) {
	t.Run("rows on one date must agree on sunrise", func(t *testing.T) {
		rows := cloneRows(fixtureRows(t))
		findRow(t, rows, "Surya Grahan (Valayakara)").Fields["Sunrise (UTC)"] = "2027-02-06 01:40"
		_, _, err := BuildEvents(rows)
		if err == nil || !strings.Contains(err.Error(), "disagree on sunrise/sunset") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("hindi and english names must map 1:1", func(t *testing.T) {
		rows := cloneRows(fixtureRows(t))
		findRow(t, rows, "Masik Shivaratri").Fields["Event (Hindi)"] = "कुछ और"
		// Make another row reuse the English name with the original Hindi name.
		dup := cloneRows([]Row{*findRow(t, rows, "Masik Karthigai")})[0]
		dup.Fields["Event (English)"] = "Masik Shivaratri"
		rows = append(rows, dup)
		_, _, err := BuildEvents(rows)
		if err == nil || !strings.Contains(err.Error(), "two Hindi names") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("krishna paksha month must match the following shukla month", func(t *testing.T) {
		rows := cloneRows(fixtureRows(t))
		f := findRow(t, rows, "Mauni Amavasya").Fields
		f["Tithi (English)"] = "Pausha, Krishna Amavasya"
		f["Tithi (Hindi)"] = "पौष, कृष्ण अमावस्या"
		_, _, err := BuildEvents(rows)
		if err == nil || !strings.Contains(err.Error(), "next shukla row") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("duplicate id", func(t *testing.T) {
		rows := cloneRows(fixtureRows(t))
		rows = append(rows, cloneRows([]Row{*findRow(t, rows, "Mauni Amavasya")})[0])
		_, _, err := BuildEvents(rows)
		if err == nil || !strings.Contains(err.Error(), "duplicate id") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("two icon URLs with one file name", func(t *testing.T) {
		rows := cloneRows(fixtureRows(t))
		f := findRow(t, rows, "Mauni Amavasya").Fields
		f["Image URL"] = "https://other.example.com/x/sj-icon-ekadashi.png"
		_, _, err := BuildEvents(rows)
		if err == nil || !strings.Contains(err.Error(), "used by two different URLs") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestBuildEvents_UppercaseEclipseTextHasNoWarning(t *testing.T) {
	rows := cloneRows(fixtureRows(t))
	for i := range rows {
		if strings.HasPrefix(rows[i].Fields["Tithi (English)"], "occurs during") {
			rows[i].Fields["Tithi (English)"] = "O" + rows[i].Fields["Tithi (English)"][1:]
		}
	}
	_, rep, err := BuildEvents(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("warnings = %v", rep.Warnings)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Diwali / Deepavali":                      "diwali-deepavali",
		"Rama Ekadashi":                           "rama-ekadashi",
		"Surya Grahan (Valayakara)":               "surya-grahan-valayakara",
		"Sawan Somwar Vrat 1 (North India)":       "sawan-somwar-vrat-1-north-india",
		"  Hanuman Jayanti / Hanuman Janmotsava ": "hanuman-jayanti-hanuman-janmotsava",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func mkEvent(date, name string) hc.Event {
	d, _ := time.Parse(hc.DateLayout, date)
	return hc.Event{
		ID: date + "_" + Slugify(name), Date: date, Category: "festival",
		IconFileKey: "hindu-events/icons/x.png",
		Name:        hc.Text{Hi: "नाम", En: name}, Description: hc.Text{Hi: "विवरण", En: "desc"},
		SunriseAt: d.Add(time.Hour), SunsetAt: d.Add(12 * time.Hour),
	}
}

func ids(events []hc.Event) string {
	var s []string
	for _, e := range events {
		s = append(s, e.ID)
	}
	return strings.Join(s, ",")
}

func TestMerge(t *testing.T) {
	existing := []hc.Event{mkEvent("2026-11-01", "A"), mkEvent("2026-12-01", "B"), mkEvent("2027-01-01", "C")}
	incoming := []hc.Event{mkEvent("2026-12-15", "D"), mkEvent("2026-12-01", "B2")}

	t.Run("merge replaces the incoming date range and keeps the rest", func(t *testing.T) {
		got, err := Merge(existing, incoming, "merge")
		if err != nil {
			t.Fatal(err)
		}
		// B (inside 12-01..12-15) is gone; A and C (outside) stay; D and B2 are added, sorted.
		want := "2026-11-01_a,2026-12-01_b2,2026-12-15_d,2027-01-01_c"
		if ids(got) != want {
			t.Errorf("merge = %s, want %s", ids(got), want)
		}
	})
	t.Run("appending a new year keeps everything", func(t *testing.T) {
		got, err := Merge(existing, []hc.Event{mkEvent("2028-01-01", "New Year")}, "merge")
		if err != nil {
			t.Fatal(err)
		}
		if want := "2026-11-01_a,2026-12-01_b,2027-01-01_c,2028-01-01_new-year"; ids(got) != want {
			t.Errorf("merge = %s, want %s", ids(got), want)
		}
	})
	t.Run("replace keeps only the incoming events", func(t *testing.T) {
		got, err := Merge(existing, incoming, "replace")
		if err != nil {
			t.Fatal(err)
		}
		if want := "2026-12-01_b2,2026-12-15_d"; ids(got) != want {
			t.Errorf("replace = %s, want %s", ids(got), want)
		}
	})
	t.Run("merge into an empty file", func(t *testing.T) {
		got, err := Merge(nil, incoming, "merge")
		if err != nil || len(got) != 2 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	t.Run("errors", func(t *testing.T) {
		if _, err := Merge(existing, nil, "merge"); err == nil {
			t.Error("want error for no incoming events")
		}
		if _, err := Merge(existing, incoming, "sideways"); err == nil {
			t.Error("want error for unknown mode")
		}
	})
}

func TestMergeIsDeterministic(t *testing.T) {
	a, _ := Merge(nil, []hc.Event{mkEvent("2027-01-02", "B"), mkEvent("2027-01-01", "A")}, "replace")
	b, _ := Merge(nil, []hc.Event{mkEvent("2027-01-01", "A"), mkEvent("2027-01-02", "B")}, "replace")
	da, _ := hc.Marshal(a)
	db, _ := hc.Marshal(b)
	if !bytes.Equal(da, db) {
		t.Error("output depends on input order")
	}
	if !bytes.HasSuffix(da, []byte("\n")) {
		t.Error("output should end with a newline")
	}
}

// TestFullCSVMatchesCommittedJSON regenerates the data from the full source CSV and compares it
// with the committed file. It needs the CSV, which is not in the repo, so it only runs when
// HINDU_CALENDAR_CSV points at it:
//
//	HINDU_CALENDAR_CSV=~/Downloads/hindu_festival_calendar_nov-dec_2026_and_2027_v2.csv go test ./scripts/generate_hindu_calendar/
func TestFullCSVMatchesCommittedJSON(t *testing.T) {
	path := os.Getenv("HINDU_CALENDAR_CSV")
	if path == "" {
		t.Skip("HINDU_CALENDAR_CSV not set")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, _, err := ReadCSV(f)
	if err != nil {
		t.Fatal(err)
	}
	events, rep, err := BuildEvents(rows)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Events != 317 || rep.WithoutTithi != 49 || len(rep.Icons) != 56 {
		t.Errorf("report = %+v", rep)
	}
	got, err := hc.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../internal/apps/sanskaar/hinducalendar/data/hindu_events.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("committed hindu_events.json differs from what the CSV generates; re-run the generator")
	}
}
