# Cron Jobs

Each job is a standalone Go binary under `cron-jobs/<job_name>/main.go`, triggered by GitHub Actions workflows in [.github/workflows/](.github/workflows/).

See [CRON_SCHEDULE.md](../CRON_SCHEDULE.md) for the full schedule, IST/UTC times, and disabled jobs.

## Running a Job

```bash
# Run directly
go run cron-jobs/<job_name>/main.go

# Or build and run
go build -o bin/<job_name> cron-jobs/<job_name>/main.go && ./bin/<job_name>
```

Jobs load `.env.<GO_ENV>` (falling back to `.env`) and use `database.NewCronConnection()`.

## Active Jobs

| Job | Description |
|---|---|
| `parse_news_rss` | Fetch and translate news from RSS feeds |
| `parse_news_serper` | Fetch area-based news via Serper with semantic dedup and Gemini translation; chains into `generate_news_media` on success |
| `generate_news_media` | Generate media for news items (also runs as a chained step after `parse_news_serper`) |
| `send_dailystory_push_notification` | Expo push to DailyStoryApp users created in the last 7 days without an active subscription |
| `send_subscription_activity_report` | Email daily report of subscription activity |
| `charge_pending_payments` | Charge payments stuck in pending state |
| `retry_failed_billing_cycles` | Retry billing cycles that previously failed |
| `process_new_billing_cycles` | Process newly created billing cycles |
| `reconcile_payments` | Reconcile payment records against Razorpay |
| `cleanup_old_news` | Delete old news records from DB |
| `cleanup_old_posters` | Delete old poster records and R2 files |
| `cleanup_orphan_news_media` | Remove news media files in R2 not referenced by any news record |
| `cleanup_orphan_posters` | Remove poster files in R2 not referenced by any poster record |
| `cleanup_orphan_profile_pictures` | Remove profile picture files in R2 not referenced by any user |
| `cleanup_triggered_meta_events` | Delete already-triggered Meta pixel event records |

## Disabled Jobs (manual dispatch only)

| Job | Description |
|---|---|
| `cleanup_old_templates` | Delete old template records and files |
| `cleanup_orphan_templates` | Remove template files in R2 not referenced by any template record |
| `cleanup_orphan_posters` | Remove orphaned poster files from R2 |
| `cleanup_rejected_templates` | Delete rejected template records and files |
| `update_subscription_amount` | Update subscription amounts for unauthenticated users |
| `update_cancelled_subscription_amount` | Update subscription amounts for users with cancelled subscriptions |
