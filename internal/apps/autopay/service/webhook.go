package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/apps/autopay/models"
	"go-backend/internal/apps/autopay/ratelimit"
	"go-backend/pkg/utils"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrInvalidWebhookSignature is returned (HTTP 401) when a webhook's signature does not verify.
var ErrInvalidWebhookSignature = errors.New("invalid webhook signature")

// webhookProcessingBudget bounds the gateway calls (re-fetch, rate-limit waits) made while a webhook request is open.
const webhookProcessingBudget = 4 * time.Second

// errUnknownEntity means an event refers to an entity this system does not own (another config, the old Razorpay
// system, a dashboard test ping). It is acknowledged and ignored.
var errUnknownEntity = errors.New("unknown entity")

// HandleWebhook verifies, stores and processes one webhook delivery (policy: the payload is signed, so it is
// applied directly; the gateway is re-fetched only on an exception or an unexpected payload). The config id
// comes from the URL (it identifies the gateway, so the URL carries no gateway name), so the signature is checked with that config's secret before any body field is read.
//
// It returns nil whenever the delivery was accepted (including duplicates and events for unknown entities) so the
// gateway stops retrying, ErrNotFound for an unknown or inactive config, ErrInvalidWebhookSignature for a bad
// signature, and an error only when the delivery could not be stored (the gateway then retries).
func (s *Service) HandleWebhook(ctx context.Context, configID uuid.UUID, headers http.Header, rawBody []byte) error {
	cfg, err := s.repo.FindConfigByID(configID)
	if err != nil || !cfg.IsActive {
		return fmt.Errorf("%w: webhook config %s", ErrNotFound, configID)
	}
	rg, err := s.gateways.Resolve(ctx, configID)
	if err != nil {
		return err
	}

	events, parseErr := rg.Gateway.VerifyAndParseWebhook(headers, rawBody)
	if errors.Is(parseErr, gateway.ErrInvalidSignature) {
		return ErrInvalidWebhookSignature
	}

	row := &models.AutopayWebhookEvent{
		GatewayConfigID: configID, EventDedupeKey: dedupeKeyOf(events, rawBody), ReceivedHeaders: headersToMetadata(headers),
		RawBody: string(rawBody), Status: models.WebhookEventStatusReceived,
	}
	if len(events) > 0 {
		// bounded: an oversized value must never make the insert (and so the acknowledgement) fail
		row.GatewayEventType = strPtr(utils.Truncate(events[0].GatewayEventType, 100))
		row.GatewayEntityID = strPtr(utils.Truncate(utils.FirstNonEmpty(events[0].GatewayOrderID, events[0].GatewayPaymentID, events[0].GatewayMandateID, events[0].MerchantPaymentID), 100))
		if !events[0].GatewayEventAt.IsZero() {
			t := events[0].GatewayEventAt.UTC()
			row.GatewayEventAt = &t
		}
	}
	inserted, err := s.repo.InsertWebhookEvent(row)
	if err != nil {
		return fmt.Errorf("store webhook event: %w", err) // 5xx: the gateway retries
	}
	if !inserted {
		return nil // duplicate delivery
	}

	if parseErr != nil { // signature verified but the payload is unexpected: keep it for a human and for replay
		msg := parseErr.Error()
		sentry.CaptureException(fmt.Errorf("[autopay webhook] unexpected %s payload for config %s: %w", cfg.Gateway, configID, parseErr))
		return s.repo.MarkWebhookEvent(row.ID, models.WebhookEventStatusFailed, msg)
	}
	// Gateways time out a slow acknowledgement (Razorpay: 5s) and then retry; the delivery is already stored, so bound the
	// gateway calls processing may make and leave anything unfinished (refetch_required) to the sync cron's replay.
	procCtx, cancel := context.WithTimeout(ctx, webhookProcessingBudget)
	defer cancel()
	s.finishWebhookEvent(procCtx, rg, row.ID, events)
	return nil
}

// finishWebhookEvent applies the events of a stored delivery and records the result on the row.
func (s *Service) finishWebhookEvent(ctx context.Context, rg *ResolvedGateway, rowID uuid.UUID, events []gateway.Event) {
	status, msg := s.processEvents(ctx, rg, events)
	if err := s.repo.MarkWebhookEvent(rowID, status, msg); err != nil {
		fmt.Printf("[autopay webhook] cannot mark event %s %s: %v\n", rowID, status, err)
	}
}

