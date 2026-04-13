package models

import (
	"time"

	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ==================== Status Types ====================

// RecurringPaymentStatus represents the status of a recurring payment mandate
type RecurringPaymentStatus string

const (
	RecurringPaymentStatusCreated   RecurringPaymentStatus = "created"
	RecurringPaymentStatusActive    RecurringPaymentStatus = "active"
	RecurringPaymentStatusPaused    RecurringPaymentStatus = "paused"
	RecurringPaymentStatusCancelled RecurringPaymentStatus = "cancelled"
	RecurringPaymentStatusExpired   RecurringPaymentStatus = "expired"
)

// BillingCycleStatus represents the status of a billing cycle
type BillingCycleStatus string

const (
	BillingCycleStatusPending   BillingCycleStatus = "pending"
	BillingCycleStatusPaid      BillingCycleStatus = "paid"
	BillingCycleStatusFailed    BillingCycleStatus = "failed"
	BillingCycleStatusSkipped   BillingCycleStatus = "skipped"
	BillingCycleStatusCancelled BillingCycleStatus = "cancelled"
)

// PaymentAttemptStatus represents the status of a payment attempt
type PaymentAttemptStatus string

const (
	PaymentAttemptStatusCreated  PaymentAttemptStatus = "created"
	PaymentAttemptStatusPending  PaymentAttemptStatus = "pending"
	PaymentAttemptStatusCaptured PaymentAttemptStatus = "captured"
	PaymentAttemptStatusFailed   PaymentAttemptStatus = "failed"
)

// ==================== Table Models ====================

// RecurringPayment represents a UPI Autopay mandate/authorization
type RecurringPayment struct {
	ID                 uuid.UUID              `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	UserID             uuid.UUID              `gorm:"type:uuid;not null;index" json:"user_id"`
	AppName            string                 `gorm:"not null;size:100;index" json:"app_name"`
	RazorpayCustomerID *string                `gorm:"size:100;index" json:"razorpay_customer_id,omitempty"`
	TokenID            *string                `gorm:"size:100;uniqueIndex" json:"token_id,omitempty"`
	Status             RecurringPaymentStatus `gorm:"type:varchar(50);not null;default:'created';index" json:"status"`
	MaxAmount          int64                  `gorm:"not null" json:"max_amount"` // Max amount per debit in paise
	Frequency          string                 `gorm:"not null;size:50" json:"frequency"`
	StartAt            *time.Time             `json:"start_at"`
	EndAt              *time.Time             `json:"end_at"`
	LastChargedAt      *time.Time             `json:"last_charged_at"`
	NextChargeAt       *time.Time             `json:"next_charge_at"`
	Metadata           utils.Metadata         `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt          time.Time              `json:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at"`
	DeletedAt          gorm.DeletedAt         `gorm:"index" json:"deleted_at,omitempty"`
}

// BeforeCreate hook to generate UUID before creating record
func (r *RecurringPayment) BeforeCreate(tx *gorm.DB) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	if r.Metadata == nil {
		r.Metadata = make(utils.Metadata)
	}
	return nil
}

