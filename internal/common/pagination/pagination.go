package pagination

import (
	"time"

	"gorm.io/gorm"
)

const (
	defaultPageSize    = 10
	maxPageSize        = 100
	hoursPerDay        = 24
	unlockBoundaryHour = 5 // new content becomes eligible at 5am IST each day
)

// istZone is a fixed UTC+5:30 offset (IST does not observe DST); FixedZone avoids
// a dependency on system tzdata being present.
var istZone = time.FixedZone("IST", 19800)

// unlockDayIndex returns a day index that increments at unlockBoundaryHour IST, so two
// instants share the same index iff no 5am-IST boundary falls between them. Used to
// count elapsed "unlock days" on IST wall-clock boundaries rather than raw elapsed hours.
func unlockDayIndex(t time.Time) int64 {
	shifted := t.In(istZone).Add(-unlockBoundaryHour * time.Hour)
	y, m, d := shifted.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / (hoursPerDay * 3600)
}

// Params holds normalized page/page_size values.
type Params struct {
	Page     int
	PageSize int
}

// NormalizeParams clamps page/page_size to sane bounds: page defaults to 1,
// page_size defaults to 10 and is capped at 100.
func NormalizeParams(page, pageSize int) Params {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return Params{Page: page, PageSize: pageSize}
}

// Result is the standard paginated list envelope shared across list APIs.
type Result[T any] struct {
	Data       []T   `json:"data"`
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
	NextPage   *int  `json:"next_page,omitempty"`
	PrevPage   *int  `json:"prev_page,omitempty"`
}

// BuildResult wraps a page of data with pagination metadata computed from total/page/pageSize.
func BuildResult[T any](data []T, total int64, page, pageSize int) Result[T] {
	totalPages := int(total) / pageSize
	if int(total)%pageSize > 0 {
		totalPages++
	}

	var nextPage, prevPage *int
	if page < totalPages {
		next := page + 1
		nextPage = &next
	}
	if page > 1 {
		prev := page - 1
		prevPage = &prev
	}

	return Result[T]{
		Data:       data,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		NextPage:   nextPage,
		PrevPage:   prevPage,
	}
}

// EligibleCount implements a progressive-unlock schedule: a user who joined
// (userCreatedAt) is eligible to see the oldest firstDayUnlockCount rows immediately,
// and an additional dailyUnlockBatchSize rows for every unlock-day elapsed since then,
// where an unlock-day advances at unlockBoundaryHour (5am) IST rather than at the
// exact hour the user joined. The result is capped at totalFiltered, since all rows
// already exist in the DB.
func EligibleCount(userCreatedAt, now time.Time, firstDayUnlockCount, dailyUnlockBatchSize int, totalFiltered int64) int64 {
	daysSinceJoin := unlockDayIndex(now) - unlockDayIndex(userCreatedAt)
	if daysSinceJoin < 0 {
		daysSinceJoin = 0
	}

	eligible := int64(firstDayUnlockCount) + daysSinceJoin*int64(dailyUnlockBatchSize)
	if eligible > totalFiltered {
		eligible = totalFiltered
	}
	if eligible < 0 {
		eligible = 0
	}
	return eligible
}

// FetchEligiblePage fetches one page of rows from an already-filtered, already-`.Model()`-scoped
// gorm query. When userCreatedAt is nil, it behaves as plain offset pagination ordered newest-first.
// When userCreatedAt is set, it first computes the eligible-rows window via EligibleCount, then
// restricts the page to that window (newest-of-eligible first) without fetching more than
// pageSize full rows: a single indexed lookup finds the cutoff timestamp of the window, then a
// normal offset/limit query (identical cost to plain pagination) fetches the requested page.
// The returned total is the eligible-window size (or the full filtered count when
// userCreatedAt is nil), so callers can pass it straight into BuildResult.
func FetchEligiblePage[T any](query *gorm.DB, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize int, now time.Time, page, pageSize int) ([]T, int64, error) {
	var totalFiltered int64
	if err := query.Session(&gorm.Session{}).Count(&totalFiltered).Error; err != nil {
		return nil, 0, err
	}

	reportedTotal := totalFiltered
	pageQuery := query.Session(&gorm.Session{})

	if userCreatedAt != nil {
		eligible := EligibleCount(*userCreatedAt, now, firstDayUnlockCount, dailyUnlockBatchSize, totalFiltered)
		reportedTotal = eligible

		if eligible <= 0 {
			return []T{}, 0, nil
		}

		if eligible < totalFiltered {
			// created_at alone is not a safe cutoff key: rows inserted in the same batch
			// (e.g. a bulk seed script) can share an identical timestamp, in which case a
			// plain "created_at <= cutoff" would pull in every tied row instead of stopping
			// at exactly `eligible` rows. Order/cut on (created_at, id) instead, which is
			// unique, so the eligible window always has exactly `eligible` rows.
			var cutoff struct {
				CreatedAt time.Time `gorm:"column:created_at"`
				ID        string    `gorm:"column:id"`
			}
			err := query.Session(&gorm.Session{}).
				Select("created_at, id").
				Order("created_at ASC, id ASC").
				Offset(int(eligible) - 1).
				Limit(1).
				Scan(&cutoff).Error
			if err != nil {
				return nil, 0, err
			}
			if !cutoff.CreatedAt.IsZero() {
				pageQuery = pageQuery.Where("(created_at, id) <= (?, ?)", cutoff.CreatedAt, cutoff.ID)
			}
		}
	}

	var rows []T
	offset := (page - 1) * pageSize
	if err := pageQuery.Order("created_at DESC, id DESC").Offset(offset).Limit(pageSize).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, reportedTotal, nil
}
