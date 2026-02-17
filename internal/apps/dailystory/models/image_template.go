package models

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// FaceConfig represents the face configuration in the template
type FaceConfig struct {
	CenterX float64 `json:"center_x"`
	CenterY float64 `json:"center_y"`
	Radius  float64 `json:"radius"`
}

// TextConfig represents the text configuration in the template
type TextConfig struct {
	TopLeftX float64 `json:"top_left_x"`
	TopLeftY float64 `json:"top_left_y"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
}

// TemplateConfig represents the config JSONB field structure
type TemplateConfig struct {
	Face   *FaceConfig `json:"face,omitempty"`
	Name   *TextConfig `json:"name,omitempty"`
	Phone  *TextConfig `json:"phone,omitempty"`
	Detail *TextConfig `json:"detail,omitempty"`
}

// Scan implements the sql.Scanner interface for TemplateConfig
func (c *TemplateConfig) Scan(value interface{}) error {
	if value == nil {
		*c = TemplateConfig{}
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, c)
}

// Value implements the driver.Valuer interface for TemplateConfig
func (c TemplateConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

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

// ImageTemplate represents an image template in the database
type ImageTemplate struct {
	ID          uuid.UUID       `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	FileKey     string          `gorm:"type:varchar(512);not null;unique" json:"file_key"`
	Category    string          `gorm:"size:255;not null" json:"category"`
	SubCategory string          `gorm:"size:255;not null" json:"sub_category"`
	Config      *TemplateConfig `gorm:"type:jsonb" json:"config,omitempty"`
	Metadata    Metadata        `gorm:"type:jsonb" json:"metadata,omitempty"`
	AuthorID    *uuid.UUID      `gorm:"type:uuid" json:"author_id,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// BeforeCreate hook to generate UUID before creating record
func (i *ImageTemplate) BeforeCreate(tx *gorm.DB) error {
	if i.ID == uuid.Nil {
		i.ID = uuid.New()
	}
	return nil
}

// CreateImageTemplateRequest represents the request body for creating an image template
type CreateImageTemplateRequest struct {
	FileKey     string          `json:"file_key" binding:"required"`
	Category    string          `json:"category" binding:"required"`
	SubCategory string          `json:"sub_category" binding:"required"`
	Config      *TemplateConfig `json:"config,omitempty"`
	Metadata    Metadata        `json:"metadata,omitempty"`
	AuthorID    *uuid.UUID      `json:"author_id,omitempty"`
}

// UpdateImageTemplateRequest represents the request body for updating an image template
type UpdateImageTemplateRequest struct {
	FileKey     *string         `json:"file_key,omitempty"`
	Category    *string         `json:"category,omitempty"`
	SubCategory *string         `json:"sub_category,omitempty"`
	Config      *TemplateConfig `json:"config,omitempty"`
	Metadata    Metadata        `json:"metadata,omitempty"`
	AuthorID    *uuid.UUID      `json:"author_id,omitempty"`
}

// ImageTemplateResponse represents the response payload for image template operations
type ImageTemplateResponse struct {
	ID          uuid.UUID       `json:"id"`
	FileKey     string          `json:"file_key"`
	Category    string          `json:"category"`
	SubCategory string          `json:"sub_category"`
	Config      *TemplateConfig `json:"config,omitempty"`
	Metadata    Metadata        `json:"metadata,omitempty"`
	AuthorID    *uuid.UUID      `json:"author_id,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// ToResponse converts ImageTemplate model to ImageTemplateResponse
func (i *ImageTemplate) ToResponse() ImageTemplateResponse {
	return ImageTemplateResponse{
		ID:          i.ID,
		FileKey:     i.FileKey,
		Category:    i.Category,
		SubCategory: i.SubCategory,
		Config:      i.Config,
		Metadata:    i.Metadata,
		AuthorID:    i.AuthorID,
		CreatedAt:   i.CreatedAt,
		UpdatedAt:   i.UpdatedAt,
	}
}

// PaginatedImageTemplatesResponse represents paginated image templates response
type PaginatedImageTemplatesResponse struct {
	Data       []ImageTemplateResponse `json:"data"`
	Page       int                     `json:"page"`
	PageSize   int                     `json:"page_size"`
	Total      int64                   `json:"total"`
	TotalPages int                     `json:"total_pages"`
	NextPage   *int                    `json:"next_page"`
	PrevPage   *int                    `json:"prev_page"`
}

// DesignerStatsResponse represents template statistics for a designer
type DesignerStatsResponse struct {
	UserID                    uuid.UUID `json:"user_id"`
	UserName                  *string   `json:"user_name,omitempty"`
	TemplatesCreatedToday     int64     `json:"templates_created_today"`
	TemplatesCreatedThisWeek  int64     `json:"templates_created_this_week"`
	TemplatesCreatedThisMonth int64     `json:"templates_created_this_month"`
	TemplatesCreatedTotal     int64     `json:"templates_created_total"`
	TemplatesPendingApproval  int64     `json:"templates_pending_approval"`
}

// TemplatePosterCountResponse represents count of posters for a template
type TemplatePosterCountResponse struct {
	TemplateID  uuid.UUID `json:"template_id"`
	FileKey     string    `json:"file_key"`
	Category    string    `json:"category"`
	SubCategory string    `json:"sub_category"`
	PosterCount int64     `json:"poster_count"`
	CreatedAt   time.Time `json:"created_at"`
}

// PaginatedTemplatePosterCountResponse represents paginated template poster count response
type PaginatedTemplatePosterCountResponse struct {
	Data       []TemplatePosterCountResponse `json:"data"`
	Page       int                           `json:"page"`
	PageSize   int                           `json:"page_size"`
	Total      int64                         `json:"total"`
	TotalPages int                           `json:"total_pages"`
	NextPage   *int                          `json:"next_page"`
	PrevPage   *int                          `json:"prev_page"`
}
