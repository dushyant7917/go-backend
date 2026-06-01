package models

import (
	"time"

	"go-backend/pkg/utils"

	"github.com/google/uuid"
)

// News represents a news article in the database
type News struct {
	ID           uuid.UUID      `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	Link         string         `json:"link" gorm:"type:varchar(2048);not null;unique"`
	ContentHash  *string        `json:"content_hash,omitempty" gorm:"type:varchar(64);uniqueIndex"`
	MediaFileKey *string        `json:"media_file_key,omitempty" gorm:"type:varchar(512)"`
	Category     string         `json:"category" gorm:"type:varchar(100);not null"`
	Status       string         `json:"status" gorm:"type:varchar(50);not null;default:'published'"`
	PublishedAt  *time.Time     `json:"published_at,omitempty"`
	Metadata     utils.Metadata `json:"metadata" gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// TableName specifies the table name for News
func (News) TableName() string {
	return "news"
}

// NewsTranslation represents a translation of a news article
type NewsTranslation struct {
	ID           uuid.UUID      `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	NewsID       uuid.UUID      `json:"news_id" gorm:"type:uuid;not null"`
	Title        string         `json:"title" gorm:"type:varchar(1000);not null"`
	Summary      string         `json:"summary" gorm:"type:varchar(1000);not null"`
	LanguageCode string         `json:"language_code" gorm:"type:varchar(10);not null"`
	Metadata     utils.Metadata `json:"metadata" gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// TableName specifies the table name for NewsTranslation
func (NewsTranslation) TableName() string {
	return "news_translations"
}

// NewsResponse represents a news article with its translated title
type NewsResponse struct {
	ID           uuid.UUID      `json:"id"`
	Link         string         `json:"link"`
	MediaFileKey *string        `json:"-"`                    // Internal field, not exposed in JSON
	MediaLink    *string        `json:"media_link,omitempty"` // Computed from MediaFileKey
	Category     string         `json:"category"`
	Status       string         `json:"status"`
	Title        string         `json:"title"`
	Summary      string         `json:"summary"`
	LanguageCode string         `json:"language_code"`
	PublishedAt  *time.Time     `json:"published_at,omitempty"`
	Metadata     utils.Metadata `json:"metadata"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// PaginatedNewsResponse represents paginated news response
type PaginatedNewsResponse struct {
	Data       []NewsResponse `json:"data"`
	Page       int            `json:"page"`
	PageSize   int            `json:"page_size"`
	Total      int64          `json:"total"`
	TotalPages int            `json:"total_pages"`
	NextPage   *int           `json:"next_page"`
	PrevPage   *int           `json:"prev_page"`
}

// UpdateNewsRequest represents the request body for updating a news article
type UpdateNewsRequest struct {
	Link         *string                `json:"link,omitempty"`
	MediaFileKey *string                `json:"media_file_key,omitempty"`
	Category     *string                `json:"category,omitempty"`
	Status       *string                `json:"status,omitempty"`
	PublishedAt  *time.Time             `json:"published_at,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}
