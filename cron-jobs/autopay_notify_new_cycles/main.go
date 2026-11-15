package main

import (
	"context"

	"go-backend/internal/apps/autopay/bootstrap"
)

func main() {
	bootstrap.RunCron("Autopay notify new cycles job", func(ctx context.Context, a *bootstrap.Autopay) error {
		return a.Service.NotifyNewCycles(ctx)
	})
}
