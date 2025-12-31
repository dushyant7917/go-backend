package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ImagePoster represents an image poster in the database
type ImagePoster struct {
	ID                    uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	UserID                uuid.UUID `gorm:"type:uuid;not null" json:"user_id"`
	TemplateID            uuid.UUID `gorm:"type:uuid;not null" json:"template_id"`
	NameUsed              string    `gorm:"type:varchar(255);not null" json:"name_used"`
	ProfilePictureKeyUsed string    `gorm:"type:varchar(512);not null" json:"profile_picture_key_used"`
	FileKey               string    `gorm:"type:varchar(512);not null" json:"file_key"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// BeforeCreate hook to generate UUID before creating record
func (i *ImagePoster) BeforeCreate(tx *gorm.DB) error {
	if i.ID == uuid.Nil {
		i.ID = uuid.New()
	}
	return nil
}

// GeneratePosterRequest represents the request body for generating a poster
type GeneratePosterRequest struct {
	TemplateID uuid.UUID `json:"template_id" binding:"required"`
	UserID     uuid.UUID `json:"user_id" binding:"required"`
}

// GeneratePosterResponse represents the response for generating a poster
type GeneratePosterResponse struct {
	PosterURL string `json:"poster_url"`
	Cached    bool   `json:"cached"`
}
