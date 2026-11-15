package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAttemptAmountLadder(t *testing.T) {
	p := DefaultRetryPolicy()
	cases := []struct {
		name      string
		max       int64
		attempt   int
		frequency string
		want      int64
	}{
		{"monthly first attempt is the full amount", 29900, 1, "monthly", 29900},
		{"monthly attempt 2 is Rs 25 less", 29900, 2, "monthly", 27400},
		{"monthly attempt 3", 29900, 3, "monthly", 24900},
		{"monthly floors at Rs 99", 29900, 9, "monthly", 9900},
		{"yearly attempt 2 is Rs 100 less", 99900, 2, "yearly", 89900},
		{"yearly floors at Rs 99", 99900, 9, "yearly", 19900}, // 99900 - 8*10000
		{"yearly hard floor", 29900, 9, "yearly", 9900},
		{"weekly keeps the full amount for 3 attempts", 5000, 3, "weekly", 5000},
		{"weekly attempt 4 is Rs 5 less", 5000, 4, "weekly", 4500},
		{"weekly floors at Rs 1", 1000, 9, "weekly", 100},
	}
	for _, c := range cases {
		if got := p.AttemptAmount(c.max, c.attempt, c.frequency); got != c.want {
			t.Errorf("%s: AttemptAmount(%d, %d, %s) = %d, want %d", c.name, c.max, c.attempt, c.frequency, got, c.want)
		}
	}
}

func TestNextRetryAtIsEveryThreeDaysFromCycleStart(t *testing.T) {
	p := DefaultRetryPolicy()
	start := time.Date(2026, 11, 1, 20, 0, 0, 0, time.UTC)
	for attempts, wantDays := range map[int]int{1: 3, 2: 6, 3: 9, 8: 24} {
		if got, want := p.NextRetryAt(start, attempts), start.AddDate(0, 0, wantDays); !got.Equal(want) {
			t.Errorf("NextRetryAt after %d attempts = %v, want %v", attempts, got, want)
		}
	}
}

func TestActualTenureMonths(t *testing.T) {
	for attempt, want := range map[int]int{1: 12, 2: 3, 3: 3, 4: 2, 5: 2, 6: 1, 9: 1} {
		if got := ActualTenureMonths(attempt); got != want {
			t.Errorf("ActualTenureMonths(%d) = %d, want %d", attempt, got, want)
		}
	}
}

func TestNextChargeDueAt(t *testing.T) {
	base := time.Date(2026, 1, 31, 22, 0, 0, 0, time.UTC)
	want := map[string]time.Time{
		"daily": base.AddDate(0, 0, 1), "weekly": base.AddDate(0, 0, 7), "fortnightly": base.AddDate(0, 0, 15),
		"bimonthly": base.AddDate(0, 2, 0), "monthly": base.AddDate(0, 1, 0), "quarterly": base.AddDate(0, 3, 0),
		"half_yearly": base.AddDate(0, 6, 0), "yearly": base.AddDate(1, 0, 0),
	}
	for freq, w := range want {
		got, err := NextChargeDueAt(freq, base)
		if err != nil || !got.Equal(w) || got.Location() != time.UTC {
			t.Errorf("NextChargeDueAt(%s) = %v (%v), want %v in UTC", freq, got, err, w)
		}
	}
	// as_presented is no longer a billing frequency: it must be rejected instead of silently meaning "monthly"
	if _, err := NextChargeDueAt("as_presented", base); err == nil {
		t.Error("as_presented must be an error")
	}
}

func TestIdentifiersAreGatewaySafeAndUnique(t *testing.T) {
	cycle := uuid.New()
	first := MerchantPaymentIDFor(cycle, 2, 0)
	replacement := MerchantPaymentIDFor(cycle, 2, 1)
	if first == replacement {
		t.Fatal("a replacement attempt must get a different merchant payment id")
	}
	for _, id := range []string{NewMerchantAutopayID(), first, replacement, AuthorizationMerchantPaymentID(cycle),
		PreDebitNotificationIDFor(first, 1), DebitExecutionIDFor(first, 2)} {
		if strings.ContainsAny(id, "- ./") || len(id) > 100 {
			t.Errorf("id %q is not gateway-safe", id)
		}
	}
	if PreDebitNotificationIDFor(first, 1) == PreDebitNotificationIDFor(first, 2) {
		t.Error("each notification send needs its own id")
	}
}

func TestPerAttemptLimitsAreTwo(t *testing.T) {
	if maxTemporaryRejectionsPerChargeAttempt != 2 || maxGatewayUnavailableRetriesPerChargeAttempt != 2 || maxPreDebitNotificationSendAttempts != 2 {
		t.Fatal("the per-attempt limits were decided to be 2")
	}
	if minNotificationAgeBeforeDebit != 26*time.Hour {
		t.Fatalf("the notification must be 26h old before a debit, got %v", minNotificationAgeBeforeDebit)
	}
}
