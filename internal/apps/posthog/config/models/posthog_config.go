package models

import (
	"time"

	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PostHogConfig represents a PostHog configuration for a specific app and environment
type PostHogConfig struct {
	ID          uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid();table:posthog_configs" json:"id"`
	AppName     string         `gorm:"not null;size:100;uniqueIndex:idx_posthog_app_env" json:"app_name"`
	Environment string         `gorm:"not null;size:20;default:'test';uniqueIndex:idx_posthog_app_env" json:"environment"`
	APIKey      string         `gorm:"not null;size:255" json:"api_key"`
	Host        string         `gorm:"not null;size:255" json:"host"`
	IsActive    bool           `gorm:"default:true" json:"is_active"`
	Metadata    utils.Metadata `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

// TableName specifies the table name for PostHogConfig
func (PostHogConfig) TableName() string {
	return "posthog_configs"
}

// BeforeCreate hook to generate UUID before creating record
func (c *PostHogConfig) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return nil
}

// CreatePostHogConfigRequest represents the request body for creating a PostHog config
type CreatePostHogConfigRequest struct {
	AppName     string         `json:"app_name" binding:"required,min=1,max=100"`
	Environment string         `json:"environment" binding:"required"`
	APIKey      string         `json:"api_key" binding:"required"`
	Host        string         `json:"host" binding:"required"`
	IsActive    *bool          `json:"is_active,omitempty"`
	Metadata    utils.Metadata `json:"metadata,omitempty"`
}

// UpdatePostHogConfigRequest represents the request body for updating a PostHog config
type UpdatePostHogConfigRequest struct {
	APIKey   *string        `json:"api_key,omitempty"`
	Host     *string        `json:"host,omitempty"`
	IsActive *bool          `json:"is_active,omitempty"`
	Metadata utils.Metadata `json:"metadata,omitempty"`
}

// PostHogConfigResponse represents the response payload for PostHog config operations
type PostHogConfigResponse struct {
	ID          uuid.UUID      `json:"id"`
	AppName     string         `json:"app_name"`
	Environment string         `json:"environment"`
	Host        string         `json:"host"`
	IsActive    bool           `json:"is_active"`
	Metadata    utils.Metadata `json:"metadata"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// ToResponse converts PostHogConfig model to PostHogConfigResponse (excludes sensitive API key)
func (c *PostHogConfig) ToResponse() PostHogConfigResponse {
	return PostHogConfigResponse{
		ID:          c.ID,
		AppName:     c.AppName,
		Environment: c.Environment,
		Host:        c.Host,
		IsActive:    c.IsActive,
		Metadata:    c.Metadata,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

// PaginatedPostHogConfigsResponse represents paginated PostHog configs response
type PaginatedPostHogConfigsResponse struct {
	Data       []PostHogConfigResponse `json:"data"`
	Page       int                     `json:"page"`
	PageSize   int                     `json:"page_size"`
	Total      int64                   `json:"total"`
	TotalPages int                     `json:"total_pages"`
	NextPage   *int                    `json:"next_page"`
	PrevPage   *int                    `json:"prev_page"`
}
