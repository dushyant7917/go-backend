package service

import (
	"errors"
	"fmt"
	"sync"

	"go-backend/internal/apps/autopay/models"
	metaEventSvc "go-backend/internal/apps/meta_event/service"
	metaDatasetModels "go-backend/internal/apps/metadataset/config/models"
	metaDatasetRepository "go-backend/internal/apps/metadataset/config/repository"
	posthogModels "go-backend/internal/apps/posthog/config/models"
	posthogRepository "go-backend/internal/apps/posthog/config/repository"
	userModels "go-backend/internal/apps/user/models"
	"go-backend/internal/common/constants"
	"go-backend/pkg/analytics"
	"go-backend/pkg/notification"
	"go-backend/pkg/utils"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PostHog event names. They are the same as the old Razorpay/Cashfree/PhonePe engines use so dashboards line up.
const (
	PostHogEventTrialStarted                   = "TrialStarted"
	PostHogEventSubscriptionCharged            = "SubscriptionCharged"
	PostHogEventRecurringPaymentFailed         = "RecurringPaymentFailed"
	PostHogEventSubscriptionCancelled          = "SubscriptionCancelled"
	PostHogEventOrderCreationFailed            = "OrderCreationFailed"            // the pre-debit notification call failed
	PostHogEventRecurringPaymentCreationFailed = "RecurringPaymentCreationFailed" // the debit submission call failed
)

// EventPublisher emits analytics and marketing events for payment milestones. Implementations must not block the
// caller; Wait blocks until in-flight events are delivered (cron binaries call it before exiting).
type EventPublisher interface {
	AuthorizationCompleted(m *models.AutopayMandate, a *models.AutopayChargeAttempt, user *userModels.User)
	Charged(m *models.AutopayMandate, a *models.AutopayChargeAttempt, c *models.AutopayBillingCycle, user *userModels.User)
	PaymentFailed(m *models.AutopayMandate, a *models.AutopayChargeAttempt, user *userModels.User)
	Cancelled(m *models.AutopayMandate, amount int64, user *userModels.User)
	NotificationSendFailed(m *models.AutopayMandate, amount int64, errorCode string)
	DebitSubmitFailed(m *models.AutopayMandate, amount int64, errorCode string)
	Wait()
}

// NopEventPublisher publishes nothing (tests, and apps without analytics).
type NopEventPublisher struct{}

func (NopEventPublisher) AuthorizationCompleted(*models.AutopayMandate, *models.AutopayChargeAttempt, *userModels.User) {
}
func (NopEventPublisher) Charged(*models.AutopayMandate, *models.AutopayChargeAttempt, *models.AutopayBillingCycle, *userModels.User) {
}
func (NopEventPublisher) PaymentFailed(*models.AutopayMandate, *models.AutopayChargeAttempt, *userModels.User) {
}
func (NopEventPublisher) Cancelled(*models.AutopayMandate, int64, *userModels.User)    {}
func (NopEventPublisher) NotificationSendFailed(*models.AutopayMandate, int64, string) {}
func (NopEventPublisher) DebitSubmitFailed(*models.AutopayMandate, int64, string)      {}
func (NopEventPublisher) Wait()                                                        {}

// UserFinder loads users for analytics.
type UserFinder interface {
	FindByID(id uuid.UUID) (*userModels.User, error)
}

type analyticsPublisher struct {
	metaDatasetRepo   metaDatasetRepository.MetaDatasetConfigRepository
	posthogConfigRepo posthogRepository.PostHogConfigRepository
	metaEventService  metaEventSvc.MetaEventService
	users             UserFinder
	metaDatasetClient *notification.MetaDatasetClient
	posthogClient     *analytics.PostHogClient
	wg                sync.WaitGroup
}

// NewAnalyticsPublisher builds the production EventPublisher (Meta Conversions API + PostHog).
func NewAnalyticsPublisher(
	metaDatasetRepo metaDatasetRepository.MetaDatasetConfigRepository,
	posthogConfigRepo posthogRepository.PostHogConfigRepository,
	metaEventService metaEventSvc.MetaEventService,
	users UserFinder,
) EventPublisher {
	return &analyticsPublisher{
		metaDatasetRepo: metaDatasetRepo, posthogConfigRepo: posthogConfigRepo, metaEventService: metaEventService,
		users: users, metaDatasetClient: notification.NewMetaDatasetClient(), posthogClient: analytics.NewPostHogClient(),
	}
}

func (p *analyticsPublisher) Wait() { p.wg.Wait() }

func (p *analyticsPublisher) async(fn func()) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("[autopay events] PANIC: %v\n", r)
			}
		}()
		fn()
	}()
}

