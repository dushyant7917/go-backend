// Package models holds the GORM models of the autopay tables (migration 20261112100000_create_autopay_tables.sql).
// All datetimes are UTC; all amounts are integers in the currency's minor unit (paise for INR).
package models

import (
	"time"

	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ==================== Enumerations (values stored in the status/type columns) ====================

// PaymentType says what an autopay_mandates row is.
type PaymentType string

const (
	PaymentTypeRecurring PaymentType = "recurring" // a UPI Autopay mandate that is billed cycle after cycle
	PaymentTypeOneTime   PaymentType = "one_time"  // a single payment (order or payment link)
)

// MandateStatus is autopay_mandates.status.
type MandateStatus string

const (
	MandateStatusAwaitingAuthorization MandateStatus = "awaiting_authorization"
	MandateStatusActive                MandateStatus = "active"
	MandateStatusPaused                MandateStatus = "paused"
	MandateStatusCancelled             MandateStatus = "cancelled"
	MandateStatusExpired               MandateStatus = "expired"
)

// MetadataKeyGatewayCancelPending is the autopay_mandates.metadata key set (to true) when the core retired a recurring
// mandate itself (all attempts failed, or it was authorized after we had closed it) while the gateway's mandate is
// still live. The nightly sync cancels it at the gateway and removes the key; a live mandate keeps costing a
// maintenance fee and is never charged again.
const MetadataKeyGatewayCancelPending = "gateway_cancel_pending"

// BillingCycleStatus is autopay_billing_cycles.status.
type BillingCycleStatus string

const (
	BillingCycleStatusAwaitingPayment   BillingCycleStatus = "awaiting_payment"
	BillingCycleStatusPaid              BillingCycleStatus = "paid"
	BillingCycleStatusFailedAllAttempts BillingCycleStatus = "failed_all_attempts"
	BillingCycleStatusSkipped           BillingCycleStatus = "skipped"
	BillingCycleStatusCancelled         BillingCycleStatus = "cancelled"
)

// ChargeAttemptStatus is autopay_charge_attempts.status.
//
//	awaiting_notification  -> the attempt exists, the pre-debit notification is not yet confirmed delivered
//	notification_delivered -> delivered; the debit may be submitted once the notification is old enough
//	debit_submitted        -> the debit was submitted; the bank result is pending
//	debit_succeeded / debit_failed -> terminal (debit_failed -> debit_succeeded is the one late-success correction)
type ChargeAttemptStatus string

const (
	ChargeAttemptStatusAwaitingNotification  ChargeAttemptStatus = "awaiting_notification"
	ChargeAttemptStatusNotificationDelivered ChargeAttemptStatus = "notification_delivered"
	ChargeAttemptStatusDebitSubmitted        ChargeAttemptStatus = "debit_submitted"
	ChargeAttemptStatusDebitSucceeded        ChargeAttemptStatus = "debit_succeeded"
	ChargeAttemptStatusDebitFailed           ChargeAttemptStatus = "debit_failed"
)

// FailureCategory is autopay_charge_attempts.failure_category.
type FailureCategory string

const (
	FailureCategoryRejectedPermanently FailureCategory = "rejected_permanently"
	FailureCategoryGatewayUnavailable  FailureCategory = "gateway_unavailable"
	FailureCategoryNotificationFailed  FailureCategory = "notification_failed"
)

// WebhookEventStatus is autopay_webhook_events.status.
type WebhookEventStatus string

const (
	WebhookEventStatusReceived             WebhookEventStatus = "received"
	WebhookEventStatusProcessed            WebhookEventStatus = "processed"
	WebhookEventStatusIgnoredUnknownEntity WebhookEventStatus = "ignored_unknown_entity"
	WebhookEventStatusRefetchRequired      WebhookEventStatus = "refetch_required"
	WebhookEventStatusFailed               WebhookEventStatus = "failed"
)

// ==================== Table models ====================

// PaymentGatewayConfig is one gateway merchant account for one app and environment (payment_gateway_configs).
// EncryptedCredentials and EncryptedWebhookSecret hold AES-encrypted values (pkg/secure/crypto.go); decryption is the
// config service's job, never the model's.
type PaymentGatewayConfig struct {
	ID                     uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Gateway                string         `gorm:"not null;size:30" json:"gateway"`
	AppName                string         `gorm:"not null;size:100" json:"app_name"`
	Environment            string         `gorm:"not null;size:20" json:"environment"`
	EncryptedCredentials   utils.Metadata `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	EncryptedWebhookSecret string         `gorm:"not null;size:255;default:''" json:"-"`
	IsActive               bool           `gorm:"not null" json:"is_active"`           // set explicitly: a gorm default:true tag would turn false into true on Create
	AcceptsNewAutopay      bool           `gorm:"not null" json:"accepts_new_autopay"` // set explicitly (see IsActive)
	GatewayRatelimitRPM    *int           `json:"gateway_ratelimit_rpm,omitempty"`
	Metadata               utils.Metadata `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
	DeletedAt              gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

func (PaymentGatewayConfig) TableName() string { return "payment_gateway_configs" }

func (c *PaymentGatewayConfig) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return nil
}

// AutopayMandate is a recurring mandate or a one-time payment (autopay_mandates).
type AutopayMandate struct {
	ID                     uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"` // the autopay_mandate_id returned to the client
	UserID                 uuid.UUID      `gorm:"type:uuid;not null" json:"user_id"`
	AppName                string         `gorm:"not null;size:100" json:"app_name"`
	GatewayConfigID        uuid.UUID      `gorm:"type:uuid;not null" json:"gateway_config_id"`
	PaymentType            PaymentType    `gorm:"type:varchar(30);not null" json:"payment_type"`
	MerchantAutopayID      string         `gorm:"not null;size:100" json:"merchant_autopay_id"`
	GatewayMandateID       *string        `gorm:"size:100" json:"gateway_mandate_id,omitempty"`
	GatewayCustomerID      *string        `gorm:"size:100" json:"gateway_customer_id,omitempty"`
	Status                 MandateStatus  `gorm:"type:varchar(30);not null;default:'awaiting_authorization'" json:"status"`
	MaxDebitAmount         int64          `gorm:"not null" json:"max_debit_amount"`
	Currency               string         `gorm:"type:char(3);not null;default:'INR'" json:"currency"`
	BillingFrequency       string         `gorm:"not null;size:30" json:"billing_frequency"`
	StartAt                *time.Time     `json:"start_at"`
	EndAt                  *time.Time     `json:"end_at"`
	LastSuccessfulChargeAt *time.Time     `json:"last_successful_charge_at"`
	NextChargeDueAt        *time.Time     `json:"next_charge_due_at"`
	Metadata               utils.Metadata `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt              time.Time      `json:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at"`
	DeletedAt              gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

func (AutopayMandate) TableName() string { return "autopay_mandates" }

func (m *AutopayMandate) BeforeCreate(tx *gorm.DB) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	return nil
}

// IsOneTime reports whether this row is a one-time payment rather than a recurring mandate.
func (m *AutopayMandate) IsOneTime() bool { return m.PaymentType == PaymentTypeOneTime }

// AutopayBillingCycle is one billing period of a mandate (CycleNumber 0 = the mandate authorization, or the single
// one-time payment). Amount is in the parent mandate's currency.
type AutopayBillingCycle struct {
	ID                    uuid.UUID          `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	AutopayMandateID      uuid.UUID          `gorm:"type:uuid;not null" json:"autopay_mandate_id"`
	CycleNumber           int                `gorm:"not null" json:"cycle_number"`
	StartAt               time.Time          `gorm:"not null" json:"start_at"`
	EndAt                 *time.Time         `json:"end_at"`
	Amount                int64              `gorm:"not null" json:"amount"`
	LastChargeAttemptAt   *time.Time         `json:"last_charge_attempt_at"`
	NextChargeAttemptAt   *time.Time         `json:"next_charge_attempt_at"`
	CountedChargeAttempts int                `gorm:"not null;default:0" json:"counted_charge_attempts"`
	Status                BillingCycleStatus `gorm:"type:varchar(30);not null;default:'awaiting_payment'" json:"status"`
	Metadata              utils.Metadata     `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt             time.Time          `json:"created_at"`
	UpdatedAt             time.Time          `json:"updated_at"`
}

func (AutopayBillingCycle) TableName() string { return "autopay_billing_cycles" }

func (c *AutopayBillingCycle) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return nil
}

