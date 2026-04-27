package models

import (
	"time"

	"go-backend/pkg/utils"

	"github.com/google/uuid"
)

// MetaEvent represents a pending Meta event to be emitted by the mobile SDK.
type MetaEvent struct {
	ID        uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	UserID    uuid.UUID      `gorm:"type:uuid;not null" json:"user_id"`
	AppName   string         `gorm:"not null;size:100" json:"app_name"`
	Name      string         `gorm:"not null;size:100" json:"name"`
	Triggered bool           `gorm:"not null;default:false" json:"triggered"`
	Metadata  utils.Metadata `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// UpdateMetaEventRequest represents the request body for updating a meta event.
type UpdateMetaEventRequest struct {
	Triggered *bool          `json:"triggered,omitempty"`
	Metadata  utils.Metadata `json:"metadata,omitempty"`
}

// MetaEventResponse represents a meta event in API responses.
type MetaEventResponse struct {
	ID        uuid.UUID      `json:"id"`
	Name      string         `json:"name"`
	Metadata  utils.Metadata `json:"metadata"`
	Triggered bool           `json:"triggered"`
	CreatedAt time.Time      `json:"created_at"`
}

// ToResponse converts a MetaEvent model to a MetaEventResponse.
func (e *MetaEvent) ToResponse() MetaEventResponse {
	return MetaEventResponse{
		ID:        e.ID,
		Name:      e.Name,
		Metadata:  e.Metadata,
		Triggered: e.Triggered,
		CreatedAt: e.CreatedAt,
	}
}
