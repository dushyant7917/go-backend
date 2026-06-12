package models

import (
	"time"

	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"github.com/pgvector/pgvector-go"
)

// News represents a news article in the database
type News struct {
	ID           uuid.UUID        `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	Link         string           `json:"link" gorm:"type:varchar(2048);not null;unique"`
	ContentHash  *string          `json:"content_hash,omitempty" gorm:"type:varchar(64);uniqueIndex"`
	MediaFileKey *string          `json:"media_file_key,omitempty" gorm:"type:varchar(512)"`
	Category     string           `json:"category" gorm:"type:varchar(100);not null"`
	SubCategory  *string          `json:"sub_category,omitempty" gorm:"type:varchar(100)"`
	Status       string           `json:"status" gorm:"type:varchar(50);not null;default:'published'"`
	PublishedAt  *time.Time       `json:"published_at,omitempty"`
	Embedding    *pgvector.Vector `json:"-" gorm:"type:vector(768)"`
	ImagePrompt  *string          `json:"image_prompt,omitempty" gorm:"type:text"`
	Metadata     utils.Metadata   `json:"metadata" gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

// TableName specifies the table name for News
func (News) TableName() string {
	return "news"
}

// SimilarNews records an article that was dropped as a semantic duplicate of an existing News
// article. It lets later cron runs short-circuit the same article (by link or content_hash) before
// any translation/embedding call. NewsID points to the canonical (kept) article and cascade-deletes
// with it. Relationship is 1:N — one canonical News may have many SimilarNews rows.
type SimilarNews struct {
	ID              uuid.UUID `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	NewsID          uuid.UUID `json:"news_id" gorm:"type:uuid;not null"`
	Link            string    `json:"link" gorm:"type:varchar(2048);not null;unique"`
	ContentHash     *string   `json:"content_hash,omitempty" gorm:"type:varchar(64)"`
	Category        string    `json:"category" gorm:"type:varchar(100)"`
	SubCategory     *string   `json:"sub_category,omitempty" gorm:"type:varchar(100)"`
	SimilarityScore *float32  `json:"similarity_score,omitempty" gorm:"type:real"`
	SourceHost      string    `json:"source_host,omitempty" gorm:"type:varchar(255)"`
	CreatedAt       time.Time `json:"created_at"`
}

// TableName specifies the table name for SimilarNews
func (SimilarNews) TableName() string {
	return "similar_news"
}

// WrongCategoryNews records an article the LLM filter rejected (wrong category/state, astrology,
// recipe, ad) so later cron runs can short-circuit it before any translation call. It has no FK to
// News (a rejected item is not a duplicate of any stored article); rows are age-pruned by the
// cleanup_old_news cron.
type WrongCategoryNews struct {
	ID          uuid.UUID `json:"id" gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	Link        string    `json:"link" gorm:"type:varchar(2048);not null;unique"`
	ContentHash *string   `json:"content_hash,omitempty" gorm:"type:varchar(64)"`
	Category    string    `json:"category" gorm:"type:varchar(100)"`
	SkipReason  string    `json:"skip_reason,omitempty" gorm:"type:varchar(500)"`
	SourceHost  string    `json:"source_host,omitempty" gorm:"type:varchar(255)"`
	CreatedAt   time.Time `json:"created_at"`
}

// TableName specifies the table name for WrongCategoryNews
func (WrongCategoryNews) TableName() string {
	return "wrong_category_news"
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
	MediaFileKey *string        `json:"-"`                    // Internal field, not exposed in JSON
	MediaLink    *string        `json:"media_link,omitempty"` // Computed from MediaFileKey
	Category     string         `json:"category"`
	SubCategory  *string        `json:"sub_category,omitempty"`
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

// BulkUpdateNewsMediaFileKeyItem is one entry in a bulk media_file_key update
type BulkUpdateNewsMediaFileKeyItem struct {
	ID           string `json:"id" binding:"required,uuid"`
	MediaFileKey string `json:"media_file_key" binding:"required"`
}

// BulkUpdateNewsMediaFileKeyRequest is the request body for bulk updating media_file_key
type BulkUpdateNewsMediaFileKeyRequest struct {
	Items []BulkUpdateNewsMediaFileKeyItem `json:"items" binding:"required,min=1"`
}
