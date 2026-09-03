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
> Last updated: 2026-09-03 (Reconcile/Process/Retry moved to 3x/day at 1:30/2:45/4:00, 1:45/3:00/4:15, 2:00/3:15/4:30 AM IST respectively; Charge Pending Payments moved to 2:15/2:30/3:30/3:45 AM IST)

## Daily Timeline (IST)

| IST Time | Job | Frequency | Workflow |
|---|---|---|---|
| 1:30 AM | Reconcile Payments | 3x Daily | [reconcile-payments.yml](.github/workflows/reconcile-payments.yml) |
| 1:45 AM | Process New Billing Cycles | 3x Daily | [process-new-billing-cycles.yml](.github/workflows/process-new-billing-cycles.yml) |
| 2:00 AM | Retry Failed Billing Cycles | 3x Daily | [retry-failed-billing-cycles.yml](.github/workflows/retry-failed-billing-cycles.yml) |
| 2:15 AM | Charge Pending Payments | 4x Daily | [charge-pending-payments.yml](.github/workflows/charge-pending-payments.yml) |
| 2:30 AM | Charge Pending Payments | 4x Daily | [charge-pending-payments.yml](.github/workflows/charge-pending-payments.yml) |
| 2:45 AM | Reconcile Payments | 3x Daily | [reconcile-payments.yml](.github/workflows/reconcile-payments.yml) |
| 3:00 AM | Process New Billing Cycles | 3x Daily | [process-new-billing-cycles.yml](.github/workflows/process-new-billing-cycles.yml) |
| 3:15 AM | Retry Failed Billing Cycles | 3x Daily | [retry-failed-billing-cycles.yml](.github/workflows/retry-failed-billing-cycles.yml) |
| 3:30 AM | Charge Pending Payments | 4x Daily | [charge-pending-payments.yml](.github/workflows/charge-pending-payments.yml) |
| 3:45 AM | Charge Pending Payments | 4x Daily | [charge-pending-payments.yml](.github/workflows/charge-pending-payments.yml) |
| 4:00 AM | Reconcile Payments | 3x Daily | [reconcile-payments.yml](.github/workflows/reconcile-payments.yml) |
| 4:15 AM | Process New Billing Cycles | 3x Daily | [process-new-billing-cycles.yml](.github/workflows/process-new-billing-cycles.yml) |
| 4:30 AM | Retry Failed Billing Cycles | 3x Daily | [retry-failed-billing-cycles.yml](.github/workflows/retry-failed-billing-cycles.yml) |
| 6:00 AM | Parse News → Generate News Media | Daily | [parse-news.yml](.github/workflows/parse-news.yml) |
| 7:00 AM | Send DailyStory Push Notification | Daily | [send-dailystory-push-notification.yml](.github/workflows/send-dailystory-push-notification.yml) |
| 8:00 AM | Cleanup Orphan News Media | Weekly (Sun) | [cleanup-orphan-news-media.yml](.github/workflows/cleanup-orphan-news-media.yml) |
| 8:00 AM | Cleanup Old Posters | Weekly (Mon) | [cleanup-old-posters.yml](.github/workflows/cleanup-old-posters.yml) |
| 8:00 AM | Cleanup Orphan Profile Pictures | Weekly (Tue) | [cleanup-orphan-profile-pictures.yml](.github/workflows/cleanup-orphan-profile-pictures.yml) |
| 8:00 AM | Cleanup Triggered Meta Events | Weekly (Wed) | [cleanup-triggered-meta-events.yml](.github/workflows/cleanup-triggered-meta-events.yml) |
| 9:00 AM | Parse News → Generate News Media | Daily | [parse-news.yml](.github/workflows/parse-news.yml) |
| 2:30 PM | Parse News → Generate News Media | Daily | [parse-news.yml](.github/workflows/parse-news.yml) |
| 4:00 PM | Send Subscription Activity Report | Daily | [send-subscription-activity-report.yml](.github/workflows/send-subscription-activity-report.yml) |
| 11:00 PM | Cleanup Old News | Daily | [cleanup-old-news.yml](.github/workflows/cleanup-old-news.yml) |

## Active Jobs (by workflow)

### Pause Underperforming Adsets — Every 2 hours
Cron expression (UTC): `0 */2 * * *`

### Charge Pending Payments — 4x Daily
Cron expressions (UTC):
- `45 20 * * *` → 2:15 AM IST
- `0 21 * * *` → 2:30 AM IST
- `0 22 * * *` → 3:30 AM IST
- `15 22 * * *` → 3:45 AM IST

### Reconcile Payments — 3x Daily
Cron expressions (UTC):
- `0 20 * * *` → 1:30 AM IST
- `15 21 * * *` → 2:45 AM IST
- `30 22 * * *` → 4:00 AM IST

### Process New Billing Cycles — 3x Daily
Cron expressions (UTC):
- `15 20 * * *` → 1:45 AM IST
- `30 21 * * *` → 3:00 AM IST
- `45 22 * * *` → 4:15 AM IST

### Retry Failed Billing Cycles — 3x Daily
Cron expressions (UTC):
- `30 20 * * *` → 2:00 AM IST
- `45 21 * * *` → 3:15 AM IST
- `0 23 * * *` → 4:30 AM IST

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
