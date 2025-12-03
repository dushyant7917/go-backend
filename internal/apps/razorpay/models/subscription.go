package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SubscriptionStatus represents the status of a subscription
type SubscriptionStatus string

const (
	SubscriptionStatusCreated       SubscriptionStatus = "created"
	SubscriptionStatusAuthenticated SubscriptionStatus = "authenticated"
	SubscriptionStatusActive        SubscriptionStatus = "active"
	SubscriptionStatusPaused        SubscriptionStatus = "paused"
	SubscriptionStatusCancelled     SubscriptionStatus = "cancelled"
	SubscriptionStatusCompleted     SubscriptionStatus = "completed"
	SubscriptionStatusExpired       SubscriptionStatus = "expired"
)

// Subscription represents a UPI Autopay subscription in the database
type Subscription struct {
	ID                     uuid.UUID          `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	UserID                 uuid.UUID          `gorm:"type:uuid;not null;index" json:"user_id" binding:"required"`
	AppName                string             `gorm:"not null;size:100;index" json:"app_name" binding:"required"`
	Phone                  string             `gorm:"not null;size:15" json:"phone" binding:"required"`
	Email                  string             `gorm:"not null;size:255" json:"email" binding:"required,email"`
	RazorpaySubscriptionID string             `gorm:"size:100;uniqueIndex" json:"razorpay_subscription_id"`
	RazorpayCustomerID     string             `gorm:"size:100;index" json:"razorpay_customer_id"`
	RazorpayPlanID         string             `gorm:"size:100" json:"razorpay_plan_id"`
	Status                 SubscriptionStatus `gorm:"type:varchar(50);default:'created';index" json:"status"`
	Amount                 int64              `gorm:"not null" json:"amount"` // Amount in paise
	Currency               string             `gorm:"size:10;default:'INR'" json:"currency"`
	MaxAmount              int64              `json:"max_amount"`               // Max amount per debit in paise
	Frequency              string             `gorm:"size:50" json:"frequency"` // daily, weekly, monthly, yearly
	TotalCount             int                `json:"total_count"`              // Total number of charges
	StartAt                *time.Time         `json:"start_at"`
	EndAt                  *time.Time         `json:"end_at"`
	NextChargeAt           *time.Time         `json:"next_charge_at"`
	ShortURL               string             `gorm:"size:500" json:"short_url"`
	Metadata               string             `gorm:"type:jsonb" json:"metadata"` // Additional metadata as JSON
	CreatedAt              time.Time          `json:"created_at"`
	UpdatedAt              time.Time          `json:"updated_at"`
	DeletedAt              gorm.DeletedAt     `gorm:"index" json:"deleted_at,omitempty"`
}

// BeforeCreate hook to generate UUID before creating record
func (s *Subscription) BeforeCreate(tx *gorm.DB) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	return nil
}

// CreateSubscriptionRequest represents the request body for creating a subscription
type CreateSubscriptionRequest struct {
	UserID               uuid.UUID              `json:"user_id" binding:"required"`
	AppName              string                 `json:"app_name" binding:"required,min=1,max=100"`
	Phone                string                 `json:"phone" binding:"required,min=10,max=15"`
	Email                string                 `json:"email" binding:"required,email"`
	PlanID               string                 `json:"plan_id" binding:"required"`
	TotalCount           int                    `json:"total_count,omitempty"`
	StartAt              *int64                 `json:"start_at,omitempty"` // Unix timestamp
	Quantity             int                    `json:"quantity,omitempty"`
	Notes                map[string]interface{} `json:"notes,omitempty"`
	InitialChargeAmount  *int                   `json:"initial_charge_amount,omitempty"`   // Amount in rupees (will be converted to paise), default: 1
	FirstChargeDelayDays *int                   `json:"first_charge_delay_days,omitempty"` // Days to delay first subscription charge, default: 1
}

// SubscriptionResponse represents the response for subscription operations
type SubscriptionResponse struct {
	ID                     uuid.UUID          `json:"id"`
	UserID                 uuid.UUID          `json:"user_id"`
	AppName                string             `json:"app_name"`
	Phone                  string             `json:"phone"`
	Email                  string             `json:"email"`
	RazorpaySubscriptionID string             `json:"razorpay_subscription_id,omitempty"`
	RazorpayCustomerID     string             `json:"razorpay_customer_id,omitempty"`
	Status                 SubscriptionStatus `json:"status"`
	Amount                 int64              `json:"amount"`
	Currency               string             `json:"currency"`
	ShortURL               string             `json:"short_url,omitempty"`
	NextChargeAt           *time.Time         `json:"next_charge_at,omitempty"`
	CreatedAt              time.Time          `json:"created_at"`
	UpdatedAt              time.Time          `json:"updated_at"`
}

// ToResponse converts Subscription model to SubscriptionResponse
func (s *Subscription) ToResponse() SubscriptionResponse {
	return SubscriptionResponse{
		ID:                     s.ID,
		UserID:                 s.UserID,
		AppName:                s.AppName,
		Phone:                  s.Phone,
		Email:                  s.Email,
		RazorpaySubscriptionID: s.RazorpaySubscriptionID,
		RazorpayCustomerID:     s.RazorpayCustomerID,
		Status:                 s.Status,
		Amount:                 s.Amount,
		Currency:               s.Currency,
		ShortURL:               s.ShortURL,
		NextChargeAt:           s.NextChargeAt,
		CreatedAt:              s.CreatedAt,
		UpdatedAt:              s.UpdatedAt,
	}
}

// CheckoutURLResponse represents the response for checkout URL creation
type CheckoutURLResponse struct {
	SubscriptionID         uuid.UUID `json:"subscription_id"`
	RazorpaySubscriptionID string    `json:"razorpay_subscription_id"`
	ShortURL               string    `json:"short_url"`
	Status                 string    `json:"status"`
}

// VerifyPaymentRequest represents the request for payment verification
type VerifyPaymentRequest struct {
	RazorpaySubscriptionID string `json:"razorpay_subscription_id" binding:"required"`
	RazorpayPaymentID      string `json:"razorpay_payment_id" binding:"required"`
	RazorpaySignature      string `json:"razorpay_signature" binding:"required"`
}
