package service

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	hc "go-backend/internal/apps/sanskaar/hinducalendar"
)

const testIconBase = "https://cdn.test"

// newTestHinduService serves the real embedded data with a fixed clock.
func newTestHinduService(now string) HinduEventService {
	t, err := time.Parse(time.RFC3339, now)
	if err != nil {
		panic(err)
	}
	return NewHinduEventService(hc.MustLoad(), testIconBase, func() time.Time { return t })
}

func TestTodayOrUpcoming_TodayHasEvents(t *testing.T) {
	svc := newTestHinduService("2026-11-16T10:00:00+05:30")
	resp := svc.TodayOrUpcoming(hc.LangEn)
	if resp.Date == nil || *resp.Date != "2026-11-16" || !resp.IsToday || len(resp.Events) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	c := resp.Events[0]
	if c.ID != "2026-11-16_vrishchika-sankranti" || c.Name != "Vrishchika Sankranti" ||
		c.Category != "auspicious_day" || c.CategoryLabel != "Auspicious Day" ||
		c.IconURL != testIconBase+"/hindu-events/icons/sj-icon-sankranti.png" ||
		c.Date != "2026-11-16" || c.Weekday != 1 || c.WeekdayLabel != "Monday" || c.MonthLabel != "November" {
		t.Errorf("card = %+v", c)
	}

	hi := svc.TodayOrUpcoming(hc.LangHi).Events[0]
	if hi.Name != "वृश्चिक संक्रांति" || hi.CategoryLabel != "शुभ दिन" || hi.WeekdayLabel != "सोम" || hi.MonthLabel != "नवंबर" {
		t.Errorf("hindi card = %+v", hi)
	}
}

func TestTodayOrUpcoming_NextDateWhenTodayIsEmpty(t *testing.T) {
	// 2026-11-14 has no events; the next date with events is 2026-11-15 (Chhath Puja).
	resp := newTestHinduService("2026-11-14T12:00:00+05:30").TodayOrUpcoming(hc.LangEn)
	if resp.Date == nil || *resp.Date != "2026-11-15" || resp.IsToday {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.Events) != 1 || resp.Events[0].ID != "2026-11-15_chhath-puja" || resp.Events[0].Weekday != 7 || resp.Events[0].WeekdayLabel != "Sunday" {
		t.Errorf("events = %+v", resp.Events)
	}
}

func TestTodayOrUpcoming_AllEventsOfADate(t *testing.T) {
	// 2027-08-17 has seven events; all of them are returned, festivals first.
	resp := newTestHinduService("2027-08-17T08:00:00+05:30").TodayOrUpcoming(hc.LangEn)
	if resp.Date == nil || *resp.Date != "2027-08-17" || !resp.IsToday || len(resp.Events) != 7 {
		t.Fatalf("resp = %+v", resp)
	}
	var cats []string
	for _, e := range resp.Events {
		cats = append(cats, e.Category)
	}
	want := "festival,festival,festival,eclipse,purnima,vrat,auspicious_day"
	if strings.Join(cats, ",") != want {
		t.Errorf("category order = %v, want %s", cats, want)
	}
}

