package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	hc "go-backend/internal/apps/sanskaar/hinducalendar"
	"go-backend/internal/apps/sanskaar/service"

	"github.com/gin-gonic/gin"
)

const hinduBase = "/api/v1/sanskaar/hindu-events"

// newHinduRouter registers the real Sanskaar routes (the other handlers are nil: their method
// values are only bound, never called) with a fixed clock of 2026-11-15, 10:00 IST.
func newHinduRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.FixedZone("IST", 19800))
	svc := service.NewHinduEventService(hc.MustLoad(), "https://cdn.test", func() time.Time { return now })
	r := gin.New()
	RegisterSanskaarRoutes(r.Group("/api/v1"), nil, nil, nil, nil, nil, nil, NewHinduEventHandler(svc))
	return r
}

func getJSON(t *testing.T, r *gin.Engine, url string) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("GET %s: body is not a JSON object: %v\n%s", url, err, w.Body.String())
	}
	return w.Code, body
}

func TestHinduEvents_TodayOrUpcoming(t *testing.T) {
	r := newHinduRouter(t)

	code, body := getJSON(t, r, hinduBase+"/today-or-upcoming?language_code=en")
	if code != 200 || body["date"] != "2026-11-15" || body["is_today"] != true {
		t.Fatalf("%d %v", code, body)
	}
	events := body["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("events = %v", events)
	}
	card := events[0].(map[string]any)
	want := map[string]any{
		"id": "2026-11-15_chhath-puja", "name": "Chhath Puja", "category": "festival", "category_label": "Festival",
		"icon_url": "https://cdn.test/hindu-events/icons/sj-icon-chhath.png", "date": "2026-11-15",
		"weekday": float64(7), "weekday_label": "Sunday", "month_label": "November",
	}
	for k, v := range want {
		if card[k] != v {
			t.Errorf("card[%s] = %v, want %v", k, card[k], v)
		}
	}
	if len(card) != len(want) {
		t.Errorf("card has unexpected keys: %v", card)
	}

	// The static route must not be swallowed by /:id.
	if _, ok := body["error"]; ok {
		t.Error("today-or-upcoming was routed to /:id")
	}
}

func TestHinduEvents_List(t *testing.T) {
	r := newHinduRouter(t)

	// Default: from today (2026-11-15) onwards, first page of 10.
	code, body := getJSON(t, r, hinduBase+"?language_code=hi")
	if code != 200 || body["total"] != float64(304) || body["page"] != float64(1) || body["page_size"] != float64(10) || body["next_page"] != float64(2) {
		t.Fatalf("%d %v", code, body)
	}
	if _, has := body["prev_page"]; has {
		t.Error("prev_page must be omitted on the first page")
	}
	first := body["data"].([]any)[0].(map[string]any)
	if first["date"] != "2026-11-15" || first["name"] != "छठ पूजा" || first["weekday_label"] != "रवि" || first["month_label"] != "नवंबर" {
		t.Errorf("first = %v", first)
	}

	// start_date / end_date / category / paging.
	code, body = getJSON(t, r, hinduBase+"?language_code=en&start_date=2027-08-01&end_date=2027-08-31&category=festival,eclipse&page_size=100")
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	for _, c := range body["data"].([]any) {
		m := c.(map[string]any)
		if d := m["date"].(string); d < "2027-08-01" || d > "2027-08-31" {
			t.Errorf("%v outside the window", m["id"])
		}
		if cat := m["category"]; cat != "festival" && cat != "eclipse" {
			t.Errorf("%v has category %v", m["id"], cat)
		}
	}

	// category can be repeated as well as comma separated, and both forms agree.
	_, comma := getJSON(t, r, hinduBase+"?language_code=en&start_date=2027-01-01&end_date=2027-03-31&category=ekadashi,vrat&page_size=100")
	_, repeated := getJSON(t, r, hinduBase+"?language_code=en&start_date=2027-01-01&end_date=2027-03-31&category=ekadashi&category=vrat&page_size=100")
	_, one := getJSON(t, r, hinduBase+"?language_code=en&start_date=2027-01-01&end_date=2027-03-31&category=ekadashi&page_size=100")
	if comma["total"] != repeated["total"] || comma["total"].(float64) <= one["total"].(float64) {
		t.Errorf("category forms disagree: comma=%v repeated=%v single=%v", comma["total"], repeated["total"], one["total"])
	}

	// A page past the end returns an empty list, not an error.
	code, body = getJSON(t, r, hinduBase+"?language_code=en&page=9999")
	if code != 200 || len(body["data"].([]any)) != 0 {
		t.Errorf("past the end: %d %v", code, body)
	}

	// page_size is capped at 100; unparsable page values fall back to the defaults.
	_, body = getJSON(t, r, hinduBase+"?language_code=en&page_size=5000&page=abc")
	if body["page_size"] != float64(100) || body["page"] != float64(1) {
		t.Errorf("clamping: %v", body)
	}
}

