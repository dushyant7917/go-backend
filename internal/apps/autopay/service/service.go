package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/apps/autopay/models"
	"go-backend/internal/apps/autopay/ratelimit"
	"go-backend/internal/apps/autopay/repository"
	userModels "go-backend/internal/apps/user/models"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	ErrNotFound            = errors.New("not found")
	ErrInvalidRequest      = errors.New("invalid request")
	ErrGatewayNotAvailable = errors.New("gateway not available")
	ErrGatewayUnavailable  = errors.New("the payment gateway is temporarily unavailable")
)

// Deps are the collaborators of Service.
type Deps struct {
	Repo        repository.Repository
	Gateways    GatewayResolver
	Limiter     TokenTaker
	Events      EventPublisher
	Users       UserFinder
	Environment string                                           // GO_ENV: selects the config when an autopay is created
	Engine      *Engine                                          // optional; defaults to NewEngine()
	Now         func() time.Time                                 // optional; defaults to the UTC clock
	Sleep       func(ctx context.Context, d time.Duration) error // optional; tests replace the retry backoff
}

// Service is the gateway-agnostic autopay core: the API operations, the four crons and the webhook handler.
type Service struct {
	repo     repository.Repository
	gateways GatewayResolver
	limiter  TokenTaker
	events   EventPublisher
	users    UserFinder
	env      string
	engine   *Engine
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
}

// New builds a Service.
func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	engine := d.Engine
	if engine == nil {
		engine = NewEngine()
		engine.Now = now
	}
	events := d.Events
	if events == nil {
		events = NopEventPublisher{}
	}
	return &Service{
		repo: d.Repo, gateways: d.Gateways, limiter: d.Limiter, events: events, users: d.Users,
		env: d.Environment, engine: engine, now: now, sleep: d.Sleep,
	}
}

// WaitForEvents blocks until in-flight analytics events are delivered; cron mains call it before exiting.
func (s *Service) WaitForEvents() { s.events.Wait() }

// ==================== Gateway calls ====================

// policyFor builds the call policy (rate-limit token per try + retries) for a resolved gateway.
func (s *Service) policyFor(rg *ResolvedGateway, class ratelimit.Class) CallPolicy {
	return CallPolicy{
		Limiter: s.limiter, ConfigID: rg.Config.ID, RequestsPerMinute: rg.RequestsPerMinute, Class: class,
		RetryUnknownOutcome: rg.Gateway.Capabilities().SupportsIdempotencyKeys, sleep: s.sleep,
	}
}

// ==================== Applying outcomes ====================

// inTx runs fn in a transaction: committed when fn returns nil, rolled back when it returns an error or panics (the
// batch runner recovers the panic, but nothing else would end the transaction and release its row locks).
func (s *Service) inTx(fn func(tx *gorm.DB) error) error {
	tx := s.repo.BeginTransaction()
	defer func() {
		if r := recover(); r != nil {
			_ = s.repo.RollbackTransaction(tx)
			panic(r)
		}
	}()
	if err := fn(tx); err != nil {
		_ = s.repo.RollbackTransaction(tx)
		return err
	}
	return s.repo.CommitTransaction(tx)
}

// mutateAttempt applies fn to an attempt (with its cycle and mandate) inside one transaction under row locks, so a
// webhook racing a cron cannot both win, persists the result and publishes its effects after the commit.
func (s *Service) mutateAttempt(attemptID uuid.UUID, fn func(State) Effects) error {
	var st State
	var fx Effects
	err := s.inTx(func(tx *gorm.DB) error {
		a, c, m, err := s.repo.LockChargeAttemptState(tx, attemptID)
		if err != nil {
			return err
		}
		st = State{Mandate: m, Cycle: c, Attempt: a}
		fx = fn(st)
		if !fx.Changed {
			return nil
		}
		return s.saveState(tx, st)
	})
	if err != nil {
		return err
	}
	s.publish(fx, st)
	return nil
}

// mutateMandate applies fn to a mandate and its latest cycle under row locks.
func (s *Service) mutateMandate(mandateID uuid.UUID, fn func(m *models.AutopayMandate, latest *models.AutopayBillingCycle) Effects) error {
	var st State
	var fx Effects
	err := s.inTx(func(tx *gorm.DB) error {
		m, latest, err := s.repo.LockMandate(tx, mandateID)
		if err != nil {
			return err
		}
		st = State{Mandate: m, Cycle: latest}
		fx = fn(m, latest)
		if !fx.Changed {
			return nil
		}
		if latest != nil {
			if err := s.repo.UpdateBillingCycle(tx, latest); err != nil {
				return err
			}
		}
		return s.repo.UpdateMandate(tx, m)
	})
	if err != nil {
		return err
	}
	s.publish(fx, st)
	return nil
}

// saveState persists the attempt, cycle and mandate of a State inside tx.
func (s *Service) saveState(tx *gorm.DB, st State) error {
	if err := s.repo.UpdateChargeAttempt(tx, st.Attempt); err != nil {
		return fmt.Errorf("update charge attempt %s: %w", st.Attempt.ID, err)
	}
	if err := s.repo.UpdateBillingCycle(tx, st.Cycle); err != nil {
		return fmt.Errorf("update billing cycle %s: %w", st.Cycle.ID, err)
	}
	if err := s.repo.UpdateMandate(tx, st.Mandate); err != nil {
		return fmt.Errorf("update autopay mandate %s: %w", st.Mandate.ID, err)
	}
	return nil
}

