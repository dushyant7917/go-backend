// Package bootstrap wires the autopay system (repository, rate limiter, gateway registry, analytics, service) from a
// database connection. The web server and every autopay cron build it the same way.
package bootstrap

import (
	"go-backend/internal/apps/autopay/adapters/cashfree"
	"go-backend/internal/apps/autopay/adapters/phonepe"
	"go-backend/internal/apps/autopay/adapters/razorpay"
	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/apps/autopay/ratelimit"
	"go-backend/internal/apps/autopay/repository"
	"go-backend/internal/apps/autopay/service"
	metaEventRepository "go-backend/internal/apps/meta_event/repository"
	metaEventService "go-backend/internal/apps/meta_event/service"
	metaDatasetRepository "go-backend/internal/apps/metadataset/config/repository"
	posthogConfigRepository "go-backend/internal/apps/posthog/config/repository"
	userRepository "go-backend/internal/apps/user/repository"
	pkgcashfree "go-backend/pkg/cashfree"
	pkgphonepe "go-backend/pkg/phonepe"
	pkgrazorpay "go-backend/pkg/razorpay"

	"gorm.io/gorm"
)

// NewRegistry returns the registry of every supported gateway. Supporting gateway N means registering one more
// adapter factory here.
func NewRegistry() *gateway.Registry {
	r := gateway.NewRegistry()
	r.Register(gateway.NameRazorpay, razorpay.NewFactory(pkgrazorpay.NewFactory()))
	r.Register(gateway.NameCashfree, cashfree.NewFactory(pkgcashfree.NewFactory()))
	r.Register(gateway.NamePhonePe, phonepe.NewFactory(pkgphonepe.NewFactory()))
	return r
}

// Autopay is the assembled autopay system.
type Autopay struct {
	Service  *service.Service
	Configs  *service.ConfigService
	Repo     repository.Repository
	Registry *gateway.Registry
}

// New assembles the autopay system.
func New(db *gorm.DB, environment string) *Autopay {
	return NewWithRepo(db, repository.New(db), environment)
}

// NewWithRepo is New with a caller-built repository, for callers that must hand the same repository to something the
// service itself depends on (the server's subscription status checker reads this repository too).
func NewWithRepo(db *gorm.DB, repo repository.Repository, environment string) *Autopay {
	registry := NewRegistry()
	users := userRepository.NewUserRepository(db)
	events := service.NewAnalyticsPublisher(
		metaDatasetRepository.NewMetaDatasetConfigRepository(db),
		posthogConfigRepository.NewPostHogConfigRepository(db),
		metaEventService.NewMetaEventService(metaEventRepository.NewMetaEventRepository(db)),
		users,
	)
	svc := service.New(service.Deps{
		Repo: repo, Gateways: service.NewConfigGatewayProvider(repo, registry), Limiter: ratelimit.New(db),
		Events: events, Users: users, Environment: environment,
	})
	return &Autopay{Service: svc, Configs: service.NewConfigService(repo, registry), Repo: repo, Registry: registry}
}
