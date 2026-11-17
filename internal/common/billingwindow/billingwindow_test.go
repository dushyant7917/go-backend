package billingwindow

import (
	"testing"
	"time"

	pkgphonepe "go-backend/pkg/phonepe"
)

func utc(day, hour, min int) time.Time {
	return time.Date(2026, 11, day, hour, min, 0, 0, time.UTC)
}

func TestNextChargeRun(t *testing.T) {
	cases := []struct{ now, want time.Time }{
		{utc(10, 19, 40), utc(10, 22, 0)}, // before tonight's run: tonight
		{utc(10, 22, 0), utc(11, 22, 0)},  // exactly at the run: strictly after, so tomorrow
		{utc(10, 22, 15), utc(11, 22, 0)}, // after the run: tomorrow
		{utc(10, 0, 5), utc(10, 22, 0)},
	}
	for _, c := range cases {
		if got := NextChargeRun(c.now); !got.Equal(c.want) {
			t.Errorf("NextChargeRun(%v) = %v, want %v", c.now, got, c.want)
		}
	}
}

func TestNextNoticeRun(t *testing.T) {
	if got := NextNoticeRun(utc(10, 19, 45)); !got.Equal(utc(11, 19, 45)) {
		t.Errorf("strictly after the run: %v", got)
	}
	if got := NextNoticeRun(utc(10, 3, 0)); !got.Equal(utc(10, 19, 45)) {
		t.Errorf("same day before the run: %v", got)
	}
}

func TestBounds(t *testing.T) {
	now := utc(10, 19, 40) // Cron A
	start, end := Bounds(now)
	if !start.Equal(utc(10, 22, 0).Add(-CatchUp)) || !end.Equal(utc(11, 22, 0)) {
		t.Fatalf("Bounds = %v .. %v", start, end)
	}
}

func TestEarliestDebit(t *testing.T) {
	now := utc(10, 19, 40)
	if got := EarliestDebit(now, utc(9, 3, 0)); !got.Equal(now.Add(Min)) {
		t.Errorf("a charge time in the past must wait for the notice to age: %v", got)
	}
	if got := EarliestDebit(now, utc(11, 21, 50)); !got.Equal(utc(11, 21, 50)) {
		t.Errorf("a charge time past the minimum notice is kept: %v", got)
	}
}

// The schedule the window is designed for (UTC). These guard the properties it exists for, so a future edit to the
// numbers or the cron times cannot quietly break them.
var (
	noticeRuns = []time.Duration{19*time.Hour + 45*time.Minute, 19*time.Hour + 40*time.Minute} // Cron A (= NoticeRunHour/Minute), Cron B
	chargeRuns = []time.Duration{
		time.Duration(ChargeRunHour)*time.Hour + time.Duration(ChargeRunMinute)*time.Minute,             // Cron C, 1st run
		time.Duration(SecondChargeRunHour)*time.Hour + time.Duration(SecondChargeRunMinute)*time.Minute, // Cron C, 2nd run
	}
)

// Whatever the charge time, a notice sent at Cron A/B is debited at the next night's first Charge run, between Min
// and 48h after the notice.
func TestNoticeToDebitGap(t *testing.T) {
	day := utc(10, 0, 0)
	for _, run := range noticeRuns {
		now := day.Add(run)
		start, end := Bounds(now)
		for charge := start; !charge.After(end); charge = charge.Add(7 * time.Minute) {
			// The first Charge run at or after the debit's earliest time is when it is made.
			earliest := EarliestDebit(now, charge)
			debit := firstChargeRunAtOrAfter(earliest)
			gap := debit.Sub(now)
			if gap < Min || gap > 48*time.Hour {
				t.Fatalf("notice at %v for charge time %v is debited at %v: gap %v outside [%v, 48h]",
					now, charge, debit, gap, Min)
			}
		}
	}
}

// Every charge time is inside the window of exactly one night's Cron A run, so a mandate is notified once, and a
// missed night is recovered by the next CatchUp runs.
func TestEveryChargeTimeIsSelectedOncePerNight(t *testing.T) {
	cronA := noticeRuns[0]
	first := utc(1, 0, 0).Add(cronA)
	// Start a day in, so the night before the first charge time exists in the simulated runs.
	for charge := first.Add(Period); charge.Before(first.Add(30 * Period)); charge = charge.Add(13 * time.Minute) {
		var normal, total int
		for night := 0; night < 40; night++ {
			now := first.Add(time.Duration(night) * Period)
			start, end := Bounds(now)
			if charge.Before(start) || charge.After(end) {
				continue
			}
			total++
			if charge.After(NextChargeRun(now)) {
				normal++
			}
		}
		if normal != 1 {
			t.Fatalf("charge time %v is in its normal window %d times, want exactly 1", charge, normal)
		}
		// One normal night plus CatchUp nights of grace (one more when the charge time sits exactly on a window edge).
		want := 1 + int(CatchUp/Period)
		if total != want && total != want+1 {
			t.Fatalf("charge time %v is visible to %d runs, want %d (1 normal + %d catch-up)", charge, total, want, int(CatchUp/Period))
		}
	}
}

func TestSchedule(t *testing.T) {
	// A notice must be Min old at the next night's first Charge run, with room for the notice crons to take time.
	firstCharge := time.Duration(ChargeRunHour)*time.Hour + time.Duration(ChargeRunMinute)*time.Minute
	for _, run := range noticeRuns {
		if margin := firstCharge + Period - run - Min; margin < 10*time.Minute {
			t.Errorf("notice cron at %v leaves only %v margin before the first Charge run's %v gate", run, margin, Min)
		}
	}
	if noticeRuns[0] != time.Duration(NoticeRunHour)*time.Hour+time.Duration(NoticeRunMinute)*time.Minute {
		t.Errorf("Cron A %v does not match NoticeRunHour/Minute", noticeRuns[0])
	}
	if chargeRuns[0] != firstCharge {
		t.Errorf("first charge run %v does not match ChargeRunHour/Minute %v", chargeRuns[0], firstCharge)
	}
	// PhonePe documents at least 1.5h between retries of the same debit; the second run must respect it, and stay in
	// the same night (before the next night's first run).
	if ChargeRunGap < pkgphonepe.MinRedemptionRetryGap || ChargeRunGap >= Period {
		t.Errorf("the second Charge run is %v after the first: want at least 1.5h (PhonePe's retry gap) and less than a day", ChargeRunGap)
	}
	if chargeRuns[1]-chargeRuns[0] != ChargeRunGap {
		t.Errorf("chargeRuns gap %v does not match ChargeRunGap %v", chargeRuns[1]-chargeRuns[0], ChargeRunGap)
	}
	if MinAuthLead != Min+Period {
		t.Errorf("MinAuthLead %v must equal Min + Period", MinAuthLead)
	}
	if Min < 25*time.Hour {
		t.Errorf("Min %v is below the 25h Cashfree documents as the notice-to-debit gap", Min)
	}
}

func firstChargeRunAtOrAfter(t time.Time) time.Time {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	for d := 0; d < 3; d++ {
		for _, run := range chargeRuns {
			if c := day.Add(time.Duration(d)*Period + run); !c.Before(t) {
				return c
			}
		}
	}
	panic("unreachable")
}