func (p *analyticsPublisher) AuthorizationCompleted(m *models.AutopayMandate, a *models.AutopayChargeAttempt, user *userModels.User) {
	mc, amount := *m, a.Amount
	p.async(func() {
		p.registerMetaEvent(&mc, amount, "StartTrial", fmt.Sprintf("%s Recurring Payment", mc.AppName), user)
		p.sendPostHogEvent(PostHogEventTrialStarted, &mc, amount, user, nil)
	})
}

func (p *analyticsPublisher) Charged(m *models.AutopayMandate, a *models.AutopayChargeAttempt, c *models.AutopayBillingCycle, user *userModels.User) {
	mc, amount, cycle := *m, a.Amount, c.CycleNumber
	p.async(func() {
		p.registerMetaEvent(&mc, amount, "Purchase", fmt.Sprintf("%s Recurring Payment - Cycle %d", mc.AppName, cycle), user)
		p.sendPostHogEvent(PostHogEventSubscriptionCharged, &mc, amount, user, nil)
	})
}

func (p *analyticsPublisher) PaymentFailed(m *models.AutopayMandate, a *models.AutopayChargeAttempt, user *userModels.User) {
	mc, amount, code := *m, a.Amount, a.FailureCode
	p.async(func() { p.sendPostHogEvent(PostHogEventRecurringPaymentFailed, &mc, amount, user, optionalCode(code)) })
}

func (p *analyticsPublisher) Cancelled(m *models.AutopayMandate, amount int64, user *userModels.User) {
	mc := *m
	p.async(func() { p.sendPostHogEvent(PostHogEventSubscriptionCancelled, &mc, amount, user, nil) })
}

func (p *analyticsPublisher) NotificationSendFailed(m *models.AutopayMandate, amount int64, errorCode string) {
	mc := *m
	p.async(func() { p.sendPostHogEvent(PostHogEventOrderCreationFailed, &mc, amount, nil, optionalCode(errorCode)) })
}

func (p *analyticsPublisher) DebitSubmitFailed(m *models.AutopayMandate, amount int64, errorCode string) {
	mc := *m
	p.async(func() {
		p.sendPostHogEvent(PostHogEventRecurringPaymentCreationFailed, &mc, amount, nil, optionalCode(errorCode))
	})
}

func optionalCode(code string) *string {
	if code == "" {
		return nil
	}
	return &code
}

func reportSkip(system, appName, env string, err error) {
	fmt.Printf("[%s] skipping event for app %s (env %s): %v\n", system, appName, env, err)
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("app_name", appName)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sentry.CaptureException(fmt.Errorf("[%s] no config found for app %s, env %s", system, appName, env))
			return
		}
		sentry.CaptureException(fmt.Errorf("[%s] app %s, env %s: %w", system, appName, env, err))
	})
}

func (p *analyticsPublisher) getMetaConfig(appName string) (*metaDatasetModels.MetaDatasetConfig, bool) {
	env := utils.GetEnv("GO_ENV", "local")
	cfg, err := p.metaDatasetRepo.FindByAppNameAndEnv(appName, env)
	if err != nil {
		reportSkip("Meta Dataset", appName, env, err)
		return nil, false
	}
	if !cfg.IsActive || cfg.DatasetID == "" {
		reportSkip("Meta Dataset", appName, env, errors.New("config is inactive or has no dataset_id"))
		return nil, false
	}
	return cfg, true
}

