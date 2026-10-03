package models

import (
	"time"

	"go-backend/pkg/utils"

	"github.com/google/uuid"
)

// Tone represents a call ringtone / alarm tone audio track in the database.
// The same list of tones is used for both ringtones and alarm tones by the client.
type Tone struct {
	ID               uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Title            string         `gorm:"size:255;not null" json:"title"`
	Deity            string         `gorm:"size:255;not null" json:"deity"`
	AudioFileKey     string         `gorm:"type:varchar(512);not null;unique" json:"audio_file_key"`
	ThumbnailFileKey string         `gorm:"type:varchar(512);not null" json:"thumbnail_file_key"`
	Duration         float64        `gorm:"not null" json:"duration"`
	Metadata         utils.Metadata `gorm:"type:jsonb;not null;default:'{}'" json:"metadata,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// CreateToneRequest represents the request body for creating a tone
type CreateToneRequest struct {
	Title            string         `json:"title" binding:"required"`
	Deity            string         `json:"deity" binding:"required"`
	AudioFileKey     string         `json:"audio_file_key" binding:"required"`
	ThumbnailFileKey string         `json:"thumbnail_file_key" binding:"required"`
	Duration         float64        `json:"duration" binding:"required"`
	Metadata         utils.Metadata `json:"metadata,omitempty"`
}

// UpdateToneRequest represents the request body for updating a tone
type UpdateToneRequest struct {
	Title            *string        `json:"title,omitempty"`
	Deity            *string        `json:"deity,omitempty"`
	AudioFileKey     *string        `json:"audio_file_key,omitempty"`
	ThumbnailFileKey *string        `json:"thumbnail_file_key,omitempty"`
	Duration         *float64       `json:"duration,omitempty"`
	Metadata         utils.Metadata `json:"metadata,omitempty"`
}

// ToneResponse represents the response payload for tone operations
type ToneResponse struct {
	ID           uuid.UUID      `json:"id"`
	Title        string         `json:"title"`
	Deity        string         `json:"deity"`
	AudioFileKey string         `json:"audio_file_key"`
	AudioURL     string         `json:"audio_url"`
	ThumbnailKey string         `json:"thumbnail_file_key"`
	ThumbnailURL string         `json:"thumbnail_url"`
	Duration     float64        `json:"duration"`
	Metadata     utils.Metadata `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// ToResponse converts a Tone model to its response DTO, embedding public URLs
// constructed from the given public bucket URL base.
func (t *Tone) ToResponse(publicURLBase string) ToneResponse {
	return ToneResponse{
		ID:           t.ID,
		Title:        t.Title,
		Deity:        t.Deity,
		AudioFileKey: t.AudioFileKey,
		AudioURL:     publicURLBase + "/" + t.AudioFileKey,
		ThumbnailKey: t.ThumbnailFileKey,
		ThumbnailURL: publicURLBase + "/" + t.ThumbnailFileKey,
		Duration:     t.Duration,
		Metadata:     t.Metadata,
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
	}
}
