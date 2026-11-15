// Package hinducalendar holds the Hindu calendar events (festivals, vrats, ekadashi, eclipses...)
// shown in the Sanskaar app. The data is static, so it lives in one JSON file that is embedded
// in the binary (data/hindu_events.json), validated at startup and served from memory.
// The file is produced by scripts/generate_hindu_calendar from the source CSV and may also be edited
// by hand; Validate is the single definition of what a well-formed file looks like.
package hinducalendar

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"sync"
	"time"

	_ "embed"
)

//go:embed data/hindu_events.json
var embeddedData []byte

// DateLayout is the layout of Event.Date and of the start_date/end_date query parameters.
const DateLayout = "2006-01-02"

// IST is the zone of the event dates: "today" is the current date in IST. IST has no DST, and
// FixedZone avoids a dependency on system tzdata.
var IST = time.FixedZone("IST", 19800)

// Text is a value in each supported language.
type Text struct {
	Hi string `json:"hi"`
	En string `json:"en"`
}

// Get returns the text for lang.
func (t Text) Get(lang Lang) string {
	if lang == LangHi {
		return t.Hi
	}
	return t.En
}

// Window is a start/end pair of instants (UTC).
type Window struct {
	StartAt time.Time `json:"start_at"`
	EndAt   time.Time `json:"end_at"`
}

// Tithi is the lunar day in effect for the event. LunarMonth is empty on eclipse rows.
type Tithi struct {
	LunarMonth string    `json:"lunar_month,omitempty"`
	Paksha     string    `json:"paksha"`
	Number     int       `json:"number"`
	StartAt    time.Time `json:"start_at"`
	EndAt      time.Time `json:"end_at"`
}

// Eclipse holds the eclipse timings (UTC).
type Eclipse struct {
	StartAt time.Time `json:"start_at"`
	PeakAt  time.Time `json:"peak_at"`
	EndAt   time.Time `json:"end_at"`
}

// Event is one event on one date. Date is the IST calendar date of the observance (it labels
// the day and carries no timezone); every *_at field is an absolute instant in UTC.
type Event struct {
	ID          string `json:"id"` // <date>_<slugified English name>
	Date        string `json:"date"`
	Category    string `json:"category"`
	IconFileKey string `json:"icon_file_key"`
	Name        Text   `json:"name"`
	Description Text   `json:"description"`

	SunriseAt time.Time `json:"sunrise_at"`
	SunsetAt  time.Time `json:"sunset_at"`

	Tithi          *Tithi     `json:"tithi,omitempty"`
	Puja           *Window    `json:"puja,omitempty"`
	NishitaStartAt *time.Time `json:"nishita_start_at,omitempty"`
	MoonriseAt     *time.Time `json:"moonrise_at,omitempty"`
	SankrantiAt    *time.Time `json:"sankranti_at,omitempty"`
	Parana         *Window    `json:"parana,omitempty"`
	Eclipse        *Eclipse   `json:"eclipse,omitempty"`
}

// Day returns the event date as midnight UTC (only the calendar date is meaningful).
func (e *Event) Day() time.Time {
	d, _ := time.Parse(DateLayout, e.Date)
	return d
}

var idPattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})_[a-z0-9]+(-[a-z0-9]+)*$`)

// Sort orders events by date, then category display order, then id. This is the order the
// data file must be in and the order every API list uses.
func Sort(events []Event) {
	sort.SliceStable(events, func(i, j int) bool { return less(&events[i], &events[j]) })
}

func less(a, b *Event) bool {
	if a.Date != b.Date {
		return a.Date < b.Date
	}
	if ra, rb := CategoryRank(a.Category), CategoryRank(b.Category); ra != rb {
		return ra < rb
	}
	return a.ID < b.ID
}

// Marshal encodes events deterministically (sorted by the caller, 2-space indent, no HTML
// escaping, trailing newline) so regenerating an unchanged file gives a byte-identical result.
func Marshal(events []Event) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if events == nil {
		events = []Event{}
	}
	if err := enc.Encode(events); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const maxReportedErrors = 25

// Validate checks every rule a data file must satisfy (the data file format is defined by this function and the Event type). It returns
// all problems found (up to a limit) joined into one error.
func Validate(events []Event) error {
	var errs []error
	add := func(e *Event, format string, args ...any) {
		if len(errs) < maxReportedErrors {
			errs = append(errs, fmt.Errorf("event %q: %s", e.ID, fmt.Sprintf(format, args...)))
		}
	}

	seen := make(map[string]bool, len(events))
	for i := range events {
		e := &events[i]

		m := idPattern.FindStringSubmatch(e.ID)
		switch {
		case m == nil:
			add(e, "id must look like YYYY-MM-DD_slug")
		case m[1] != e.Date:
			add(e, "id date prefix %s does not match date %s", m[1], e.Date)
		}
		if _, err := time.Parse(DateLayout, e.Date); err != nil {
			add(e, "invalid date %q, expected YYYY-MM-DD", e.Date)
		}
		if seen[e.ID] {
			add(e, "duplicate id")
		}
		seen[e.ID] = true

		if !IsCategory(e.Category) {
			add(e, "unknown category %q", e.Category)
		}
		if e.IconFileKey == "" {
			add(e, "icon_file_key is required")
		}
		if e.Name.Hi == "" || e.Name.En == "" {
			add(e, "name needs both hi and en")
		}
		if e.Description.Hi == "" || e.Description.En == "" {
			add(e, "description needs both hi and en")
		}

		checkUTC := func(label string, t time.Time) {
			if t.IsZero() {
				add(e, "%s is required", label)
			} else if _, off := t.Zone(); off != 0 {
				add(e, "%s must be in UTC (end with Z)", label)
			}
		}
		checkUTC("sunrise_at", e.SunriseAt)
		checkUTC("sunset_at", e.SunsetAt)
		if !e.SunriseAt.IsZero() && !e.SunsetAt.After(e.SunriseAt) {
			add(e, "sunset_at must be after sunrise_at")
		}

		if t := e.Tithi; t != nil {
			if !slices.Contains(Pakshas, t.Paksha) {
				add(e, "tithi.paksha %q must be shukla or krishna", t.Paksha)
			}
			if t.Number < 1 || t.Number > 15 {
				add(e, "tithi.number %d must be 1..15", t.Number)
			}
			switch {
			case t.LunarMonth != "" && !slices.Contains(LunarMonths, t.LunarMonth):
				add(e, "tithi.lunar_month %q is not a lunar month", t.LunarMonth)
			case t.LunarMonth == "" && (e.Category != CategoryEclipse || t.Number != 15):
				add(e, "tithi.lunar_month is required (only eclipse rows, whose tithi is Amavasya or Purnima = number 15, may omit it)")
			}
			checkUTC("tithi.start_at", t.StartAt)
			checkUTC("tithi.end_at", t.EndAt)
			if !t.StartAt.IsZero() && !t.EndAt.After(t.StartAt) {
				add(e, "tithi.end_at must be after tithi.start_at")
			}
		}
		// Slices, not maps, so the reported errors come out in a stable order.
		for _, w := range []struct {
			name string
			win  *Window
		}{{"puja", e.Puja}, {"parana", e.Parana}} {
			if w.win == nil {
				continue
			}
			checkUTC(w.name+".start_at", w.win.StartAt)
			checkUTC(w.name+".end_at", w.win.EndAt)
			if !w.win.StartAt.IsZero() && !w.win.EndAt.After(w.win.StartAt) {
				add(e, "%s.end_at must be after %s.start_at", w.name, w.name)
			}
		}
		for _, p := range []struct {
			name string
			at   *time.Time
		}{{"nishita_start_at", e.NishitaStartAt}, {"moonrise_at", e.MoonriseAt}, {"sankranti_at", e.SankrantiAt}} {
			if p.at != nil {
				checkUTC(p.name, *p.at)
			}
		}
		if x := e.Eclipse; x != nil {
			if e.Category != CategoryEclipse {
				add(e, "eclipse timings are only allowed on eclipse rows")
			}
			checkUTC("eclipse.start_at", x.StartAt)
			checkUTC("eclipse.peak_at", x.PeakAt)
			checkUTC("eclipse.end_at", x.EndAt)
			if !x.PeakAt.After(x.StartAt) || !x.EndAt.After(x.PeakAt) {
				add(e, "eclipse needs start_at < peak_at < end_at")
			}
		}

		if i > 0 && !less(&events[i-1], e) {
			add(e, "rows must be sorted by date, category order, id (previous row is %q)", events[i-1].ID)
		}
	}
	return errors.Join(errs...)
}

// Store is the immutable in-memory calendar. Methods return pointers into its slice; callers
// must treat them as read-only.
type Store struct {
	events []Event
	byID   map[string]int
}

// Load parses and validates a data file.
func Load(data []byte) (*Store, error) {
	var events []Event
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&events); err != nil {
		return nil, fmt.Errorf("hindu calendar: invalid JSON: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("hindu calendar: invalid JSON: unexpected data after the event array")
	}
	if err := Validate(events); err != nil {
		return nil, fmt.Errorf("hindu calendar: invalid data:\n%w", err)
	}
	s := &Store{events: events, byID: make(map[string]int, len(events))}
	for i := range events {
		s.byID[events[i].ID] = i
	}
	return s, nil
}

var (
	embeddedOnce  sync.Once
	embeddedStore *Store
	embeddedErr   error
)

// MustLoad loads the embedded data file once. It panics if the file is malformed, so a bad
// data file fails the deploy at startup instead of serving wrong data.
func MustLoad() *Store {
	embeddedOnce.Do(func() {
		embeddedStore, embeddedErr = Load(embeddedData)
		if embeddedErr == nil && embeddedStore.Len() == 0 {
			embeddedErr = errors.New("hindu calendar: the embedded data file has no events")
		}
	})
	if embeddedErr != nil {
		panic(embeddedErr)
	}
	return embeddedStore
}

// Len is the number of events.
func (s *Store) Len() int { return len(s.events) }

// FirstDate / LastDate are the first and last event dates ("" when empty).
func (s *Store) FirstDate() string {
	if len(s.events) == 0 {
		return ""
	}
	return s.events[0].Date
}

func (s *Store) LastDate() string {
	if len(s.events) == 0 {
		return ""
	}
	return s.events[len(s.events)-1].Date
}

// Get returns the event with the given id.
func (s *Store) Get(id string) (*Event, bool) {
	i, ok := s.byID[id]
	if !ok {
		return nil, false
	}
	return &s.events[i], true
}

// firstIndexOnOrAfter is the index of the first event whose date is >= date.
func (s *Store) firstIndexOnOrAfter(date string) int {
	return sort.Search(len(s.events), func(i int) bool { return s.events[i].Date >= date })
}

// OnOrAfter returns all events on the first date >= date that has any (nil if none).
func (s *Store) OnOrAfter(date string) []*Event {
	i := s.firstIndexOnOrAfter(date)
	if i >= len(s.events) {
		return nil
	}
	day := s.events[i].Date
	var out []*Event
	for ; i < len(s.events) && s.events[i].Date == day; i++ {
		out = append(out, &s.events[i])
	}
	return out
}

// Query selects events. Empty StartDate/EndDate mean unbounded; empty Categories means all.
// Offset/Limit page the matches; total is the number of matches before paging.
type Query struct {
	StartDate  string
	EndDate    string
	Categories []string
	Offset     int
	Limit      int
}

// Query returns one page of matching events (in data order) and the total match count.
func (s *Store) Query(q Query) (page []*Event, total int) {
	start := 0
	if q.StartDate != "" {
		start = s.firstIndexOnOrAfter(q.StartDate)
	}
	for i := start; i < len(s.events); i++ {
		e := &s.events[i]
		if q.EndDate != "" && e.Date > q.EndDate {
			break
		}
		if len(q.Categories) > 0 && !slices.Contains(q.Categories, e.Category) {
			continue
		}
		if total >= q.Offset && len(page) < q.Limit {
			page = append(page, e)
		}
		total++
	}
	return page, total
}

// coverageWarnDays is how far before the last date a warning is logged.
const coverageWarnDays = 90

// Coverage describes the covered date range for a startup log line and reports whether the
// data runs out within coverageWarnDays of today (so a new CSV must be merged soon).
func (s *Store) Coverage(today time.Time) (summary string, warn bool) {
	if len(s.events) == 0 {
		return "hindu calendar loaded: no events", true
	}
	summary = fmt.Sprintf("hindu calendar loaded: %d events, %s..%s", len(s.events), s.FirstDate(), s.LastDate())
	last, err := time.Parse(DateLayout, s.LastDate())
	if err != nil {
		return summary, true
	}
	todayDate := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	return summary, last.Before(todayDate.AddDate(0, 0, coverageWarnDays))
}
