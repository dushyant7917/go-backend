package models

import (
	"time"

	"go-backend/pkg/utils"

	"github.com/google/uuid"
)

// DevotionalMusic represents a mantra/bhajan/aarti/chalisa audio track in the database
type DevotionalMusic struct {
	ID               uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Title            string         `gorm:"size:255;not null" json:"title"`
	Deity            string         `gorm:"size:255;not null" json:"deity"`
	Category         string         `gorm:"size:255;not null" json:"category"`
	AudioFileKey     string         `gorm:"type:varchar(512);not null;unique" json:"audio_file_key"`
	ThumbnailFileKey string         `gorm:"type:varchar(512);not null" json:"thumbnail_file_key"`
	Duration         float64        `gorm:"not null" json:"duration"`
	Metadata         utils.Metadata `gorm:"type:jsonb;not null;default:'{}'" json:"metadata,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// TableName overrides the default pluralized table name
func (DevotionalMusic) TableName() string {
	return "devotional_music"
}

// CreateDevotionalMusicRequest represents the request body for creating a devotional music track
type CreateDevotionalMusicRequest struct {
	Title            string         `json:"title" binding:"required"`
	Deity            string         `json:"deity" binding:"required"`
	Category         string         `json:"category" binding:"required,oneof=mantra bhajan aarti chalisa"`
	AudioFileKey     string         `json:"audio_file_key" binding:"required"`
	ThumbnailFileKey string         `json:"thumbnail_file_key" binding:"required"`
	Duration         float64        `json:"duration" binding:"required"`
	Metadata         utils.Metadata `json:"metadata,omitempty"`
}

// UpdateDevotionalMusicRequest represents the request body for updating a devotional music track
type UpdateDevotionalMusicRequest struct {
	Title            *string        `json:"title,omitempty"`
	Deity            *string        `json:"deity,omitempty"`
	Category         *string        `json:"category,omitempty" binding:"omitempty,oneof=mantra bhajan aarti chalisa"`
	AudioFileKey     *string        `json:"audio_file_key,omitempty"`
	ThumbnailFileKey *string        `json:"thumbnail_file_key,omitempty"`
	Duration         *float64       `json:"duration,omitempty"`
	Metadata         utils.Metadata `json:"metadata,omitempty"`
}

// DevotionalMusicResponse represents the response payload for devotional music operations
type DevotionalMusicResponse struct {
	ID           uuid.UUID      `json:"id"`
	Title        string         `json:"title"`
	Deity        string         `json:"deity"`
	Category     string         `json:"category"`
	AudioFileKey string         `json:"audio_file_key"`
	AudioURL     string         `json:"audio_url"`
	ThumbnailKey string         `json:"thumbnail_file_key"`
	ThumbnailURL string         `json:"thumbnail_url"`
	Duration     float64        `json:"duration"`
	Metadata     utils.Metadata `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// ToResponse converts a DevotionalMusic model to its response DTO, embedding
// public URLs constructed from the given public bucket URL base so mobile
// clients never need a separate view-url call.
func (m *DevotionalMusic) ToResponse(publicURLBase string) DevotionalMusicResponse {
	return DevotionalMusicResponse{
		ID:           m.ID,
		Title:        m.Title,
		Deity:        m.Deity,
		Category:     m.Category,
		AudioFileKey: m.AudioFileKey,
		AudioURL:     publicURLBase + "/" + m.AudioFileKey,
		ThumbnailKey: m.ThumbnailFileKey,
		ThumbnailURL: publicURLBase + "/" + m.ThumbnailFileKey,
		Duration:     m.Duration,
		Metadata:     m.Metadata,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}
