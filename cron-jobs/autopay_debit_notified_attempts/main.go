package main

import (
	"context"

	"go-backend/internal/apps/autopay/bootstrap"
)

func main() {
	bootstrap.RunCron("Autopay debit notified attempts job", func(ctx context.Context, a *bootstrap.Autopay) error {
		return a.Service.DebitNotifiedAttempts(ctx)
	})
}
