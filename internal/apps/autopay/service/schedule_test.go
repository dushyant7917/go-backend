package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAttemptAmountLadder(t *testing.T) {
	p := DefaultRetryPolicy()
	// every billing frequency follows the same ladder: max(Rs 99, previous - Rs 100); amounts in paise
	cases := []struct {
		name    string
		max     int64
		attempt int
		want    int64
	}{
		{"first attempt is the full amount", 94300, 1, 94300},
		{"943 -> 843", 94300, 2, 84300},
		{"943 -> 743", 94300, 3, 74300},
		{"199 -> 99", 19900, 2, 9900},
		{"199 stays at 99", 19900, 3, 9900},
		{"199 stays at 99 on the last attempt", 19900, 9, 9900},
		{"249 -> 149", 24900, 2, 14900},
		{"249 -> 99", 24900, 3, 9900},
		{"249 stays at 99", 24900, 4, 9900},
		{"a Rs 99 mandate is always Rs 99", 9900, 5, 9900},
		{"a legacy mandate below the minimum is never charged above its own max", 5000, 2, 5000},
	}
	for _, c := range cases {
		if got := p.AttemptAmount(c.max, c.attempt); got != c.want {
			t.Errorf("%s: AttemptAmount(%d, %d) = %d, want %d", c.name, c.max, c.attempt, got, c.want)
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