// alert tells a human (log + Sentry) about a state that needs a look: money possibly moved twice, or a record the
// system cannot act on.
func alert(msg string) {
	fmt.Printf("[autopay ALERT] %s\n", msg)
	sentry.CaptureMessage("[autopay] " + msg)
}

func (s *Service) publish(fx Effects, st State) {
	if fx.Alert != "" {
		alert(fx.Alert)
	}
	var user *userModels.User
	loadUser := func() *userModels.User {
		if user == nil && s.users != nil && st.Mandate != nil {
			user, _ = s.users.FindByID(st.Mandate.UserID)
		}
		return user
	}
	switch {
	case fx.AuthorizationCompleted && st.Attempt != nil:
		s.events.AuthorizationCompleted(st.Mandate, st.Attempt, loadUser())
		// an authorization that was a real charge (amount == max) is also a purchase
		if st.Attempt.Amount == st.Mandate.MaxDebitAmount && st.Cycle != nil {
			s.events.Charged(st.Mandate, st.Attempt, st.Cycle, loadUser())
		}
	case fx.Charged && st.Attempt != nil && st.Cycle != nil:
		s.events.Charged(st.Mandate, st.Attempt, st.Cycle, loadUser())
	}
	if fx.PaymentFailed && st.Attempt != nil {
		s.events.PaymentFailed(st.Mandate, st.Attempt, loadUser())
	}
	if fx.MandateCancelled && st.Mandate != nil {
		amount := st.Mandate.MaxDebitAmount
		if st.Cycle != nil {
			amount = st.Cycle.Amount
		}
		s.events.Cancelled(st.Mandate, amount, loadUser())
	}
}

// ==================== Small shared helpers ====================

// loadCycleAndMandate loads the billing cycle an attempt belongs to and that cycle's mandate.
func (s *Service) loadCycleAndMandate(billingCycleID uuid.UUID) (*models.AutopayBillingCycle, *models.AutopayMandate, error) {
	cycle, err := s.repo.FindBillingCycleByID(billingCycleID)
	if err != nil {
		return nil, nil, err
	}
	m, err := s.repo.FindMandateByID(cycle.AutopayMandateID)
	if err != nil {
		return nil, nil, err
	}
	return cycle, m, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func mandateRef(m *models.AutopayMandate) gateway.MandateRef {
	return gateway.MandateRef{
		MerchantAutopayID:              m.MerchantAutopayID,
		GatewayMandateID:               deref(m.GatewayMandateID),
		GatewayCustomerID:              deref(m.GatewayCustomerID),
		AuthorizationMerchantPaymentID: AuthorizationMerchantPaymentID(m.ID),
	}
}

func attemptRef(m *models.AutopayMandate, a *models.AutopayChargeAttempt) gateway.ChargeAttemptRef {
	return gateway.ChargeAttemptRef{
		Mandate:                mandateRef(m),
		MerchantPaymentID:      a.MerchantPaymentID,
		GatewayOrderID:         deref(a.GatewayOrderID),
		GatewayPaymentID:       deref(a.GatewayPaymentID),
		PreDebitNotificationID: deref(a.PreDebitNotificationID),
		DebitExecutionID:       deref(a.DebitExecutionID),
	}
}

func userInfo(u *userModels.User) gateway.UserInfo {
	info := gateway.UserInfo{UserID: u.ID.String(), Name: deref(u.Name), Email: deref(u.Email), Phone: deref(u.Phone), CountryCode: deref(u.CountryCode)}
	return info
}

// processInParallel runs processor over items with bounded concurrency, recovering from panics so one bad row does
// not stop the run.
func processInParallel[T any](items []T, processor func(T) error, logPrefix string) {
	const maxConcurrency = 5
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(item T) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[%s] PANIC: %v\n", logPrefix, r)
					sentry.CaptureException(fmt.Errorf("[%s] panic: %v", logPrefix, r))
				}
			}()
			if err := processor(item); err != nil {
				fmt.Printf("[%s] ERROR: %v\n", logPrefix, err)
			}
		}(item)
	}
	wg.Wait()
}

// forEachConfig runs fn for every active config (merchant account), isolating failures per config.
func (s *Service) forEachConfig(ctx context.Context, name string, fn func(ctx context.Context, rg *ResolvedGateway) error) error {
	configs, err := s.gateways.ActiveConfigs(ctx)
	if err != nil {
		return fmt.Errorf("list gateway configs: %w", err)
	}
	var failed int
	for _, cfg := range configs {
		rg, err := s.gateways.Resolve(ctx, cfg.ID)
		if err != nil {
			failed++
			fmt.Printf("[%s] config %s (%s/%s): %v\n", name, cfg.ID, cfg.Gateway, cfg.AppName, err)
			sentry.WithScope(func(scope *sentry.Scope) {
				scope.SetTag("gateway", cfg.Gateway)
				scope.SetTag("app_name", cfg.AppName)
				sentry.CaptureException(fmt.Errorf("[%s] cannot use gateway config %s: %w", name, cfg.ID, err))
			})
			continue
		}
		if err := fn(ctx, rg); err != nil {
			failed++
			fmt.Printf("[%s] config %s (%s/%s): %v\n", name, cfg.ID, cfg.Gateway, cfg.AppName, err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%s: %d of %d gateway configs had errors", name, failed, len(configs))
	}
	return nil
}