// AutopayChargeAttempt is one pre-debit notification plus one debit.
type AutopayChargeAttempt struct {
	ID                                uuid.UUID           `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	BillingCycleID                    uuid.UUID           `gorm:"type:uuid;not null" json:"billing_cycle_id"`
	GatewayConfigID                   uuid.UUID           `gorm:"type:uuid;not null" json:"gateway_config_id"`
	AttemptNumber                     int                 `gorm:"not null" json:"attempt_number"`
	CountsTowardAttemptLimit          bool                `gorm:"not null" json:"counts_toward_attempt_limit"` // set explicitly: true for a normal attempt, false for a gateway_unavailable one
	MerchantPaymentID                 string              `gorm:"not null;size:100" json:"merchant_payment_id"`
	GatewayOrderID                    *string             `gorm:"size:100" json:"gateway_order_id,omitempty"`
	GatewayPaymentID                  *string             `gorm:"size:100" json:"gateway_payment_id,omitempty"`
	GatewayPaymentLinkID              *string             `gorm:"size:100" json:"gateway_payment_link_id,omitempty"`
	PreDebitNotificationID            *string             `gorm:"size:120" json:"pre_debit_notification_id,omitempty"`
	PreDebitNotificationStatus        *string             `gorm:"size:50" json:"pre_debit_notification_status,omitempty"`
	PreDebitNotificationSendAttempts  int                 `gorm:"not null;default:0" json:"pre_debit_notification_send_attempts"`
	PreDebitNotificationSentAt        *time.Time          `json:"pre_debit_notification_sent_at,omitempty"`
	PreDebitNotificationDeliveredAt   *time.Time          `json:"pre_debit_notification_delivered_at,omitempty"`
	DebitExecutionID                  *string             `gorm:"size:120" json:"debit_execution_id,omitempty"`
	DebitExecutionStatus              *string             `gorm:"size:50" json:"debit_execution_status,omitempty"`
	DebitSubmitAttempts               int                 `gorm:"not null;default:0" json:"debit_submit_attempts"`
	DebitTemporaryRejectionCount      int                 `gorm:"not null;default:0" json:"debit_temporary_rejection_count"`
	DebitGatewayUnavailableRetryCount int                 `gorm:"not null;default:0" json:"debit_gateway_unavailable_retry_count"`
	Status                            ChargeAttemptStatus `gorm:"type:varchar(30);not null;default:'awaiting_notification'" json:"status"`
	FailureCategory                   *FailureCategory    `gorm:"size:40" json:"failure_category,omitempty"`
	FailureCode                       string              `gorm:"size:255" json:"failure_code"`
	FailureDescription                string              `json:"failure_description"`
	Amount                            int64               `gorm:"not null" json:"amount"`
	LastGatewayEventAt                *time.Time          `json:"last_gateway_event_at,omitempty"`
	Metadata                          utils.Metadata      `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt                         time.Time           `json:"created_at"`
	UpdatedAt                         time.Time           `json:"updated_at"`
}

