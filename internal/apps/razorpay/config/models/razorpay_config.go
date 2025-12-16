package models

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Metadata is a custom type for JSONB fields
type Metadata map[string]interface{}

// Scan implements the sql.Scanner interface for Metadata
func (m *Metadata) Scan(value interface{}) error {
	if value == nil {
		*m = make(Metadata)
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, m)
}

// Value implements the driver.Valuer interface for Metadata
func (m Metadata) Value() (driver.Value, error) {
	if m == nil {
		return json.Marshal(make(map[string]interface{}))
	}
	return json.Marshal(m)
}

// RazorpayConfig represents a Razorpay configuration for a specific app and environment
type RazorpayConfig struct {
	ID                    uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid();table:razorpay_configs" json:"id"`
	AppName               string         `gorm:"not null;size:100" json:"app_name"`
	Environment           string         `gorm:"not null;size:20;default:'test'" json:"environment"`
	RazorpayKeyID         string         `gorm:"not null;size:255" json:"razorpay_key_id"`
	RazorpayKeySecret     string         `gorm:"not null;size:255" json:"razorpay_key_secret"`
	RazorpayWebhookSecret string         `gorm:"not null;size:255" json:"razorpay_webhook_secret"`
	IsActive              bool           `gorm:"default:true" json:"is_active"`
	Metadata              Metadata       `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
	DeletedAt             gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

// TableName specifies the table name for RazorpayConfig
func (RazorpayConfig) TableName() string {
	return "razorpay_configs"
}

// BeforeCreate hook to generate UUID before creating record
func (c *RazorpayConfig) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return nil
}

// CreateRazorpayConfigRequest represents the request body for creating a razorpay config
type CreateRazorpayConfigRequest struct {
	AppName               string   `json:"app_name" binding:"required,min=1,max=100"`
	Environment           string   `json:"environment" binding:"required,oneof=test live"`
	RazorpayKeyID         string   `json:"razorpay_key_id" binding:"required"`
	RazorpayKeySecret     string   `json:"razorpay_key_secret" binding:"required"`
	RazorpayWebhookSecret string   `json:"razorpay_webhook_secret" binding:"required"`
	IsActive              *bool    `json:"is_active,omitempty"`
	Metadata              Metadata `json:"metadata,omitempty"`
}

// UpdateRazorpayConfigRequest represents the request body for updating a razorpay config
type UpdateRazorpayConfigRequest struct {
	RazorpayKeyID         *string  `json:"razorpay_key_id,omitempty"`
	RazorpayKeySecret     *string  `json:"razorpay_key_secret,omitempty"`
	RazorpayWebhookSecret *string  `json:"razorpay_webhook_secret,omitempty"`
	IsActive              *bool    `json:"is_active,omitempty"`
	Metadata              Metadata `json:"metadata,omitempty"`
}

// RazorpayConfigResponse represents the response payload for razorpay config operations
// Excludes sensitive credentials
type RazorpayConfigResponse struct {
	ID          uuid.UUID `json:"id"`
	AppName     string    `json:"app_name"`
	Environment string    `json:"environment"`
	IsActive    bool      `json:"is_active"`
	Metadata    Metadata  `json:"metadata"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ToResponse converts RazorpayConfig model to RazorpayConfigResponse (excludes sensitive data)
func (c *RazorpayConfig) ToResponse() RazorpayConfigResponse {
	return RazorpayConfigResponse{
		ID:          c.ID,
		AppName:     c.AppName,
		Environment: c.Environment,
		IsActive:    c.IsActive,
		Metadata:    c.Metadata,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

// PaginatedRazorpayConfigsResponse represents paginated razorpay configs response
type PaginatedRazorpayConfigsResponse struct {
	Data       []RazorpayConfigResponse `json:"data"`
	Page       int                      `json:"page"`
	PageSize   int                      `json:"page_size"`
	Total      int64                    `json:"total"`
	TotalPages int                      `json:"total_pages"`
	NextPage   *int                     `json:"next_page"`
	PrevPage   *int                     `json:"prev_page"`
}
