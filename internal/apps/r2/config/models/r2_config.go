package models

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Metadata is a custom type for JSONB fields
type R2Metadata map[string]interface{}

// Scan implements the sql.Scanner interface for R2Metadata
func (m *R2Metadata) Scan(value interface{}) error {
	if value == nil {
		*m = make(R2Metadata)
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, m)
}

// Value implements the driver.Valuer interface for R2Metadata
func (m R2Metadata) Value() (driver.Value, error) {
	if m == nil {
		return json.Marshal(make(map[string]interface{}))
	}
	return json.Marshal(m)
}

// R2Config represents an R2 configuration for a specific app and environment
type R2Config struct {
	ID              uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid();table:r2_configs" json:"id"`
	AppName         string         `gorm:"not null;size:100" json:"app_name"`
	Environment     string         `gorm:"not null;size:20;default:'test'" json:"environment"`
	AccountID       string         `gorm:"not null;size:255" json:"account_id"`
	AccessKeyID     string         `gorm:"not null;size:255" json:"access_key_id"`
	SecretAccessKey string         `gorm:"not null;size:255" json:"secret_access_key"`
	Metadata        R2Metadata     `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

// TableName specifies the table name for R2Config
func (R2Config) TableName() string {
	return "r2_configs"
}

// BeforeCreate hook to generate UUID before creating record
func (c *R2Config) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return nil
}

// CreateR2ConfigRequest represents the request body for creating an R2 config
type CreateR2ConfigRequest struct {
	AppName         string     `json:"app_name" binding:"required,min=1,max=100"`
	Environment     string     `json:"environment" binding:"required,oneof=test live"`
	AccountID       string     `json:"account_id" binding:"required"`
	AccessKeyID     string     `json:"access_key_id" binding:"required"`
	SecretAccessKey string     `json:"secret_access_key" binding:"required"`
	Metadata        R2Metadata `json:"metadata,omitempty"`
}

// UpdateR2ConfigRequest represents the request body for updating an R2 config
type UpdateR2ConfigRequest struct {
	AccountID       *string    `json:"account_id,omitempty"`
	AccessKeyID     *string    `json:"access_key_id,omitempty"`
	SecretAccessKey *string    `json:"secret_access_key,omitempty"`
	Metadata        R2Metadata `json:"metadata,omitempty"`
}

// R2ConfigResponse represents the response payload for R2 config operations
// Excludes sensitive credentials
type R2ConfigResponse struct {
	ID          uuid.UUID  `json:"id"`
	AppName     string     `json:"app_name"`
	Environment string     `json:"environment"`
	Metadata    R2Metadata `json:"metadata"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ToResponse converts R2Config model to R2ConfigResponse (excludes sensitive data)
func (c *R2Config) ToResponse() R2ConfigResponse {
	return R2ConfigResponse{
		ID:          c.ID,
		AppName:     c.AppName,
		Environment: c.Environment,
		Metadata:    c.Metadata,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

// PaginatedR2ConfigsResponse represents paginated R2 configs response
type PaginatedR2ConfigsResponse struct {
	Data       []R2ConfigResponse `json:"data"`
	Page       int                `json:"page"`
	PageSize   int                `json:"page_size"`
	Total      int64              `json:"total"`
	TotalPages int                `json:"total_pages"`
	NextPage   *int               `json:"next_page"`
	PrevPage   *int               `json:"prev_page"`
}