// BillingCycle represents a single billing period
type BillingCycle struct {
	ID                 uuid.UUID          `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	RecurringPaymentID uuid.UUID          `gorm:"type:uuid;not null;index" json:"recurring_payment_id"`
	CycleNumber        int                `gorm:"not null" json:"cycle_number"` // 0 for authorization, 1+ for billing
	StartAt            time.Time          `gorm:"not null" json:"start_at"`
	EndAt              *time.Time         `json:"end_at"`
	Amount             int64              `gorm:"not null" json:"amount"` // Amount in paise
	LastAttemptAt      *time.Time         `json:"last_attempt_at"`
	NextAttemptAt      *time.Time         `json:"next_attempt_at"`
	ChargeAttempts     int                `gorm:"default:0" json:"charge_attempts"`
	Status             BillingCycleStatus `gorm:"type:varchar(50);not null;default:'pending';index" json:"status"`
	Metadata           utils.Metadata     `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt          time.Time          `json:"created_at"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// BeforeCreate hook to generate UUID before creating record
func (b *BillingCycle) BeforeCreate(tx *gorm.DB) error {
	if b.ID == uuid.Nil {
		b.ID = uuid.New()
	}
	if b.Metadata == nil {
		b.Metadata = make(utils.Metadata)
	}
	return nil
}

// PaymentAttempt represents a single payment attempt
type PaymentAttempt struct {
	ID                uuid.UUID            `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	BillingCycleID    uuid.UUID            `gorm:"type:uuid;not null;index" json:"billing_cycle_id"`
	AttemptNumber     int                  `gorm:"not null" json:"attempt_number"`
	RazorpayPaymentID *string              `gorm:"size:100;uniqueIndex" json:"razorpay_payment_id,omitempty"`
	RazorpayOrderID   *string              `gorm:"size:100;uniqueIndex" json:"razorpay_order_id,omitempty"`
	Status            PaymentAttemptStatus `gorm:"type:varchar(50);not null;default:'created';index" json:"status"`
	ErrorCode         string               `gorm:"size:100" json:"error_code"`
	ErrorDescription  string               `json:"error_description"`
	Amount            int64                `gorm:"not null" json:"amount"` // Amount in paise
	Metadata          utils.Metadata       `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

// BeforeCreate hook to generate UUID before creating record
func (p *PaymentAttempt) BeforeCreate(tx *gorm.DB) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	if p.Metadata == nil {
		p.Metadata = make(utils.Metadata)
	}
	return nil
}

// ==================== Request DTOs ====================

// CreateAuthorizationOrderRequest represents the request body for creating an authorization order
type CreateAuthorizationOrderRequest struct {
	UserID              uuid.UUID `json:"user_id" binding:"required"`
	AppName             string    `json:"app_name" binding:"required,min=1,max=100"`
	AuthorizationAmount int64     `json:"authorization_amount" binding:"required,min=1"` // Amount in paise (e.g., 100 for ₹1)
	MaxAmount           int64     `json:"max_amount" binding:"required,min=1"`           // Max per debit in paise
	StartAt             time.Time `json:"start_at" binding:"required"`                   // First billing cycle start date
	Frequency           string    `json:"frequency" binding:"required,oneof=daily weekly fortnightly bimonthly monthly quarterly half_yearly yearly as_presented"`
}

// VerifyAuthorizationPaymentRequest represents the request for verifying authorization payment
type VerifyAuthorizationPaymentRequest struct {
	RazorpayOrderID   string `json:"razorpay_order_id" binding:"required"`
	RazorpayPaymentID string `json:"razorpay_payment_id" binding:"required"`
	RazorpaySignature string `json:"razorpay_signature" binding:"required"`
}

// ==================== Response DTOs ====================

// AuthorizationOrderResponse represents the response for authorization order creation
type AuthorizationOrderResponse struct {
	RecurringPaymentID uuid.UUID `json:"recurring_payment_id"`
	RazorpayOrderID    string    `json:"razorpay_order_id"`
	RazorpayCustomerID string    `json:"razorpay_customer_id"`
	Amount             int64     `json:"amount"` // Amount in paise
	Currency           string    `json:"currency"`
	KeyID              string    `json:"key_id"` // For frontend checkout
}

// CreateRegistrationLinkRequest represents the request for creating a registration link
// Uses same params as CreateAuthorizationOrderRequest for simplicity
// Note: This creates the full flow (customer + order + registration link)
type CreateRegistrationLinkRequest struct {
	UserID              uuid.UUID `json:"user_id" binding:"required"`
	AppName             string    `json:"app_name" binding:"required,min=1,max=100"`
	AuthorizationAmount int64     `json:"authorization_amount" binding:"required,min=1"` // Amount in paise
	MaxAmount           int64     `json:"max_amount" binding:"required,min=1"`           // Max per debit in paise
	StartAt             time.Time `json:"start_at" binding:"required"`                   // First billing cycle start date
	Frequency           string    `json:"frequency" binding:"required,oneof=daily weekly fortnightly bimonthly monthly quarterly half_yearly yearly as_presented"`
}

// RegistrationLinkResponse represents the response for registration link creation
type RegistrationLinkResponse struct {
	ShortURL string `json:"short_url"` // Hosted checkout URL
}

// RecurringPaymentResponse represents the response for recurring payment operations
type RecurringPaymentResponse struct {
	ID                 uuid.UUID              `json:"id"`
	UserID             uuid.UUID              `json:"user_id"`
	AppName            string                 `json:"app_name"`
	RazorpayCustomerID *string                `json:"razorpay_customer_id,omitempty"`
	TokenID            *string                `json:"token_id,omitempty"`
	Status             RecurringPaymentStatus `json:"status"`
	MaxAmount          int64                  `json:"max_amount"`
	Frequency          string                 `json:"frequency"`
	StartAt            *time.Time             `json:"start_at,omitempty"`
	EndAt              *time.Time             `json:"end_at,omitempty"`
	LastChargedAt      *time.Time             `json:"last_charged_at,omitempty"`
	NextChargeAt       *time.Time             `json:"next_charge_at,omitempty"`
	CreatedAt          time.Time              `json:"created_at"`
	UpdatedAt          time.Time              `json:"updated_at"`
}

// ToResponse converts RecurringPayment model to RecurringPaymentResponse
func (r *RecurringPayment) ToResponse() RecurringPaymentResponse {
	return RecurringPaymentResponse{
		ID:                 r.ID,
		UserID:             r.UserID,
		AppName:            r.AppName,
		RazorpayCustomerID: r.RazorpayCustomerID,
		TokenID:            r.TokenID,
		Status:             r.Status,
		MaxAmount:          r.MaxAmount,
		Frequency:          r.Frequency,
		StartAt:            r.StartAt,
		EndAt:              r.EndAt,
		LastChargedAt:      r.LastChargedAt,
		NextChargeAt:       r.NextChargeAt,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
	}
}

// RecurringPaymentStatusResponse represents the response for checking user's recurring payment status
type RecurringPaymentStatusResponse struct {
	ActiveSubscription bool `json:"active_subscription"` // Has active recurring payment mandate
	UsedFreeTrial      bool `json:"used_free_trial"`     // Ever completed authorization (availed free trial)
}