// processEvents applies a delivery's events and returns the status for its row.
func (s *Service) processEvents(ctx context.Context, rg *ResolvedGateway, events []gateway.Event) (models.WebhookEventStatus, string) {
	if len(events) == 0 {
		return models.WebhookEventStatusProcessed, "no actionable events"
	}
	unknown := 0
	var failures []string
	needsRefetch := false
	for _, ev := range events {
		err := s.processEvent(rg, ev)
		switch {
		case err == nil:
		case errors.Is(err, errUnknownEntity):
			unknown++
		default:
			// exception while applying: ask the gateway instead of trusting the payload
			if rerr := s.refetchAndApply(ctx, rg, ev); errors.Is(rerr, errUnknownEntity) {
				unknown++ // the entity is not ours after all (e.g. another system's): nothing to re-fetch, ever
			} else if rerr != nil {
				needsRefetch = true
				failures = append(failures, fmt.Sprintf("%s: %v; refetch: %v", ev.Type, err, rerr))
			}
		}
	}
	switch {
	case needsRefetch:
		return models.WebhookEventStatusRefetchRequired, strings.Join(failures, " | ")
	case unknown == len(events):
		return models.WebhookEventStatusIgnoredUnknownEntity, ""
	default:
		return models.WebhookEventStatusProcessed, ""
	}
}

// processEvent applies one normalized event through the transition rules.
func (s *Service) processEvent(rg *ResolvedGateway, ev gateway.Event) error {
	cfgID := rg.Config.ID
	switch ev.Type {
	case gateway.EventMandateStatusChanged:
		m, err := s.findMandateForEvent(cfgID, ev)
		if err != nil {
			return err
		}
		err = s.mutateMandate(m.ID, func(mm *models.AutopayMandate, latest *models.AutopayBillingCycle) Effects {
			return s.engine.OnMandateStatusChanged(mm, latest, ev.MandateState)
		})
		return err

	case gateway.EventMandateAuthorizationSucceeded, gateway.EventMandateAuthorizationFailed:
		m, err := s.findMandateForEvent(cfgID, ev)
		if err != nil {
			return err
		}
		attempt, err := s.repo.FindAuthorizationChargeAttempt(m.ID)
		if err != nil {
			return errUnknownEntity
		}
		if ev.Type == gateway.EventMandateAuthorizationSucceeded {
			return s.applyAuthorizationStatus(attempt.ID, &gateway.MandateAuthorizationStatus{
				State: gateway.AuthorizationSucceeded, GatewayMandateID: ev.GatewayMandateID, GatewayPaymentID: ev.GatewayPaymentID,
			})
		}
		return s.applyAuthorizationStatus(attempt.ID, &gateway.MandateAuthorizationStatus{
			State: gateway.AuthorizationFailed, FailureCode: ev.FailureCode, FailureDescription: ev.FailureDescription,
		})

	default: // attempt-level events
		a, err := s.findAttemptForEvent(cfgID, ev)
		if err != nil {
			return err
		}
		return s.applyAttemptEvent(a.ID, ev)
	}
}

