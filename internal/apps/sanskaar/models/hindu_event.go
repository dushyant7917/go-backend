package models

import (
	"time"

	hc "go-backend/internal/apps/sanskaar/hinducalendar"
)

// HinduEventCard is the compact event shown in the home carousel and the calendar list.
// All text fields are localized to the requested language_code.
type HinduEventCard struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	CategoryLabel string `json:"category_label"`
	IconURL       string `json:"icon_url"`
	Date          string `json:"date"`          // YYYY-MM-DD, IST calendar date
	Weekday       int    `json:"weekday"`       // ISO: Monday=1 .. Sunday=7
	WeekdayLabel  string `json:"weekday_label"` // e.g. "Monday" / "सोम"
	MonthLabel    string `json:"month_label"`   // e.g. "November" / "नवंबर"
}

// HinduEventTodayOrUpcomingResponse is the response of GET /hindu-events/today-or-upcoming:
// the events of today (IST) or, when there are none, of the next date that has any. Date is
// null (and Events empty) once the data has run out.
type HinduEventTodayOrUpcomingResponse struct {
	Date    *string          `json:"date"`
	IsToday bool             `json:"is_today"`
	Events  []HinduEventCard `json:"events"`
}

// HinduEventTithi is the lunar day of the event. LunarMonth is null on eclipse rows.
type HinduEventTithi struct {
	Label      string    `json:"label"`
	LunarMonth *string   `json:"lunar_month"`
	Paksha     string    `json:"paksha"`
	Number     int       `json:"number"`
	StartAt    time.Time `json:"start_at"`
	EndAt      time.Time `json:"end_at"`
}

// HinduEventDetail is the event screen payload. Sunrise and sunset are always present; every
// optional group is always present as a key and is null when the data has no value for it, so
// clients can build their reminder choices from the non-null times. Puja, Parana and Eclipse
// are the calendar's own start/end groups (UTC instants), serialized as stored.
type HinduEventDetail struct {
	HinduEventCard
	Description    string           `json:"description"`
	SunriseAt      time.Time        `json:"sunrise_at"`
	SunsetAt       time.Time        `json:"sunset_at"`
	Tithi          *HinduEventTithi `json:"tithi"`
	Puja           *hc.Window       `json:"puja"`
	NishitaStartAt *time.Time       `json:"nishita_start_at"`
	MoonriseAt     *time.Time       `json:"moonrise_at"`
	SankrantiAt    *time.Time       `json:"sankranti_at"`
	Parana         *hc.Window       `json:"parana"`
	Eclipse        *hc.Eclipse      `json:"eclipse"`
}
