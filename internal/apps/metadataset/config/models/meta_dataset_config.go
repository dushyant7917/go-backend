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

// MetaDatasetConfig represents a Meta dataset configuration for a specific app and environment
type MetaDatasetConfig struct {
	ID          uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid();table:meta_datasets" json:"id"`
	AppName     string         `gorm:"not null;size:100" json:"app_name"`
	Environment string         `gorm:"not null;size:20;default:'test'" json:"environment"`
	DatasetID   string         `gorm:"not null;size:100" json:"dataset_id"`
	AccessToken string         `gorm:"not null;size:500" json:"access_token"`
	IsActive    bool           `gorm:"default:true" json:"is_active"`
	Metadata    Metadata       `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

// TableName specifies the table name for MetaDatasetConfig
func (MetaDatasetConfig) TableName() string {
	return "meta_datasets"
}

// BeforeCreate hook to generate UUID before creating record
func (c *MetaDatasetConfig) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return nil
}

// CreateMetaDatasetConfigRequest represents the request body for creating a meta dataset config
type CreateMetaDatasetConfigRequest struct {
	AppName     string   `json:"app_name" binding:"required,min=1,max=100"`
	Environment string   `json:"environment" binding:"required,oneof=test live"`
	DatasetID   string   `json:"dataset_id" binding:"required"`
	AccessToken string   `json:"access_token" binding:"required"`
	IsActive    *bool    `json:"is_active,omitempty"`
	Metadata    Metadata `json:"metadata,omitempty"`
}

// UpdateMetaDatasetConfigRequest represents the request body for updating a meta dataset config
type UpdateMetaDatasetConfigRequest struct {
	DatasetID   *string  `json:"dataset_id,omitempty"`
	AccessToken *string  `json:"access_token,omitempty"`
	IsActive    *bool    `json:"is_active,omitempty"`
	Metadata    Metadata `json:"metadata,omitempty"`
}

// MetaDatasetConfigResponse represents the response payload for meta dataset config operations
// Excludes sensitive access token
type MetaDatasetConfigResponse struct {
	ID          uuid.UUID `json:"id"`
	AppName     string    `json:"app_name"`
	Environment string    `json:"environment"`
	DatasetID   string    `json:"dataset_id"`
	IsActive    bool      `json:"is_active"`
	Metadata    Metadata  `json:"metadata"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ToResponse converts MetaDatasetConfig model to MetaDatasetConfigResponse (excludes sensitive data)
func (c *MetaDatasetConfig) ToResponse() MetaDatasetConfigResponse {
	return MetaDatasetConfigResponse{
		ID:          c.ID,
		AppName:     c.AppName,
		Environment: c.Environment,
		DatasetID:   c.DatasetID,
		IsActive:    c.IsActive,
		Metadata:    c.Metadata,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

// PaginatedMetaDatasetConfigsResponse represents paginated meta dataset configs response
type PaginatedMetaDatasetConfigsResponse struct {
	Data       []MetaDatasetConfigResponse `json:"data"`
	Page       int                         `json:"page"`
	PageSize   int                         `json:"page_size"`
	Total      int64                       `json:"total"`
	TotalPages int                         `json:"total_pages"`
	NextPage   *int                        `json:"next_page"`
	PrevPage   *int                        `json:"prev_page"`
}