// applyAttemptEvent applies an attempt-level event. Ordering is decided by the state-transition rules; on top of
// them an event older than the newest one already applied is dropped unless it is a success (which may always jump
// forward).
func (s *Service) applyAttemptEvent(attemptID uuid.UUID, ev gateway.Event) error {
	isSuccess := ev.Type == gateway.EventAutopayChargeSucceeded || ev.Type == gateway.EventOneTimePaymentSucceeded
	return s.mutateAttempt(attemptID, func(st State) Effects {
		a := st.Attempt
		if !isSuccess && !ev.GatewayEventAt.IsZero() && a.LastGatewayEventAt != nil && ev.GatewayEventAt.Before(*a.LastGatewayEventAt) {
			return Effects{Ignored: "stale_event"}
		}
		if isSuccess && ev.Amount > 0 && ev.Amount < a.Amount {
			return Effects{Ignored: "amount_mismatch", Alert: fmt.Sprintf("autopay %s success event paid %d but the attempt expects %d: ignored", a.MerchantPaymentID, ev.Amount, a.Amount)}
		}
		if ev.GatewayPaymentID != "" && a.GatewayPaymentID == nil {
			a.GatewayPaymentID = strPtr(ev.GatewayPaymentID)
		}
		if ev.GatewayOrderID != "" && a.GatewayOrderID == nil {
			a.GatewayOrderID = strPtr(ev.GatewayOrderID)
		}

		var fx Effects
		switch ev.Type {
		case gateway.EventPreDebitNotificationDelivered:
			fx = s.engine.OnPreDebitNotificationDelivered(st, ev.DeliveredAt)
		case gateway.EventPreDebitNotificationFailed:
			fx = s.engine.OnPreDebitNotificationFailed(st, ev.FailureCode, ev.FailureDescription)
		case gateway.EventAutopayChargeSucceeded, gateway.EventOneTimePaymentSucceeded:
			switch {
			case st.Mandate.PaymentType == models.PaymentTypeOneTime:
				fx = s.engine.OnOneTimePaymentSucceeded(st)
			case st.Cycle.CycleNumber == 0:
				fx = s.engine.OnMandateAuthorizationSucceeded(st, ev.GatewayMandateID, ev.GatewayPaymentID)
			default:
				fx = s.engine.OnChargeSucceeded(st, s.laterAttemptExists(a))
			}
		case gateway.EventAutopayChargeFailed, gateway.EventOneTimePaymentFailed:
			if ev.FailureTemporary && st.Cycle.CycleNumber >= 1 && a.Status == models.ChargeAttemptStatusDebitSubmitted {
				fx = s.temporaryRejection(st, ev.FailureCode, ev.FailureDescription)
			} else {
				fx = s.engine.OnChargeFailed(st, permanentFailure(ev.FailureCode, ev.FailureDescription))
			}
		default:
			return Effects{Ignored: "unsupported_event"}
		}
		if fx.Changed && !ev.GatewayEventAt.IsZero() {
			t := ev.GatewayEventAt.UTC()
			if a.LastGatewayEventAt == nil || t.After(*a.LastGatewayEventAt) {
				a.LastGatewayEventAt = &t
			}
		}
		return fx
	})
}

// refetchAndApply is the fallback for an event that could not be applied: it asks the gateway for the entity's real
// state and applies that.
func (s *Service) refetchAndApply(ctx context.Context, rg *ResolvedGateway, ev gateway.Event) error {
	switch ev.Type {
	case gateway.EventMandateStatusChanged:
		m, err := s.findMandateForEvent(rg.Config.ID, ev)
		if err != nil {
			return err
		}
		st, err := Call(ctx, s.policyFor(rg, ratelimit.Background), func(ctx context.Context) (*gateway.MandateStatus, error) {
			return rg.Gateway.FetchMandateStatus(ctx, mandateRef(m))
		})
		if err != nil {
			return err
		}
		err = s.mutateMandate(m.ID, func(mm *models.AutopayMandate, latest *models.AutopayBillingCycle) Effects {
			return s.engine.OnMandateStatusChanged(mm, latest, st.State)
		})
		return err

	case gateway.EventMandateAuthorizationSucceeded, gateway.EventMandateAuthorizationFailed,
		gateway.EventOneTimePaymentSucceeded, gateway.EventOneTimePaymentFailed:
		a, err := s.findAttemptOrAuthorizationForEvent(rg.Config.ID, ev)
		if err != nil {
			return err
		}
		_, m, err := s.loadCycleAndMandate(a.BillingCycleID)
		if err != nil {
			return err
		}
		return s.syncAuthorizationOrPayment(ctx, rg, m, a, ratelimit.Background)

	default:
		a, err := s.findAttemptForEvent(rg.Config.ID, ev)
		if err != nil {
			return err
		}
		cycle, m, err := s.loadCycleAndMandate(a.BillingCycleID)
		if err != nil {
			return err
		}
		if cycle.CycleNumber == 0 {
			return s.syncAuthorizationOrPayment(ctx, rg, m, a, ratelimit.Background)
		}
		st, err := Call(ctx, s.policyFor(rg, ratelimit.Background), func(ctx context.Context) (*gateway.ChargeAttemptStatus, error) {
			return rg.Gateway.FetchChargeAttemptStatus(ctx, attemptRef(m, a))
		})
		if err != nil {
			return err
		}
		return s.applyChargeStatus(a.ID, st)
	}
}

// ==================== Finding the entity an event refers to ====================

func (s *Service) findMandateForEvent(cfgID uuid.UUID, ev gateway.Event) (*models.AutopayMandate, error) {
	var m *models.AutopayMandate
	var err error
	switch {
	case ev.MerchantAutopayID != "":
		m, err = s.repo.FindMandateByMerchantAutopayID(ev.MerchantAutopayID)
	case ev.GatewayMandateID != "":
		m, err = s.repo.FindMandateByGatewayMandateID(cfgID, ev.GatewayMandateID)
	default:
		a, aerr := s.findAttemptForEvent(cfgID, ev)
		if aerr != nil {
			return nil, aerr
		}
		cycle, cerr := s.repo.FindBillingCycleByID(a.BillingCycleID)
		if cerr != nil {
			return nil, cerr
		}
		m, err = s.repo.FindMandateByID(cycle.AutopayMandateID)
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errUnknownEntity
		}
		return nil, err
	}
	if m.GatewayConfigID != cfgID {
		return nil, errUnknownEntity // another merchant account's mandate
	}
	return m, nil
}

