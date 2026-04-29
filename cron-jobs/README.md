# Cron Jobs

This directory contains scheduled scripts for automated tasks.

## Available Scripts

### DailyStory Push Notification Script

**File:** `send_dailystory_push_notification/main.go`

Sends push notifications to DailyStoryApp users who were created within the last 7 days and do not have an active subscription or recurring payment. The script connects directly to the database using GORM to find eligible users and sends localized notifications via Expo Push API.

#### Configuration

The script uses hardcoded app name and user age filter. Edit these constants in the script:

```go
const (
	targetAppName = "DailyStoryApp"
	userAgeDays   = 7 // Only notify users created within last 7 days
)
```

#### Usage

```bash
# Run directly with go run
go run cron-jobs/send_dailystory_push_notification/main.go

# Or compile once and use the binary (recommended for production)
go build -o cron-jobs/send_dailystory_push_notification_bin cron-jobs/send_dailystory_push_notification/main.go
./cron-jobs/send_dailystory_push_notification_bin
```

#### Environment Variables

**Database Configuration:**

- `DB_HOST` - Database host (default: `localhost`)
- `DB_PORT` - Database port (default: `5432`)
- `DB_USER` - Database user (default: `postgres`)
- `DB_PASSWORD` - Database password (default: empty)
- `DB_NAME` - Database name (default: `gobackend`)
- `DB_SSL_MODE` - SSL mode (default: `disable`)

#### Output Example

```
2026/01/04 16:30:00 [2026-01-04 16:30:00] Starting push notification for app: DailyStoryApp
2026/01/04 16:30:00 Database connection established successfully
2026/01/04 16:30:00 [2026-01-04 16:30:00] Database connected successfully
2026/01/04 16:30:00 [2026-01-04 16:30:00] Found 47 new users without active subscription
2026/01/04 16:30:01 [2026-01-04 16:30:01] Sending notifications to 47 users
2026/01/04 16:30:01 [2026-01-04 16:30:01] ✓ Push notifications sent successfully!
2026/01/04 16:30:01 [2026-01-04 16:30:01]   Success: 45, Failed: 2, Total: 47
```

### Cleanup Orphaned Profile Pictures Script

**File:** `cleanup_orphan_profile_pictures.go`

Scans the R2 bucket for profile pictures that are no longer assigned to any user in the DailyStoryApp. The script identifies orphaned files by comparing all profile pictures in the R2 bucket against the `profile_picture_key` field in users' metadata.

#### What It Does

1. Connects to the database and fetches all DailyStoryApp users
2. Extracts all active `profile_picture_key` values from user metadata
3. Lists all objects in the R2 bucket with `profile-pictures/` prefix
4. Identifies files that are NOT referenced by any user
5. Outputs a list of orphaned files (does not delete them)

#### Usage

```bash
# Run directly with go run
go run cron-jobs/cleanup_orphan_profile_pictures.go

# Or compile once and use the binary (recommended for production)
go build -o cron-jobs/cleanup_orphan_profile_pics cron-jobs/cleanup_orphan_profile_pictures.go
./cron-jobs/cleanup_orphan_profile_pics
```

#### Environment Variables

**Database Configuration:**

- `DB_HOST` - Database host (default: `localhost`)
- `DB_PORT` - Database port (default: `5432`)
- `DB_USER` - Database user (default: `postgres`)
- `DB_PASSWORD` - Database password (default: empty)
- `DB_NAME` - Database name (default: `gobackend`)
- `DB_SSL_MODE` - SSL mode (default: `disable`)

**R2 Storage Configuration:**

- `R2_ACCOUNT_ID` - Cloudflare R2 account ID (required)
- `R2_ACCESS_KEY_ID` - R2 access key ID (required)
- `R2_SECRET_ACCESS_KEY` - R2 secret access key (required)
- `R2_DS_USERS_BUCKET_NAME` - Bucket name for DailyStory users (required)

#### Output Example

