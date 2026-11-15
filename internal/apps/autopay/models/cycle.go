package models

import "time"

// Billing cycles are half-open: [start_at, end_at). end_at is the true boundary, the instant the next cycle starts
// (recurring) or the paid period expires (one-time), so consecutive cycles touch with no gap and no overlap.
//
// RecurringCycleGrace is the one explicit slack the subscription-status query adds, and only for recurring mandates:
// if the next cycle's row does not exist yet at the boundary (a late or failed notice cron), a paying subscriber is
// not cut off at once. One-time payments have no successor cycle and get no grace. Wrongly deactivating a paying
// subscriber is worse than a day of access, hence the bias.
const RecurringCycleGrace = 24 * time.Hour
