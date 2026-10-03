package models

import (
	"time"

	"go-backend/pkg/utils"

	"github.com/google/uuid"
)

// Status represents a static image or live (video) status update in the database
type Status struct {
	ID               uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Deity            string         `gorm:"size:255;not null" json:"deity"`
	MediaFileKey     string         `gorm:"type:varchar(512);not null;unique" json:"media_file_key"`
	Type             string         `gorm:"size:20;not null" json:"type"`
	Category         string         `gorm:"size:255;not null" json:"category"`
	AspectRatio      string         `gorm:"size:10;not null" json:"aspect_ratio"`
	ThumbnailFileKey *string        `gorm:"type:varchar(512)" json:"thumbnail_file_key,omitempty"`
	Metadata         utils.Metadata `gorm:"type:jsonb;not null;default:'{}'" json:"metadata,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// CreateStatusRequest represents the request body for creating a status.
// ThumbnailFileKey is required when Type is "video" and must be omitted when Type is
// "image" — enforced in the service, not here, since it depends on the value of Type.
type CreateStatusRequest struct {
	Deity            string         `json:"deity" binding:"required"`
	MediaFileKey     string         `json:"media_file_key" binding:"required"`
	Type             string         `json:"type" binding:"required,oneof=image video"`
	Category         string         `json:"category" binding:"required"`
	AspectRatio      string         `json:"aspect_ratio" binding:"required,oneof=4:5 9:16"`
	ThumbnailFileKey *string        `json:"thumbnail_file_key,omitempty"`
	Metadata         utils.Metadata `json:"metadata,omitempty"`
}

// UpdateStatusRequest represents the request body for updating a status.
// If the update results in Type "image", ThumbnailFileKey is auto-cleared by the service.
type UpdateStatusRequest struct {
	Deity            *string        `json:"deity,omitempty"`
	MediaFileKey     *string        `json:"media_file_key,omitempty"`
	Type             *string        `json:"type,omitempty" binding:"omitempty,oneof=image video"`
	Category         *string        `json:"category,omitempty"`
	AspectRatio      *string        `json:"aspect_ratio,omitempty" binding:"omitempty,oneof=4:5 9:16"`
	ThumbnailFileKey *string        `json:"thumbnail_file_key,omitempty"`
	Metadata         utils.Metadata `json:"metadata,omitempty"`
}

// StatusResponse represents the response payload for status operations
type StatusResponse struct {
	ID               uuid.UUID      `json:"id"`
	Deity            string         `json:"deity"`
	MediaFileKey     string         `json:"media_file_key"`
	MediaURL         string         `json:"media_url"`
	Type             string         `json:"type"`
	Category         string         `json:"category"`
	AspectRatio      string         `json:"aspect_ratio"`
	ThumbnailFileKey string         `json:"thumbnail_file_key,omitempty"`
	ThumbnailURL     string         `json:"thumbnail_url,omitempty"`
	Metadata         utils.Metadata `json:"metadata,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// ToResponse converts a Status model to its response DTO, embedding a
// public URL constructed from the given public bucket URL base.
func (s *Status) ToResponse(publicURLBase string) StatusResponse {
	resp := StatusResponse{
		ID:           s.ID,
		Deity:        s.Deity,
		MediaFileKey: s.MediaFileKey,
		MediaURL:     publicURLBase + "/" + s.MediaFileKey,
		Type:         s.Type,
		Category:     s.Category,
		AspectRatio:  s.AspectRatio,
		Metadata:     s.Metadata,
		CreatedAt:    s.CreatedAt,
		UpdatedAt:    s.UpdatedAt,
	}
	if s.ThumbnailFileKey != nil {
		resp.ThumbnailFileKey = *s.ThumbnailFileKey
		resp.ThumbnailURL = publicURLBase + "/" + *s.ThumbnailFileKey
	}
	return resp
}
