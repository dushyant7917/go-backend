// Package billingwindow is the single definition of the pre-debit notice window used by every payment
// billing crons (the old Razorpay ones and the unified autopay ones) and by the repair tooling. Change the numbers here,
// nowhere else.
//
// The daily schedule this window is built around (cron-job.org, UTC; IST is +5:30):
//
//	Reconcile (Cron D)          19:15  (12:45 AM IST)
//	Retry failed cycles (Cron B)19:40  ( 1:10 AM IST)
//	Process new cycles (Cron A) 19:45  ( 1:15 AM IST)
//	Charge (Cron C), 1st run    22:00  ( 3:30 AM IST)   <- ChargeRunHour/ChargeRunMinute
//	Charge (Cron C), 2nd run    22:15  ( 3:45 AM IST)
//
// How the window works: debits only happen at the Charge runs, and never before the notice is Min old. A notice
// sent by Cron A/B at 19:45/19:40 therefore always becomes a debit at the very next night's first Charge run,
// 26h15m-26h20m later: above the 24h notice the gateways require, and well inside the 48h a gateway may keep a
// notice valid. Cron A/B select a mandate by WHICH Charge run its charge time falls before, not by how far away
// the charge time is: a charge time between tonight's first Charge run and tomorrow night's is notified now and
// debited tomorrow night. Each mandate is therefore notified on exactly one night (plus CatchUp nights of grace if
// a run was missed, see Bounds), and the notice never ages past its validity.
package billingwindow

import "time"

const (
	// Period is how often the billing crons run: once a day.
	Period = 24 * time.Hour

	// Min is the least notice a customer gets before a debit, and the least age of a pre-debit notice before the
	// debit is made. It sits above the 24h notice every gateway requires (Cashfree documents 25h).
	Min = 26 * time.Hour

	// NoticeRunHour and NoticeRunMinute (UTC) are when Cron A starts: 1:15 AM IST (Cron B runs 5 minutes earlier).
	// The notice crons must start at least Min - Period = 2h before the first Charge run's clock time, otherwise a
	// notice is not yet Min old at the next night's first Charge run and its debit slips a whole day.
	NoticeRunHour   = 19
	NoticeRunMinute = 45

	// ChargeRunHour and ChargeRunMinute (UTC) are when the first Charge run of the day starts: 3:30 AM IST.
	ChargeRunHour   = 22
	ChargeRunMinute = 0

	// CatchUp is how many days of missed nights Cron A/B still pick up: a mandate whose charge time is already
	// before tonight's first Charge run (a notice cron run failed or was skipped) is notified now and debited at
	// tomorrow night's run, up to CatchUp days late. Without it, the one night a mandate is selected would be its
	// only chance. Charging a customer long after the date they were told is a business call, so it is capped;
	// older mandates are handled by scripts/reschedule_missed_billing_cycles.
	CatchUp = 2 * Period

	// MinAuthLead is the least start_at lead accepted when a mandate is created: a new mandate is first seen by
	// Cron A up to one Period after it is authorized, and its first debit must still be Min after the notice.
	MinAuthLead = Min + Period
)

// NextNoticeRun returns the start of the first Cron A run strictly after now. The repair tooling uses it to place
// a mandate in a specific night's window.
func NextNoticeRun(now time.Time) time.Time {
	now = now.UTC()
	run := time.Date(now.Year(), now.Month(), now.Day(), NoticeRunHour, NoticeRunMinute, 0, 0, time.UTC)
	if !run.After(now) {
		run = run.Add(Period)
	}
	return run
}

// NextChargeRun returns the start of the first Charge run strictly after now.
func NextChargeRun(now time.Time) time.Time {
	now = now.UTC()
	run := time.Date(now.Year(), now.Month(), now.Day(), ChargeRunHour, ChargeRunMinute, 0, 0, time.UTC)
	if !run.After(now) {
		run = run.Add(Period)
	}
	return run
}

// Bounds returns the earliest and latest charge times that a cron run at now should send notices for.
// The latest is the charge time that falls before tomorrow night's first Charge run, which is when a notice sent
// now is debited. The earliest reaches CatchUp back from tonight's run, so a missed night is recovered.
func Bounds(now time.Time) (start, end time.Time) {
	tonight := NextChargeRun(now)
	return tonight.Add(-CatchUp), tonight.Add(Period)
}

// EarliestDebit returns when a debit whose notice is sent at now may happen: the scheduled charge time, but never
// sooner than Min after the notice. Gateways that take the charge time in the notice (Razorpay's payment_after,
// PhonePe's order expiry) must be given this, not the raw charge time, which for a mandate that is already due is
// in the past.
func EarliestDebit(now, scheduled time.Time) time.Time {
	earliest := now.Add(Min)
	if scheduled.After(earliest) {
		earliest = scheduled
	}
	return earliest.UTC() // callers store this, and datetime columns always hold UTC
}
