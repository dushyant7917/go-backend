package models

import (
	"time"

	"github.com/google/uuid"
	"go-backend/pkg/utils"
)

// NewsPoster represents a news poster in the database
type NewsPoster struct {
	ID                uuid.UUID      `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	NewsID            uuid.UUID      `json:"news_id" gorm:"type:uuid;not null"`
	UserID            uuid.UUID      `json:"user_id" gorm:"type:uuid;not null"`
	ProfilePictureKey *string        `json:"profile_picture_key,omitempty" gorm:"type:varchar(512)"`
	UserName          string         `json:"user_name" gorm:"type:varchar(255);not null"`
	UserDetail        *string        `json:"user_detail,omitempty" gorm:"type:varchar(500)"`
	UserStateID       string         `json:"user_state_id" gorm:"type:varchar(100);not null"`
	LanguageCode      string         `json:"language_code" gorm:"type:varchar(10);not null"`
	Metadata          utils.Metadata `json:"metadata" gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// TableName specifies the table name for NewsPoster
func (NewsPoster) TableName() string {
	return "news_posters"
}

// CreateNewsPosterRequest represents the request body for creating a news poster
type CreateNewsPosterRequest struct {
	NewsID            uuid.UUID       `json:"news_id" binding:"required"`
	UserID            uuid.UUID       `json:"user_id" binding:"required"`
	ProfilePictureKey *string         `json:"profile_picture_key,omitempty"`
	UserName          string          `json:"user_name" binding:"required"`
	UserDetail        *string         `json:"user_detail,omitempty"`
	UserStateID       string          `json:"user_state_id" binding:"required"`
	LanguageCode      string          `json:"language_code" binding:"required"`
	Metadata          *utils.Metadata `json:"metadata,omitempty"`
}

// NewsPosterResponse represents the response for a news poster
type NewsPosterResponse struct {
	ID                uuid.UUID      `json:"id"`
	NewsID            uuid.UUID      `json:"news_id"`
	UserID            uuid.UUID      `json:"user_id"`
	ProfilePictureKey *string        `json:"profile_picture_key,omitempty"`
	UserName          string         `json:"user_name"`
	UserDetail        *string        `json:"user_detail,omitempty"`
	UserStateID       string         `json:"user_state_id"`
	LanguageCode      string         `json:"language_code"`
	Metadata          utils.Metadata `json:"metadata"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}
