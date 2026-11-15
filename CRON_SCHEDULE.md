# Cron Schedule Reference

Single source of truth for all scheduled GitHub Actions cron jobs.
Workflows live in [.github/workflows/](.github/workflows/). All can also be run manually via `workflow_dispatch`.

> **Schedules below are driven externally by cron-job.org**, which calls each workflow's
> `workflow_dispatch` REST API endpoint at the times listed. GitHub's native `schedule:`
> trigger was removed from these workflows because its scheduled-event queue was
> deprioritizing runs under platform load, causing 1–10 hour delays vs. the configured
> times — `workflow_dispatch` calls aren't subject to that same queue. The GitHub PAT
> used by cron-job.org to call the dispatch API lives only in cron-job.org — never commit
> it anywhere in this repo.

> Times shown in **IST** (UTC+5:30). GitHub Actions cron is in **UTC**.
> Last updated: 2026-11-12 (unified autopay crons added beside the old Razorpay ones, same times; the per-gateway Cashfree/PhonePe jobs were never deployed and are removed; weekly autopay webhook-event cleanup; earlier, 2026-11-11: payment jobs moved: notices 1:10 AM (retry) and 1:15 AM (process new), charges 3:30/3:45 AM, with a notice window tied to the Charge runs; earlier, 2026-11-07: the four PhonePe recurring-payment jobs added on the same schedule as their Razorpay and Cashfree counterparts; earlier, 2026-11-06: payment jobs corrected to their real once-a-day schedule and the four Cashfree jobs added)

## Daily Timeline (IST)

| IST Time | Job | Frequency | Workflow |
|---|---|---|---|
| 12:45 AM | Reconcile Payments, Autopay Sync From Gateway | Daily | [reconcile-payments.yml](.github/workflows/reconcile-payments.yml), [autopay-sync-from-gateway.yml](.github/workflows/autopay-sync-from-gateway.yml) |
| 1:10 AM | Retry Failed Billing Cycles, Autopay Notify Retry Attempts | Daily | [retry-failed-billing-cycles.yml](.github/workflows/retry-failed-billing-cycles.yml), [autopay-notify-retry-attempts.yml](.github/workflows/autopay-notify-retry-attempts.yml) |
| 1:15 AM | Process New Billing Cycles, Autopay Notify New Cycles | Daily | [process-new-billing-cycles.yml](.github/workflows/process-new-billing-cycles.yml), [autopay-notify-new-cycles.yml](.github/workflows/autopay-notify-new-cycles.yml) |
| 3:30 AM | Charge Pending Payments, Autopay Debit Notified Attempts | 2x Daily | [charge-pending-payments.yml](.github/workflows/charge-pending-payments.yml), [autopay-debit-notified-attempts.yml](.github/workflows/autopay-debit-notified-attempts.yml) |
| 3:45 AM | Charge Pending Payments, Autopay Debit Notified Attempts | 2x Daily | [charge-pending-payments.yml](.github/workflows/charge-pending-payments.yml), [autopay-debit-notified-attempts.yml](.github/workflows/autopay-debit-notified-attempts.yml) |
| 6:00 AM | Parse News → Generate News Media | Daily | [parse-news.yml](.github/workflows/parse-news.yml) |
| 7:00 AM | Send DailyStory Push Notification | Daily | [send-dailystory-push-notification.yml](.github/workflows/send-dailystory-push-notification.yml) |
| 8:00 AM | Cleanup Orphan News Media | Weekly (Sun) | [cleanup-orphan-news-media.yml](.github/workflows/cleanup-orphan-news-media.yml) |
| 8:00 AM | Cleanup Old Posters | Weekly (Mon) | [cleanup-old-posters.yml](.github/workflows/cleanup-old-posters.yml) |
| 8:00 AM | Cleanup Orphan Profile Pictures | Weekly (Tue) | [cleanup-orphan-profile-pictures.yml](.github/workflows/cleanup-orphan-profile-pictures.yml) |
| 8:00 AM | Cleanup Triggered Meta Events | Weekly (Wed) | [cleanup-triggered-meta-events.yml](.github/workflows/cleanup-triggered-meta-events.yml) |
| 8:00 AM | Cleanup Old Autopay Webhook Events | Weekly (Thu) | [cleanup-old-autopay-webhook-events.yml](.github/workflows/cleanup-old-autopay-webhook-events.yml) |
| 9:00 AM | Parse News → Generate News Media | Daily | [parse-news.yml](.github/workflows/parse-news.yml) |
| 2:30 PM | Parse News → Generate News Media | Daily | [parse-news.yml](.github/workflows/parse-news.yml) |
| 4:00 PM | Send Subscription Activity Report | Daily | [send-subscription-activity-report.yml](.github/workflows/send-subscription-activity-report.yml) |
| 11:00 PM | Cleanup Old News | Daily | [cleanup-old-news.yml](.github/workflows/cleanup-old-news.yml) |