func TestHinduEvents_Detail(t *testing.T) {
	r := newHinduRouter(t)

	code, body := getJSON(t, r, hinduBase+"/2026-11-05_rama-ekadashi?language_code=hi")
	if code != 200 {
		t.Fatalf("%d %v", code, body)
	}
	if body["name"] != "रमा एकादशी" || body["sunrise_at"] != "2026-11-05T01:06:00Z" || body["sunset_at"] != "2026-11-05T12:03:00Z" {
		t.Errorf("body = %v", body)
	}
	tithi := body["tithi"].(map[string]any)
	if tithi["label"] != "कार्तिक, कृष्ण एकादशी" || tithi["lunar_month"] != "kartika" || tithi["number"] != float64(11) {
		t.Errorf("tithi = %v", tithi)
	}
	parana := body["parana"].(map[string]any)
	if parana["start_at"] != "2026-11-06T01:07:00Z" {
		t.Errorf("parana = %v", parana)
	}
	for _, k := range []string{"puja", "nishita_start_at", "moonrise_at", "sankranti_at", "eclipse"} {
		if v, present := body[k]; !present || v != nil {
			t.Errorf("%s must be present and null, got %v (present=%v)", k, v, present)
		}
	}

	code, body = getJSON(t, r, hinduBase+"/2026-11-16_vrishchika-sankranti?language_code=en")
	if code != 200 || body["tithi"] != nil || body["sankranti_at"] != "2026-11-16T14:22:00Z" {
		t.Errorf("sankranti: %d %v", code, body)
	}

	code, body = getJSON(t, r, hinduBase+"/2027-02-21_chandra-grahan-upachchaya?language_code=en")
	ecl, _ := body["eclipse"].(map[string]any)
	if code != 200 || ecl["peak_at"] != "2027-02-20T23:13:00Z" || body["tithi"].(map[string]any)["label"] != "Occurs during Purnima" {
		t.Errorf("eclipse: %d %v", code, body)
	}

	code, body = getJSON(t, r, hinduBase+"/2026-11-05_nope?language_code=en")
	if code != 404 || body["error"] != "hindu event not found" {
		t.Errorf("unknown id: %d %v", code, body)
	}
}

func TestHinduEvents_LanguageCodeIsRequiredEverywhere(t *testing.T) {
	r := newHinduRouter(t)
	for _, url := range []string{
		hinduBase + "/today-or-upcoming",
		hinduBase,
		hinduBase + "/2026-11-05_rama-ekadashi",
		hinduBase + "/does-not-exist", // a missing language_code is reported before the lookup
	} {
		code, body := getJSON(t, r, url)
		if code != 400 || body["error"] != "language_code is required" {
			t.Errorf("GET %s: %d %v", url, code, body)
		}
	}
	for _, url := range []string{
		hinduBase + "/today-or-upcoming?language_code=fr",
		hinduBase + "?language_code=HI",
		hinduBase + "/2026-11-05_rama-ekadashi?language_code=",
	} {
		code, body := getJSON(t, r, url)
		wantMsg := "unsupported language_code, use hi or en"
		if strings.HasSuffix(url, "language_code=") {
			wantMsg = "language_code is required"
		}
		if code != 400 || body["error"] != wantMsg {
			t.Errorf("GET %s: %d %v, want 400 %q", url, code, body, wantMsg)
		}
	}
}

func TestHinduEvents_ListValidation(t *testing.T) {
	r := newHinduRouter(t)
	cases := []struct{ query, want string }{
		{"start_date=2026-1-5", "invalid start_date, expected YYYY-MM-DD"},
		{"start_date=tomorrow", "invalid start_date, expected YYYY-MM-DD"},
		{"end_date=2026-02-30", "invalid end_date, expected YYYY-MM-DD"},
		{"start_date=2027-03-10&end_date=2027-03-09", "end_date must not be before start_date"},
		{"category=ekadashi,party", `unknown category "party"`},
	}
	for _, tc := range cases {
		code, body := getJSON(t, r, hinduBase+"?language_code=en&"+tc.query)
		if code != 400 || body["error"] != tc.want {
			t.Errorf("?%s: %d %v, want 400 %q", tc.query, code, body, tc.want)
		}
	}

	// Same start and end date is a valid one-day window; an end_date before today alone is just empty.
	if code, body := getJSON(t, r, hinduBase+"?language_code=en&start_date=2027-03-06&end_date=2027-03-06"); code != 200 || body["total"] != float64(len(body["data"].([]any))) || body["total"].(float64) == 0 {
		t.Errorf("one-day window: %d %v", code, body)
	}
	if code, body := getJSON(t, r, hinduBase+"?language_code=en&end_date=2026-11-01"); code != 200 || body["total"] != float64(0) {
		t.Errorf("end before today: %d %v", code, body)
	}
}
