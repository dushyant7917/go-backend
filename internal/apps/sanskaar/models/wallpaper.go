package models

import (
	"time"

	"go-backend/pkg/utils"

	"github.com/google/uuid"
)

// Wallpaper represents a static image or live (video) wallpaper in the database
type Wallpaper struct {
	ID               uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Deity            string         `gorm:"size:255;not null" json:"deity"`
	MediaFileKey     string         `gorm:"type:varchar(512);not null;unique" json:"media_file_key"`
	Type             string         `gorm:"size:20;not null" json:"type"`
	ThumbnailFileKey *string        `gorm:"type:varchar(512)" json:"thumbnail_file_key,omitempty"`
	Metadata         utils.Metadata `gorm:"type:jsonb;not null;default:'{}'" json:"metadata,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// CreateWallpaperRequest represents the request body for creating a wallpaper.
// ThumbnailFileKey is required when Type is "video" and must be omitted when Type is
// "image" — enforced in the service, not here, since it depends on the value of Type.
type CreateWallpaperRequest struct {
	Deity            string         `json:"deity" binding:"required"`
	MediaFileKey     string         `json:"media_file_key" binding:"required"`
	Type             string         `json:"type" binding:"required,oneof=image video"`
	ThumbnailFileKey *string        `json:"thumbnail_file_key,omitempty"`
	Metadata         utils.Metadata `json:"metadata,omitempty"`
}

// UpdateWallpaperRequest represents the request body for updating a wallpaper.
// If the update results in Type "image", ThumbnailFileKey is auto-cleared by the service.
type UpdateWallpaperRequest struct {
	Deity            *string        `json:"deity,omitempty"`
	MediaFileKey     *string        `json:"media_file_key,omitempty"`
	Type             *string        `json:"type,omitempty" binding:"omitempty,oneof=image video"`
	ThumbnailFileKey *string        `json:"thumbnail_file_key,omitempty"`
	Metadata         utils.Metadata `json:"metadata,omitempty"`
}

// WallpaperResponse represents the response payload for wallpaper operations
type WallpaperResponse struct {
	ID               uuid.UUID      `json:"id"`
	Deity            string         `json:"deity"`
	MediaFileKey     string         `json:"media_file_key"`
	MediaURL         string         `json:"media_url"`
	Type             string         `json:"type"`
	ThumbnailFileKey string         `json:"thumbnail_file_key,omitempty"`
	ThumbnailURL     string         `json:"thumbnail_url,omitempty"`
	Metadata         utils.Metadata `json:"metadata,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// ToResponse converts a Wallpaper model to its response DTO, embedding a
// public URL constructed from the given public bucket URL base.
func (w *Wallpaper) ToResponse(publicURLBase string) WallpaperResponse {
	resp := WallpaperResponse{
		ID:           w.ID,
		Deity:        w.Deity,
		MediaFileKey: w.MediaFileKey,
		MediaURL:     publicURLBase + "/" + w.MediaFileKey,
		Type:         w.Type,
		Metadata:     w.Metadata,
		CreatedAt:    w.CreatedAt,
		UpdatedAt:    w.UpdatedAt,
	}
	if w.ThumbnailFileKey != nil {
		resp.ThumbnailFileKey = *w.ThumbnailFileKey
		resp.ThumbnailURL = publicURLBase + "/" + *w.ThumbnailFileKey
	}
	return resp
}