## Active Jobs (by workflow)

### Pause Underperforming Adsets — Every 2 hours
Cron expression (UTC): `0 */2 * * *`

### Payment jobs (old Razorpay and unified autopay) — overnight, in this order

Each old-Razorpay job and its unified `autopay-*` counterpart (listed below) are triggered at the same time. The order matters: reconcile first, then the **notice** (Process New Billing Cycles / Retry Failed Billing Cycles), then the **debit** (Charge Pending Payments). Reconcile, Process and Retry run once a day; Charge runs twice. UPI debits run overnight, outside NPCI's peak-hour blackout windows. This also keeps PhonePe inside the non-peak retry windows its docs require (9:31 PM–9:59 AM and 1:01 PM–4:59 PM IST).

| Job (old Razorpay / autopay) | Cron (UTC) | IST |
|---|---|---|
| Reconcile Payments / Autopay Sync From Gateway | `15 19 * * *` | 12:45 AM |
| Process New Billing Cycles / Autopay Notify New Cycles | `45 19 * * *` | 1:15 AM |
| Retry Failed Billing Cycles / Autopay Notify Retry Attempts | `40 19 * * *` | 1:10 AM |
| Charge Pending Payments / Autopay Debit Notified Attempts | `0 22 * * *` | 3:30 AM |
| Charge Pending Payments / Autopay Debit Notified Attempts (2nd run) | `15 22 * * *` | 3:45 AM |

**Notice window (shared by all gateways):** debits only happen at the Charge Pending Payments runs (3:30 and 3:45 AM IST), and never until a pre-debit notice is at least 26 hours old. Retry Failed Billing Cycles (1:10 AM) and Process New Billing Cycles (1:15 AM) therefore select a mandate by **which Charge run its charge time falls before**: a charge time between tonight's first Charge run (3:30 AM) and tomorrow night's is noticed now and debited at tomorrow's 3:30 AM run, **26h15m–26h20m after the notice**. That is above the 24h notice every gateway requires and well inside the 48 hours a gateway may keep a notice valid (PhonePe lets a notice lapse after roughly 48–72h and then rejects the debit with `SUBSCRIPTION_DEBIT_EXECUTE_INTERVAL_EXPIRED`). The numbers and the schedule they assume live in one place, `internal/common/billingwindow`, which the old Razorpay jobs, the autopay jobs and the repair script all use. Autopay's debit job additionally waits until the notification is *delivered* and at least 26h old and the cycle is due (PhonePe also waits for the `validAfter` it reports). For a charge time that is already due (a missed night) the order's `payment_after` (Razorpay) and expiry (PhonePe) are set to 26h after the notification, never to a time in the past.

> Each mandate is selected on exactly one night, plus **2 nights of catch-up** (`billingwindow.CatchUp`): if a run of Process New Billing Cycles / Retry Failed Billing Cycles is missed, the next nights still notice the mandate and it is debited one night later than planned. A mandate missed for more than the catch-up days is left to `scripts/reschedule_missed_billing_cycles`, so still watch for failed runs of these jobs (old Razorpay and autopay).
>
> **Keep the notice jobs at least 2 hours before the first Charge run's clock time** (they are 2h20m and 2h15m before it). Closer than that and a notice is not 26h old at the next night's first Charge run, so its debit slips a whole day (to about 50h, past a 48h notice validity). If you move any of these cron-job.org times, change the constants in `internal/common/billingwindow` and its tests with them.

### Unified autopay jobs (all gateways, one set of crons)

