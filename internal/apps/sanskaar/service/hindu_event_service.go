package service

import (
	"log"
	"math"
	"time"

	hc "go-backend/internal/apps/sanskaar/hinducalendar"
	"go-backend/internal/apps/sanskaar/models"
	"go-backend/internal/common/pagination"
)

// HinduEventListParams are the already-validated filters of the list endpoint.
type HinduEventListParams struct {
	Lang       hc.Lang
	StartDate  string // YYYY-MM-DD; empty means today (IST)
	EndDate    string // YYYY-MM-DD; empty means no upper bound
	Categories []string
	Page       int
	PageSize   int
}

// HinduEventService serves the in-memory Hindu calendar.
type HinduEventService interface {
	TodayOrUpcoming(lang hc.Lang) models.HinduEventTodayOrUpcomingResponse
	List(p HinduEventListParams) pagination.Result[models.HinduEventCard]
	// GetByID returns the event, or false for an unknown id.
	GetByID(id string, lang hc.Lang) (*models.HinduEventDetail, bool)
}

type hinduEventService struct {
	store         *hc.Store
	publicURLBase string
	now           func() time.Time
}

// NewHinduEventService creates the service. publicURLBase is the Sanskaar public R2 URL (icons
// are served from it) and now is the clock (nil means time.Now; tests pass a fixed clock). It
// logs the covered date range and warns when the data is about to run out.
func NewHinduEventService(store *hc.Store, publicURLBase string, now func() time.Time) HinduEventService {
	if now == nil {
		now = time.Now
	}
	s := &hinduEventService{store: store, publicURLBase: publicURLBase, now: now}
	summary, warn := store.Coverage(now().In(hc.IST))
	if warn {
		log.Printf("WARNING: %s; the calendar runs out soon, merge the next CSV with scripts/generate_hindu_calendar", summary)
	} else {
		log.Print(summary)
	}
	return s
}

func (s *hinduEventService) today() string {
	return s.now().In(hc.IST).Format(hc.DateLayout)
}

func (s *hinduEventService) TodayOrUpcoming(lang hc.Lang) models.HinduEventTodayOrUpcomingResponse {
	today := s.today()
	events := s.store.OnOrAfter(today)
	resp := models.HinduEventTodayOrUpcomingResponse{Events: []models.HinduEventCard{}}
	if len(events) == 0 {
		return resp
	}
	date := events[0].Date
	resp.Date = &date
	resp.IsToday = date == today
	for _, e := range events {
		resp.Events = append(resp.Events, s.card(e, lang))
	}
	return resp
}

func (s *hinduEventService) List(p HinduEventListParams) pagination.Result[models.HinduEventCard] {
	start := p.StartDate
	if start == "" {
		start = s.today()
	}
	params := pagination.NormalizeParams(p.Page, p.PageSize)
	events, total := s.store.Query(hc.Query{
		StartDate:  start,
		EndDate:    p.EndDate,
		Categories: p.Categories,
		Offset:     pageOffset(params.Page, params.PageSize),
		Limit:      params.PageSize,
	})
	cards := make([]models.HinduEventCard, 0, len(events))
	for _, e := range events {
		cards = append(cards, s.card(e, p.Lang))
	}
	return pagination.BuildResult(cards, int64(total), params.Page, params.PageSize)
}

// pageOffset is the index of the first item of a 1-based page. A page so large that the offset
// would overflow int is clamped to MaxInt (no data is that far out), instead of wrapping around
// to a negative offset and returning the first page.
func pageOffset(page, pageSize int) int {
	if page-1 > math.MaxInt/pageSize {
		return math.MaxInt
	}
	return (page - 1) * pageSize
}

func (s *hinduEventService) GetByID(id string, lang hc.Lang) (*models.HinduEventDetail, bool) {
	e, ok := s.store.Get(id)
	if !ok {
		return nil, false
	}
	d := &models.HinduEventDetail{
		HinduEventCard: s.card(e, lang),
		Description:    e.Description.Get(lang),
		SunriseAt:      e.SunriseAt,
		SunsetAt:       e.SunsetAt,
		NishitaStartAt: e.NishitaStartAt,
		MoonriseAt:     e.MoonriseAt,
		SankrantiAt:    e.SankrantiAt,
		Puja:           e.Puja,
		Parana:         e.Parana,
		Eclipse:        e.Eclipse,
	}
	if t := e.Tithi; t != nil {
		var month *string
		if t.LunarMonth != "" {
			m := t.LunarMonth
			month = &m
		}
		d.Tithi = &models.HinduEventTithi{
			Label:      hc.TithiLabel(lang, e.Category, *t),
			LunarMonth: month,
			Paksha:     t.Paksha,
			Number:     t.Number,
			StartAt:    t.StartAt,
			EndAt:      t.EndAt,
		}
	}
	return d, true
}

// card builds the localized compact view of an event. The weekday and month labels are derived
// from the event date; the icon URL is the public R2 URL base plus the stored file key.
func (s *hinduEventService) card(e *hc.Event, lang hc.Lang) models.HinduEventCard {
	day := e.Day()
	return models.HinduEventCard{
		ID:            e.ID,
		Name:          e.Name.Get(lang),
		Category:      e.Category,
		CategoryLabel: hc.CategoryLabel(lang, e.Category),
		IconURL:       s.publicURLBase + "/" + e.IconFileKey,
		Date:          e.Date,
		Weekday:       hc.ISOWeekday(day.Weekday()),
		WeekdayLabel:  hc.WeekdayLabel(lang, day.Weekday()),
		MonthLabel:    hc.MonthLabel(lang, day.Month()),
	}
}
