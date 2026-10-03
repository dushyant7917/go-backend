package pagination

import (
	"testing"
	"time"
)

func TestEligibleCount(t *testing.T) {
	joined := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const firstDayUnlockCount = 20
	const dailyUnlockBatchSize = 5
	const totalFiltered = 1000

	tests := []struct {
		name         string
		daysElapsed  int
		wantEligible int64
	}{
		{"day 0 (joined today)", 0, 20},
		{"day 1", 1, 25},
		{"day 2", 2, 30},
		{"day 7", 7, 55},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := joined.Add(time.Duration(tt.daysElapsed) * 24 * time.Hour)
			got := EligibleCount(joined, now, firstDayUnlockCount, dailyUnlockBatchSize, totalFiltered)
			if got != tt.wantEligible {
				t.Errorf("EligibleCount() = %d, want %d", got, tt.wantEligible)
			}
		})
	}
}

func TestEligibleCount_CapsAtTotal(t *testing.T) {
	joined := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := joined.Add(365 * 24 * time.Hour)

	got := EligibleCount(joined, now, 20, 5, 3)
	if got != 3 {
		t.Errorf("EligibleCount() = %d, want 3 (capped at totalFiltered)", got)
	}
}

func TestEligibleCount_ZeroFilteredRows(t *testing.T) {
	joined := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := joined.Add(30 * 24 * time.Hour)

	got := EligibleCount(joined, now, 20, 5, 0)
	if got != 0 {
		t.Errorf("EligibleCount() = %d, want 0 when there are no filtered rows", got)
	}
}