func (s *Service) findAttemptForEvent(cfgID uuid.UUID, ev gateway.Event) (*models.AutopayChargeAttempt, error) {
	lookups := []func() (*models.AutopayChargeAttempt, error){}
	if ev.MerchantPaymentID != "" {
		lookups = append(lookups, func() (*models.AutopayChargeAttempt, error) {
			return s.repo.FindChargeAttemptByMerchantPaymentID(ev.MerchantPaymentID)
		})
	}
	if ev.GatewayOrderID != "" {
		lookups = append(lookups, func() (*models.AutopayChargeAttempt, error) {
			return s.repo.FindChargeAttemptByGatewayOrderID(cfgID, ev.GatewayOrderID)
		})
	}
	if ev.GatewayPaymentID != "" {
		lookups = append(lookups, func() (*models.AutopayChargeAttempt, error) {
			return s.repo.FindChargeAttemptByGatewayPaymentID(cfgID, ev.GatewayPaymentID)
		})
	}
	if ev.GatewayPaymentLinkID != "" {
		lookups = append(lookups, func() (*models.AutopayChargeAttempt, error) {
			return s.repo.FindChargeAttemptByGatewayPaymentLinkID(cfgID, ev.GatewayPaymentLinkID)
		})
	}
	for _, find := range lookups {
		a, err := find()
		if err == nil {
			if a.GatewayConfigID != cfgID {
				return nil, errUnknownEntity
			}
			return a, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	return nil, errUnknownEntity
}

// findAttemptOrAuthorizationForEvent finds the attempt of an authorization or one-time event: by the attempt ids, or
// through the mandate's cycle-0 attempt.
func (s *Service) findAttemptOrAuthorizationForEvent(cfgID uuid.UUID, ev gateway.Event) (*models.AutopayChargeAttempt, error) {
	if a, err := s.findAttemptForEvent(cfgID, ev); err == nil {
		return a, nil
	}
	m, err := s.findMandateForEvent(cfgID, ev)
	if err != nil {
		return nil, err
	}
	a, err := s.repo.FindAuthorizationChargeAttempt(m.ID)
	if err != nil {
		return nil, errUnknownEntity
	}
	return a, nil
}

// ==================== Replay ====================

// reprocessWebhookEvents finishes events that were stored but never processed (the process died, or applying and
// re-fetching both failed). Called by the sync cron.
func (s *Service) reprocessWebhookEvents(ctx context.Context, rg *ResolvedGateway) error {
	rows, err := s.repo.FindUnprocessedWebhookEvents(s.now().Add(-5*time.Minute), 200)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.GatewayConfigID != rg.Config.ID {
			continue
		}
		events, err := rg.Gateway.VerifyAndParseWebhook(metadataToHeaders(row.ReceivedHeaders), []byte(row.RawBody))
		if err != nil {
			_ = s.repo.MarkWebhookEvent(row.ID, models.WebhookEventStatusFailed, "replay: "+err.Error())
			continue
		}
		s.finishWebhookEvent(ctx, rg, row.ID, events)
	}
	return nil
}

// ==================== Small helpers ====================

func dedupeKeyOf(events []gateway.Event, rawBody []byte) string {
	for _, ev := range events {
		if ev.DedupeKey != "" {
			return utils.Truncate(ev.DedupeKey, 200)
		}
	}
	sum := sha256.Sum256(rawBody)
	return hex.EncodeToString(sum[:])
}

// headersToMetadata keeps the first value of each header for the audit trail. Credential-bearing headers are
// dropped; the signature itself is kept because replay re-verifies it.
func headersToMetadata(h http.Header) utils.Metadata {
	out := utils.Metadata{}
	for k, v := range h {
		if len(v) == 0 || strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Cookie") {
			continue
		}
		out[k] = v[0]
	}
	return out
}

func metadataToHeaders(md utils.Metadata) http.Header {
	h := http.Header{}
	for k, v := range md {
		if s, ok := v.(string); ok {
			h.Set(k, s)
		}
	}
	return h
}