// registerMetaEvent records a meta_events row (the pixel-dedup anchor) and sends it to Meta CAPI.
func (p *analyticsPublisher) registerMetaEvent(m *models.AutopayMandate, amount int64, eventName, contentName string, user *userModels.User) {
	value := float64(amount) / 100.0
	event, err := p.metaEventService.CreateMetaEvent(nil, m.UserID, m.AppName, eventName, map[string]interface{}{"value": value, "currency": m.Currency})
	if err != nil {
		fmt.Printf("[Meta Event ERROR] Failed to create %s meta event: %v\n", eventName, err)
		return
	}
	metaConfig, ok := p.getMetaConfig(m.AppName)
	if !ok {
		return
	}
	var phone string
	var appData notification.AppData
	if user == nil && p.users != nil {
		user, _ = p.users.FindByID(m.UserID)
	}
	if user != nil {
		if user.CountryCode != nil && user.Phone != nil {
			phone = *user.CountryCode + *user.Phone
		}
		appData = notification.AppDataFromUserMetadata(user.Metadata)
	}
	eventID := event.ID.String()
	eventData := notification.NewAppEventData(notification.MetaEventParams{
		DatasetID: metaConfig.DatasetID, AccessToken: metaConfig.AccessToken, EventName: eventName, ActionSource: "app",
		Phone: phone, UserID: m.UserID.String(), Currency: m.Currency, Value: value, ContentName: contentName,
		ContentID: eventID, DedupSource: eventID, EventID: eventID, AppData: &appData,
	})
	if err := p.metaDatasetClient.SendEvent(eventData); err != nil {
		fmt.Printf("[Meta Dataset ERROR] Failed to send %s event: %v\n", eventName, err)
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("app_name", m.AppName)
			scope.SetTag("autopay_mandate_id", m.ID.String())
			scope.SetTag("event_name", eventName)
			sentry.CaptureException(fmt.Errorf("[Meta Dataset] failed to send %s event for autopay mandate %s: %w", eventName, m.ID, err))
		})
	}
}

func (p *analyticsPublisher) getPostHogConfig(appName string) (*posthogModels.PostHogConfig, bool) {
	env := utils.GetEnv("GO_ENV", "local")
	cfg, err := p.posthogConfigRepo.FindByAppNameAndEnv(appName, env)
	if err != nil {
		reportSkip("PostHog", appName, env, err)
		return nil, false
	}
	if !cfg.IsActive {
		reportSkip("PostHog", appName, env, errors.New("config is inactive"))
		return nil, false
	}
	return cfg, true
}

// extractStateAndLanguageCode pulls state_id and language_code from user metadata (DailyStoryApp only).
func extractStateAndLanguageCode(user *userModels.User, appName string) (stateID, languageCode *string) {
	if appName != constants.AppNameDailyStory || user == nil || user.Metadata == nil {
		return nil, nil
	}
	if v, ok := user.Metadata["state_id"].(string); ok && v != "" {
		stateID = &v
	}
	if v, ok := user.Metadata["language_code"].(string); ok && v != "" {
		languageCode = &v
	}
	return stateID, languageCode
}

func (p *analyticsPublisher) sendPostHogEvent(eventName string, m *models.AutopayMandate, amount int64, user *userModels.User, errorCode *string) {
	cfg, ok := p.getPostHogConfig(m.AppName)
	if !ok {
		return
	}
	if user == nil && p.users != nil {
		user, _ = p.users.FindByID(m.UserID)
	}
	stateID, languageCode := extractStateAndLanguageCode(user, m.AppName)
	props := analytics.RecurringPaymentEventProperties{
		UserID: m.UserID, AppName: m.AppName, Amount: float64(amount) / 100.0,
		StateID: stateID, LanguageCode: languageCode, ErrorCode: errorCode,
	}
	if err := p.posthogClient.SendEvent(cfg.Host, cfg.APIKey, eventName, m.UserID.String(), props.ToProperties()); err != nil {
		fmt.Printf("[PostHog ERROR] Failed to send %s event: %v\n", eventName, err)
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("app_name", m.AppName)
			scope.SetTag("event_name", eventName)
			scope.SetTag("user_id", m.UserID.String())
			sentry.CaptureException(fmt.Errorf("[PostHog] failed to send %s event for user %s: %w", eventName, m.UserID, err))
		})
	}
}
