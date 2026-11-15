// cleanup_old_autopay_webhook_events deletes finished autopay webhook receipts (processed or ignored) older than the
// retention window, in batches, and alerts when unfinished ones are piling up. Usage: go run main.go [days] (default 30).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"go-backend/internal/apps/autopay/bootstrap"

	"github.com/getsentry/sentry-go"
)

const (
	defaultRetentionDays = 30
	batchSize            = 1000
	// unfinishedAlertAfter: a webhook event still received/refetch_required/failed this long after arrival is stuck work.
	unfinishedAlertAfter = 24 * time.Hour
)

func main() {
	days := defaultRetentionDays
	if len(os.Args) > 1 {
		n, err := strconv.Atoi(os.Args[1])
		if err != nil || n <= 0 {
			log.Fatalf("Error: days must be a positive integer, got: %s\nUsage: go run main.go [days]", os.Args[1])
		}
		days = n
	}

	bootstrap.RunCron("Cleanup old autopay webhook events job", func(ctx context.Context, a *bootstrap.Autopay) error {
		cutoff := time.Now().UTC().AddDate(0, 0, -days)
		var total int64
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := a.Repo.DeleteFinishedWebhookEventsBefore(cutoff, batchSize)
			if err != nil {
				return fmt.Errorf("delete autopay webhook events: %w", err)
			}
			total += n
			if n < batchSize {
				break
			}
		}
		log.Printf("Deleted %d finished autopay webhook events received before %s\n", total, cutoff.Format(time.RFC3339))

		stuck, err := a.Repo.FindUnprocessedWebhookEvents(time.Now().UTC().Add(-unfinishedAlertAfter), 1)
		if err != nil {
			return fmt.Errorf("check unfinished autopay webhook events: %w", err)
		}
		if len(stuck) > 0 {
			msg := fmt.Sprintf("autopay webhook events are stuck unprocessed (oldest received %s, status %s)", stuck[0].ReceivedAt.Format(time.RFC3339), stuck[0].Status)
			log.Printf("ALERT: %s\n", msg)
			sentry.CaptureMessage("[autopay] " + msg)
		}
		return nil
	})
}
