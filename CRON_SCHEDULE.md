# Cron Schedule Reference

Single source of truth for all scheduled GitHub Actions cron jobs.
Workflows live in [.github/workflows/](.github/workflows/). All can also be run manually via `workflow_dispatch`.

> Times shown in **IST** (UTC+5:30). GitHub Actions cron is in **UTC**.
> Last updated: 2026-07-21 (preponed Reconcile Payments/Process New Billing Cycles/Retry Failed Billing Cycles by 1.5h, Charge Pending Payments by 3h)

## Daily Timeline (IST)

| IST Time | Job | Frequency | Workflow |
|---|---|---|---|
| 11:30 PM (prev day) | Reconcile Payments | Daily | [reconcile-payments.yml](.github/workflows/reconcile-payments.yml) |
| 12:00 AM | Process New Billing Cycles | Daily | [process-new-billing-cycles.yml](.github/workflows/process-new-billing-cycles.yml) |
| 12:30 AM | Retry Failed Billing Cycles | Daily | [retry-failed-billing-cycles.yml](.github/workflows/retry-failed-billing-cycles.yml) |
| 1:40 AM | Charge Pending Payments | Daily | [charge-pending-payments.yml](.github/workflows/charge-pending-payments.yml) |
| 2:00 AM | Charge Pending Payments | Daily | [charge-pending-payments.yml](.github/workflows/charge-pending-payments.yml) |
| 6:00 AM | Parse News → Generate News Media | Daily | [parse-news.yml](.github/workflows/parse-news.yml) |
| 7:00 AM | Send DailyStory Push Notification | Daily | [send-dailystory-push-notification.yml](.github/workflows/send-dailystory-push-notification.yml) |
| 8:00 AM | Cleanup Orphan News Media | Weekly (Sun) | [cleanup-orphan-news-media.yml](.github/workflows/cleanup-orphan-news-media.yml) |
| 8:00 AM | Cleanup Old Posters | Weekly (Mon) | [cleanup-old-posters.yml](.github/workflows/cleanup-old-posters.yml) |
| 8:00 AM | Cleanup Orphan Profile Pictures | Weekly (Tue) | [cleanup-orphan-profile-pictures.yml](.github/workflows/cleanup-orphan-profile-pictures.yml) |
| 8:00 AM | Cleanup Triggered Meta Events | Weekly (Wed) | [cleanup-triggered-meta-events.yml](.github/workflows/cleanup-triggered-meta-events.yml) |
| 9:00 AM | Parse News → Generate News Media | Daily | [parse-news.yml](.github/workflows/parse-news.yml) |
| 2:30 PM | Parse News → Generate News Media | Daily | [parse-news.yml](.github/workflows/parse-news.yml) |
| 4:00 PM | Send Subscription Activity Report | Daily | [send-subscription-activity-report.yml](.github/workflows/send-subscription-activity-report.yml) |
| 4:30 PM | Send Complete Signup WhatsApp | Daily | [send-complete-signup-whatsapp.yml](.github/workflows/send-complete-signup-whatsapp.yml) |
| 11:00 PM | Cleanup Old News | Daily | [cleanup-old-news.yml](.github/workflows/cleanup-old-news.yml) |

## Active Jobs (by workflow)

### Pause Underperforming Adsets — Every hour
Cron expression (UTC): `0 * * * *`

### Charge Pending Payments — Daily
Cron expressions (UTC):
- `10 20 * * *` → 1:40 AM IST
- `30 20 * * *` → 2:00 AM IST

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
| Send Complete Signup WhatsApp | `0 11 * * *` | 4:30 PM |
| Reconcile Payments | `0 18 * * *` | 11:30 PM (prev day) |
| Process New Billing Cycles | `30 18 * * *` | 12:00 AM |
| Retry Failed Billing Cycles | `0 19 * * *` | 12:30 AM |

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

---
**Maintenance note:** When you change a cron in any `.github/workflows/*.yml`, update this file in the same commit.
