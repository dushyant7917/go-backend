package main

import (
	"context"

	"go-backend/internal/apps/autopay/bootstrap"
)

func main() {
	bootstrap.RunCron("Autopay sync from gateway job", func(ctx context.Context, a *bootstrap.Autopay) error {
		return a.Service.SyncFromGateway(ctx)
	})
}