The unified autopay system (`/api/v1/autopay/*`, `autopay_*` tables) runs the same overnight sequence as the old Razorpay jobs above, at the **same times**, but with meaningful names. Each job loops over every active `payment_gateway_configs` row (one merchant account), isolates failures per config and shares one DB-backed rate limit per config with the web server.

| Job | Cron (UTC) | IST |
|---|---|---|
| Autopay Sync From Gateway | `15 19 * * *` | 12:45 AM |
| Autopay Notify Retry Attempts | `40 19 * * *` | 1:10 AM |
| Autopay Notify New Cycles | `45 19 * * *` | 1:15 AM |
| Autopay Debit Notified Attempts | `0 22 * * *` and `15 22 * * *` | 3:30 and 3:45 AM |

Create their cron-job.org jobs when the first autopay config goes live (the four daily workflows, the debit one called at both 3:30 and 3:45 AM, plus the weekly `cleanup-old-autopay-webhook-events` below); until then they are harmless no-ops (no active configs). The notification window, the 26h gap and the catch-up are the same `internal/common/billingwindow` constants.

### Parse News — Daily
Runs `parse-news-topics` job, then chains into `parse-news-areas`, then `generate-news-media` on success.

Cron expressions (UTC):
- `30 0 * * *` → 6:00 AM IST
- `30 3 * * *` → 9:00 AM IST
- `0 9 * * *` → 2:30 PM IST

### Single-run daily jobs
| Job | Cron (UTC) | IST |
|---|---|---|
| Cleanup Old News | `30 17 * * *` | 11:00 PM |
| Send DailyStory Push Notification | `30 1 * * *` | 7:00 AM |
| Send Subscription Activity Report | `30 10 * * *` | 4:00 PM |

### Weekly cleanup jobs
| Job | Cron (UTC) | IST | Day |
|---|---|---|---|
| Cleanup Orphan News Media | `30 2 * * 0` | 8:00 AM | Sunday |
| Cleanup Old Posters | `30 2 * * 1` | 8:00 AM | Monday |
| Cleanup Orphan Profile Pictures | `30 2 * * 2` | 8:00 AM | Tuesday |
| Cleanup Triggered Meta Events | `30 2 * * 3` | 8:00 AM | Wednesday |
| Cleanup Old Autopay Webhook Events (30 days) | `30 2 * * 4` | 8:00 AM | Thursday |

## Disabled Jobs (schedule commented out / removed — manual dispatch only)

| Job | Workflow |
|---|---|
| Parse News RSS | [parse-news-rss.yml](.github/workflows/parse-news-rss.yml) |
| Generate News Media (standalone) | [generate-news-media.yml](.github/workflows/generate-news-media.yml) |
| Cleanup Old Templates | [cleanup-old-templates.yml](.github/workflows/cleanup-old-templates.yml) |
| Cleanup Orphan Posters | [cleanup-orphan-posters.yml](.github/workflows/cleanup-orphan-posters.yml) |
| Cleanup Orphan Templates | [cleanup-orphan-templates.yml](.github/workflows/cleanup-orphan-templates.yml) |
| Cleanup Rejected Templates | [cleanup-rejected-templates.yml](.github/workflows/cleanup-rejected-templates.yml) |
| Update Cancelled Subscription Amount | [update-cancelled-subscription-amount.yml](.github/workflows/update-cancelled-subscription-amount.yml) |
| Update Subscription Amount | [update-subscription-amount.yml](.github/workflows/update-subscription-amount.yml) |
| Send Complete Signup WhatsApp | [send-complete-signup-whatsapp.yml](.github/workflows/send-complete-signup-whatsapp.yml) |
| Pause Underperforming Ads (superseded by Pause Underperforming Adsets) | [pause-underperforming-ads.yml](.github/workflows/pause-underperforming-ads.yml) |

---
**Maintenance note:** When you change a cron time here, update the corresponding cron-job.org job's schedule to match (the `.github/workflows/*.yml` files no longer carry a `schedule:` trigger — see the note at the top of this file). If you add a new scheduled workflow or change an existing schedule, update this file, the workflow's comment noting its "previous cron", and the cron-job.org job, all in the same change.