func (AutopayChargeAttempt) TableName() string { return "autopay_charge_attempts" }

func (a *AutopayChargeAttempt) BeforeCreate(tx *gorm.DB) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return nil
}

// AutopayWebhookEvent is a verified webhook receipt (dedupe, audit, replay). There is no UpdatedAt/DeletedAt:
// ReceivedAt is its creation time, Status + ProcessedAt carry updates, and the retention cron hard-deletes rows.
type AutopayWebhookEvent struct {
	ID               uuid.UUID          `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	GatewayConfigID  uuid.UUID          `gorm:"type:uuid;not null" json:"gateway_config_id"`
	EventDedupeKey   string             `gorm:"not null;size:200" json:"event_dedupe_key"`
	GatewayEventType *string            `gorm:"size:100" json:"gateway_event_type,omitempty"`
	GatewayEntityID  *string            `gorm:"size:100" json:"gateway_entity_id,omitempty"`
	GatewayEventAt   *time.Time         `json:"gateway_event_at,omitempty"`
	ReceivedHeaders  utils.Metadata     `gorm:"type:jsonb;not null;default:'{}'" json:"received_headers"`
	RawBody          string             `gorm:"not null" json:"raw_body"`
	Status           WebhookEventStatus `gorm:"type:varchar(30);not null;default:'received'" json:"status"`
	ProcessingError  *string            `json:"processing_error,omitempty"`
	ReceivedAt       time.Time          `gorm:"not null;default:now()" json:"received_at"`
	ProcessedAt      *time.Time         `json:"processed_at,omitempty"`
}

func (AutopayWebhookEvent) TableName() string { return "autopay_webhook_events" }

func (e *AutopayWebhookEvent) BeforeCreate(tx *gorm.DB) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	return nil
}