func TestTodayOrUpcoming_UsesISTForToday(t *testing.T) {
	// 20:00 UTC on Nov 15 is 01:30 IST on Nov 16, so "today" is already the 16th.
	resp := newTestHinduService("2026-11-15T20:00:00Z").TodayOrUpcoming(hc.LangEn)
	if resp.Date == nil || *resp.Date != "2026-11-16" || !resp.IsToday {
		t.Fatalf("resp = %+v", resp)
	}
	// 17:00 UTC on Nov 15 is 22:30 IST on Nov 15: still the 15th.
	resp = newTestHinduService("2026-11-15T17:00:00Z").TodayOrUpcoming(hc.LangEn)
	if resp.Date == nil || *resp.Date != "2026-11-15" || !resp.IsToday {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestTodayOrUpcoming_AfterTheData(t *testing.T) {
	resp := newTestHinduService("2028-01-01T12:00:00+05:30").TodayOrUpcoming(hc.LangEn)
	if resp.Date != nil || resp.IsToday || resp.Events == nil || len(resp.Events) != 0 {
		t.Fatalf("resp = %+v (events must be an empty, non-nil slice)", resp)
	}
	b, _ := json.Marshal(resp)
	if string(b) != `{"date":null,"is_today":false,"events":[]}` {
		t.Errorf("json = %s", b)
	}
}

func TestList_DefaultStartIsToday(t *testing.T) {
	svc := newTestHinduService("2026-11-15T09:00:00+05:30")
	res := svc.List(HinduEventListParams{Lang: hc.LangEn, Page: 1, PageSize: 3})
	// 317 events in total, 13 of them before 2026-11-15.
	if res.Total != 304 || res.TotalPages != 102 || res.Page != 1 || res.PageSize != 3 {
		t.Fatalf("meta = total %d pages %d page %d size %d", res.Total, res.TotalPages, res.Page, res.PageSize)
	}
	if res.NextPage == nil || *res.NextPage != 2 || res.PrevPage != nil {
		t.Errorf("next/prev = %v/%v", res.NextPage, res.PrevPage)
	}
	var ids []string
	for _, c := range res.Data {
		ids = append(ids, c.ID)
	}
	if want := "2026-11-15_chhath-puja,2026-11-16_vrishchika-sankranti,2026-11-17_masik-durgashtami"; strings.Join(ids, ",") != want {
		t.Errorf("ids = %v, want %s", ids, want)
	}
}

func TestList_ExplicitStartCanReachIntoThePast(t *testing.T) {
	svc := newTestHinduService("2026-11-15T09:00:00+05:30")
	res := svc.List(HinduEventListParams{Lang: hc.LangEn, StartDate: "2026-11-01", Page: 1, PageSize: 100})
	if res.Total != 317 || len(res.Data) != 100 || res.Data[0].Date != "2026-11-01" {
		t.Errorf("total %d, len %d, first %s", res.Total, len(res.Data), res.Data[0].Date)
	}
}

func TestList_DateWindowAndCategory(t *testing.T) {
	svc := newTestHinduService("2026-11-15T09:00:00+05:30")

	aug := svc.List(HinduEventListParams{Lang: hc.LangHi, StartDate: "2027-08-01", EndDate: "2027-08-31", Page: 1, PageSize: 100})
	var on17 int
	for _, c := range aug.Data {
		if c.Date < "2027-08-01" || c.Date > "2027-08-31" {
			t.Errorf("%s outside the window", c.ID)
		}
		if c.Date == "2027-08-17" {
			on17++
		}
	}
	if on17 != 7 || int(aug.Total) != len(aug.Data) {
		t.Errorf("Aug 17 events = %d, total %d vs %d rows", on17, aug.Total, len(aug.Data))
	}

	ek := svc.List(HinduEventListParams{Lang: hc.LangEn, StartDate: "2027-01-01", Categories: []string{"ekadashi"}, Page: 2, PageSize: 5})
	if ek.Page != 2 || len(ek.Data) != 5 || ek.PrevPage == nil || *ek.PrevPage != 1 {
		t.Fatalf("page 2 = %+v", ek)
	}
	for _, c := range ek.Data {
		if c.Category != "ekadashi" || c.CategoryLabel != "Ekadashi" {
			t.Errorf("unexpected card %+v", c)
		}
	}
}

func TestList_PaginationBoundaries(t *testing.T) {
	svc := newTestHinduService("2026-11-15T09:00:00+05:30")

	last := svc.List(HinduEventListParams{Lang: hc.LangEn, Page: 102, PageSize: 3}) // 304 = 101*3 + 1
	if len(last.Data) != 1 || last.NextPage != nil || last.PrevPage == nil || *last.PrevPage != 101 {
		t.Errorf("last page = len %d next %v prev %v", len(last.Data), last.NextPage, last.PrevPage)
	}

	past := svc.List(HinduEventListParams{Lang: hc.LangEn, Page: 103, PageSize: 3})
	if past.Data == nil || len(past.Data) != 0 || past.NextPage != nil || past.Total != 304 {
		t.Errorf("page past the end = %+v", past)
	}
	b, _ := json.Marshal(past)
	if !strings.Contains(string(b), `"data":[]`) {
		t.Errorf("empty page must encode data as [], got %s", b)
	}

	clamped := svc.List(HinduEventListParams{Lang: hc.LangEn, Page: 0, PageSize: 5000})
	if clamped.Page != 1 || clamped.PageSize != 100 || len(clamped.Data) != 100 {
		t.Errorf("clamped = page %d size %d len %d", clamped.Page, clamped.PageSize, len(clamped.Data))
	}
	defaulted := svc.List(HinduEventListParams{Lang: hc.LangEn})
	if defaulted.PageSize != 10 || len(defaulted.Data) != 10 {
		t.Errorf("defaulted = size %d len %d", defaulted.PageSize, len(defaulted.Data))
	}

	after := svc.List(HinduEventListParams{Lang: hc.LangEn, StartDate: "2028-01-01", Page: 1, PageSize: 10})
	if after.Total != 0 || after.TotalPages != 0 || len(after.Data) != 0 || after.NextPage != nil {
		t.Errorf("after the data = %+v", after)
	}
}

func TestGetByID_FullEvent(t *testing.T) {
	svc := newTestHinduService("2026-11-15T09:00:00+05:30")
	d, ok := svc.GetByID("2026-11-05_rama-ekadashi", hc.LangHi)
	if !ok {
		t.Fatal("event not found")
	}
	if d.Name != "रमा एकादशी" || d.CategoryLabel != "एकादशी" || d.WeekdayLabel != "गुरु" || d.Weekday != 4 || d.MonthLabel != "नवंबर" {
		t.Errorf("card part = %+v", d.HinduEventCard)
	}
	if d.Description != "कार्तिक कृष्ण एकादशी-रमा एकादशी।" {
		t.Errorf("description = %q", d.Description)
	}
	if d.SunriseAt.Format(time.RFC3339) != "2026-11-05T01:06:00Z" || d.SunsetAt.Format(time.RFC3339) != "2026-11-05T12:03:00Z" {
		t.Errorf("sun = %v / %v", d.SunriseAt, d.SunsetAt)
	}
	tt := d.Tithi
	if tt == nil || tt.Label != "कार्तिक, कृष्ण एकादशी" || tt.LunarMonth == nil || *tt.LunarMonth != "kartika" ||
		tt.Paksha != "krishna" || tt.Number != 11 ||
		tt.StartAt.Format(time.RFC3339) != "2026-11-04T05:33:00Z" || tt.EndAt.Format(time.RFC3339) != "2026-11-05T05:05:00Z" {
		t.Errorf("tithi = %+v", tt)
	}
	if d.Parana == nil || d.Parana.StartAt.Format(time.RFC3339) != "2026-11-06T01:07:00Z" || d.Parana.EndAt.Format(time.RFC3339) != "2026-11-06T03:18:00Z" {
		t.Errorf("parana = %+v", d.Parana)
	}
	if d.Puja != nil || d.NishitaStartAt != nil || d.MoonriseAt != nil || d.SankrantiAt != nil || d.Eclipse != nil {
		t.Errorf("absent groups must be nil: %+v", d)
	}

	// Every optional group is an explicit null in the JSON, not a missing key.
	b, _ := json.Marshal(d)
	for _, key := range []string{`"puja":null`, `"nishita_start_at":null`, `"moonrise_at":null`, `"sankranti_at":null`, `"eclipse":null`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("json lacks %s: %s", key, b)
		}
	}
}

func TestGetByID_NoTithiAndSankranti(t *testing.T) {
	d, ok := newTestHinduService("2026-11-15T09:00:00+05:30").GetByID("2026-11-16_vrishchika-sankranti", hc.LangEn)
	if !ok {
		t.Fatal("event not found")
	}
	if d.Tithi != nil || d.SankrantiAt == nil || d.SankrantiAt.Format(time.RFC3339) != "2026-11-16T14:22:00Z" {
		t.Errorf("detail = %+v", d)
	}
	b, _ := json.Marshal(d)
	if !strings.Contains(string(b), `"tithi":null`) {
		t.Errorf("tithi must be an explicit null: %s", b)
	}
}

func TestGetByID_Eclipse(t *testing.T) {
	svc := newTestHinduService("2026-11-15T09:00:00+05:30")

	lunar, ok := svc.GetByID("2027-02-21_chandra-grahan-upachchaya", hc.LangEn)
	if !ok {
		t.Fatal("event not found")
	}
	if lunar.Tithi == nil || lunar.Tithi.Label != "Occurs during Purnima" || lunar.Tithi.LunarMonth != nil {
		t.Errorf("lunar eclipse tithi = %+v", lunar.Tithi)
	}
	if lunar.Eclipse == nil || lunar.Eclipse.PeakAt.Format(time.RFC3339) != "2027-02-20T23:13:00Z" {
		t.Errorf("lunar eclipse = %+v", lunar.Eclipse)
	}
	b, _ := json.Marshal(lunar.Tithi)
	if !strings.Contains(string(b), `"lunar_month":null`) {
		t.Errorf("eclipse tithi must carry lunar_month:null: %s", b)
	}

	solar, ok := svc.GetByID("2027-02-06_surya-grahan-valayakara", hc.LangHi)
	if !ok {
		t.Fatal("event not found")
	}
	if solar.Eclipse != nil || solar.Tithi == nil || solar.Tithi.Label != "अमावस्या के दौरान" {
		t.Errorf("solar eclipse = %+v / %+v", solar.Eclipse, solar.Tithi)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	if d, ok := newTestHinduService("2026-11-15T09:00:00+05:30").GetByID("2026-11-05_nope", hc.LangEn); ok || d != nil {
		t.Errorf("unknown id returned %v, %v", d, ok)
	}
}

func TestList_HugePageDoesNotWrapAround(t *testing.T) {
	svc := newTestHinduService("2026-11-15T09:00:00+05:30")
	for _, page := range []int{math.MaxInt, math.MaxInt - 1, math.MaxInt/10 + 2} {
		r := svc.List(HinduEventListParams{Lang: hc.LangEn, Page: page, PageSize: 10})
		if len(r.Data) != 0 || r.Page != page || r.Total != 304 {
			t.Errorf("page %d: got %d rows, page=%d, total=%d; want an empty page", page, len(r.Data), r.Page, r.Total)
		}
	}
	if got := pageOffset(1, 10); got != 0 {
		t.Errorf("pageOffset(1,10) = %d", got)
	}
	if got := pageOffset(3, 10); got != 20 {
		t.Errorf("pageOffset(3,10) = %d", got)
	}
}