```
2026/02/03 22:54:00 [2026-02-03 22:54:00] Starting orphaned profile pictures cleanup for DailyStoryApp
2026/02/03 22:54:00 Database connection established successfully
2026/02/03 22:54:00 [2026-02-03 22:54:00] ✓ Database connected successfully
2026/02/03 22:54:00 [2026-02-03 22:54:00] ✓ R2 client initialized successfully
2026/02/03 22:54:00 [2026-02-03 22:54:00] Using bucket: dailystory-users
2026/02/03 22:54:01 [2026-02-03 22:54:01] Found 1523 users in DailyStoryApp
2026/02/03 22:54:01 [2026-02-03 22:54:01] Found 1402 active profile picture keys in database
2026/02/03 22:54:02 Scanned 1450 total objects in bucket with prefix 'profile-pictures/'
2026/02/03 22:54:02 [2026-02-03 22:54:02] ⚠ Found 48 orphaned profile pictures:
2026/02/03 22:54:02 [2026-02-03 22:54:02]   1. profile-pictures/user_123_old.jpg
2026/02/03 22:54:02 [2026-02-03 22:54:02]   2. profile-pictures/deleted_user_456.jpg
2026/02/03 22:54:02 [2026-02-03 22:54:02] ✓ Cleanup scan completed successfully
2026/02/03 22:54:02 [2026-02-03 22:54:02] Note: This script only identifies orphaned files. To delete them, extend the script or manually remove.
```

#### Important Notes

- This script **ONLY IDENTIFIES** orphaned files - it does not delete them
- Run this periodically to monitor storage usage
- Review the output before manually deleting files
- Consider extending the script to automatically delete files older than a certain threshold

## Setting Up Cron Jobs

### 1. Edit Crontab

```bash
crontab -e
```

### 2. Add Cron Entry

See `crontab.example` for examples. Basic format:

```bash
# Send daily notification at 9:00 AM (using go run)
0 9 * * * cd /Users/dushyant7917/D7/go-backend && DB_HOST=localhost DB_PASSWORD=yourpass go run cron-jobs/send_dailystory_push_notification/main.go

# Or using compiled binary (faster, recommended for production)
0 9 * * * cd /Users/dushyant7917/D7/go-backend && DB_HOST=localhost DB_PASSWORD=yourpass ./cron-jobs/send_dailystory_push_notification_bin
```

### 3. Verify Crontab

```bash
crontab -l
```

## Common Cron Schedule Examples

| Schedule              | Cron Expression   | Description            |
| --------------------- | ----------------- | ---------------------- |
| Every day at 9 AM     | `0 9 * * *`       | Daily morning reminder |
| Every day at 8 PM     | `0 20 * * *`      | Daily evening reminder |
| Every Monday at 10 AM | `0 10 * * 1`      | Weekly reminder        |
| Every hour            | `0 * * * *`       | Hourly notification    |
| Every 30 minutes      | `*/30 * * * *`    | Frequent reminders     |
| 9 AM, 2 PM, 7 PM      | `0 9,14,19 * * *` | Three times daily      |

## Testing

Before scheduling, test the script manually:

```bash
# Test with local backend using go run
DB_HOST=localhost DB_PASSWORD=yourpass go run cron-jobs/send_dailystory_push_notification/main.go

# Or compile and test
go build -o cron-jobs/send_dailystory_push_notification_bin cron-jobs/send_dailystory_push_notification/main.go
DB_HOST=localhost DB_PASSWORD=yourpass ./cron-jobs/send_dailystory_push_notification_bin
```

## Production Deployment

For production servers, compile the binary first for better performance:

```bash
# Compile the binary
go build -o cron-jobs/send_dailystory_push_notification_bin cron-jobs/send_dailystory_push_notification/main.go

# Update crontab with the production database credentials
0 9 * * * cd /path/to/go-backend && DB_HOST=prod-db.example.com DB_PASSWORD=yourpass DB_NAME=production ./cron-jobs/send_dailystory_push_notification_bin >> /var/log/cron-push.log 2>&1
```

## Troubleshooting

### Script not executing

1. Check cron is running: `sudo service cron status` (Linux) or `launchctl list | grep cron` (macOS)
2. Verify Go is installed and in PATH: `which go`
3. For compiled binary, verify it has execute permissions: `chmod +x cron-jobs/send_dailystory_push_notification_bin`
4. Use absolute paths in crontab
5. Check cron logs: `grep CRON /var/log/syslog` (Linux) or `log show --predicate 'process == "cron"' --last 1h` (macOS)

### No notifications sent

1. Verify database connection settings
2. Check users have `push_notification_token` in metadata
3. Verify `app_name` matches database records
4. Ensure database is accessible from the server running the cron job

## Adding New Cron Scripts

When adding new scripts to this directory:

1. Create the script file in `cron-jobs/`
2. Make it executable: `chmod +x cron-jobs/your_script.sh`
3. Test manually first
4. Add example to `crontab.example`
5. Document it in this README
