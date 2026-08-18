# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Development
make dev              # Run server in debug mode (GIN_MODE=debug)
make build            # Build server binary to bin/server
make run              # Build and run server binary
make test             # Run all tests (go test -v ./...)
go test ./internal/apps/user/...   # Run tests for a specific package

# Database
make docker-up        # Start PostgreSQL via Docker Compose
make setup            # docker-up + migrate-up (first-time setup)
make migrate-up       # Apply pending migrations (uses goose + .env.$(GO_ENV))
make migrate-down     # Rollback last migration
make migrate-status   # Check migration status
make migrate-create NAME=migration_name  # Create new migration file

# Cron jobs (run directly or build binary first)
go run cron-jobs/<job_name>/main.go
go build -o bin/<job_name> cron-jobs/<job_name>/main.go
```

The server loads `.env.<GO_ENV>` (e.g. `.env.local`, `.env.prod`) then falls back to `.env`. See `.env.example` for all required variables.

## Architecture

### Request Flow

```
gin router (cmd/server/main.go)
  └── CORS middleware (internal/common/middleware/cors.go)
  └── Sentry middleware
  └── /api/v1/* routes
        └── handler (parse request, call service)
        └── service (business logic)
        └── repository (GORM queries)
```

All handlers use `internal/common/response` helpers: `response.Success()`, `response.Error()`, `response.ErrorWithDetails()`. `response.Error()` automatically captures 5xx errors to Sentry.

### Code Organization

Each feature lives under `internal/apps/<app>/` with four sub-packages:
- `models/` — GORM model structs
- `repository/` — database queries via GORM
- `service/` — business logic (depends on repository interfaces)
- `handler/` — Gin handlers + route registration (`*_router.go` registers routes onto the `v1` group)

Apps:
- **user** — user CRUD, metadata, profile pictures
- **otp** — phone OTP (AuthKey provider in prod, no-op locally) and email OTP
- **crush** — CrushConnect app (social matching)
- **dailystory** — DailyStory app: image templates, image posters, news (RSS), news posters, profile pictures, subscription status aggregation
- **wingwoman** — WingWoman app helper matching
- **razorpay/config** — per-app Razorpay credential configs stored in DB
- **razorpay/subscription** — Razorpay subscription lifecycle
- **razorpay/recurring_payment** — recurring billing cycles, charge/retry logic
- **r2/config** — per-app Cloudflare R2 configs stored in DB; `R2ClientFactory` creates S3-compatible clients dynamically
- **posthog/config** — per-app PostHog configs stored in DB
- **meta_event** — Meta pixel event tracking
- **metadataset/config** — notification dataset configs
- **agora/chat, agora/voicecall** — Agora RTC token generation
- **stream/chat** — GetStream chat token generation

### Key Patterns

**Multi-tenant configs**: Razorpay keys, R2 credentials, and PostHog keys are stored per-app in the database (not env vars). The `R2ClientFactory` (`internal/apps/r2/config/service/r2_client_factory.go`) is injected into handlers that need storage access. Admin-only config creation endpoints are registered *before* the CORS middleware in `main.go`.

**Environment-sensitive behavior**: `GO_ENV=prod` activates the real AuthKey SMS provider; any other value uses a no-op provider that only logs the OTP. Razorpay uses test/live keys based on the config record's environment field.

**Database connections**: Use `database.NewConnection()` for the web server and `database.NewCronConnection()` for cron jobs — they have different pool profiles (`ServerPoolProfile` vs `CronPoolProfile`).

**Encryption**: Sensitive values (e.g. Razorpay secrets) are encrypted at rest using AES via `pkg/secure/crypto.go`. Requires `ENCRYPTION_KEY` env var (16, 24, or 32 chars).

### Cron Jobs

Each cron job is a standalone Go binary in `cron-jobs/<job_name>/main.go`. They are triggered by GitHub Actions workflows (`.github/workflows/`). Jobs use `database.NewCronConnection()`. Active jobs:

- `parse_news_topics` — fetch topic-based news (india/world/sports) via Serper; English base + 9-language translations; caps per category; runs first in `.github/workflows/parse-news.yml`
- `parse_news_areas` — fetch area-based news via Serper with semantic dedup and Gemini translation; runs second in `.github/workflows/parse-news.yml`
- `generate_news_media` — generate images for approved news; runs third in `.github/workflows/parse-news.yml` (also available as standalone manual-dispatch workflow)
- `send_dailystory_push_notification` — Expo push to new users without subscriptions
- `send_subscription_activity_report` — email report of subscription activity
- `charge_pending_payments` — charge payments in pending state
- `retry_failed_billing_cycles` — retry billing cycles that previously failed
- `process_new_billing_cycles` — process newly created billing cycles
- `reconcile_payments` — reconcile payment records with Razorpay
- `cleanup_old_news` — delete old news records from DB
- `cleanup_old_posters` — delete old poster records and files
- `cleanup_triggered_meta_events` — delete already-triggered Meta pixel events
- `cleanup_orphan_news_media` — remove orphaned news media files from R2
- `cleanup_orphan_posters` — remove orphaned poster files from R2
- `cleanup_orphan_profile_pictures` — remove orphaned profile picture files from R2
- `cleanup_orphan_templates` — remove orphaned template files from R2
- `pause_underperforming_adsets` — pause Meta ad sets whose cost-per-result exceeds threshold bands; campaigns configured via `META_CAMPAIGN_IDS`; runs every hour

Inactive (schedule disabled, manual dispatch only): `parse_news_rss`, `cleanup_old_templates`, `cleanup_orphan_posters`, `cleanup_orphan_templates`, `cleanup_rejected_templates`, `update_subscription_amount`, `update_cancelled_subscription_amount`, `send_complete_signup_whatsapp`, `pause_underperforming_ads` (superseded by `pause_underperforming_adsets`)

### External Services

| Service | Package | Purpose |
|---|---|---|
| Cloudflare R2 | `pkg/storage/r2.go` | Object storage (S3-compatible) |
| Razorpay | `razorpay-go` SDK | Payments & subscriptions |
| Expo Push | `pkg/notification/expo_push.go` | Mobile push notifications |
| PostHog | `pkg/analytics/posthog.go` | Product analytics |
| Sentry | `getsentry/sentry-go` | Error tracking |
| Agora | `agora/chat`, `agora/voicecall` | RTC token generation |
| GetStream | `stream/chat` | Chat token generation |
| Google Gemini | `google.golang.org/genai` | AI (news translation/summarization) |

### Migrations

SQL migration files live in `migrations/` and are managed with [goose](https://github.com/pressly/goose). Filenames follow `YYYYMMDDHHMMSS_description.sql`.
