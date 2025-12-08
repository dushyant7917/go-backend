package models

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

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

// Crush represents a crush entry in Crush Connect app
type Crush struct {
	ID          uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	UserID      uuid.UUID      `gorm:"type:uuid;not null" json:"user_id"`
	Name        string         `gorm:"not null;size:255" json:"name"`
	CountryCode *string        `gorm:"size:10" json:"country_code,omitempty"`
	Phone       *string        `gorm:"size:20" json:"phone,omitempty"`
	InstagramID *string        `gorm:"size:255" json:"instagram_id,omitempty"`
	SnapchatID  *string        `gorm:"size:255" json:"snapchat_id,omitempty"`
	Metadata    Metadata       `gorm:"type:jsonb;not null;default:'{}'" json:"metadata"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

// BeforeCreate hook to generate UUID before creating record
func (c *Crush) BeforeCreate(tx *gorm.DB) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	return nil
}

// CreateCrushRequest represents the request body for creating a crush
type CreateCrushRequest struct {
	UserID      uuid.UUID `json:"user_id" binding:"required"`
	Name        string    `json:"name" binding:"required,min=1,max=255"`
	CountryCode *string   `json:"country_code,omitempty"`
	Phone       *string   `json:"phone,omitempty"`
	InstagramID *string   `json:"instagram_id,omitempty"`
	SnapchatID  *string   `json:"snapchat_id,omitempty"`
	Metadata    Metadata  `json:"metadata,omitempty"`
}

// UpdateCrushRequest represents the request body for updating a crush
type UpdateCrushRequest struct {
	Name        *string  `json:"name,omitempty" binding:"omitempty,min=1,max=255"`
	CountryCode *string  `json:"country_code,omitempty"`
	Phone       *string  `json:"phone,omitempty"`
	InstagramID *string  `json:"instagram_id,omitempty"`
	SnapchatID  *string  `json:"snapchat_id,omitempty"`
	Metadata    Metadata `json:"metadata,omitempty"`
}

// CrushResponse represents the response payload for crush operations
type CrushResponse struct {
	ID          uuid.UUID `json:"id"`
	UserID      uuid.UUID `json:"user_id"`
	Name        string    `json:"name"`
	CountryCode *string   `json:"country_code,omitempty"`
	Phone       *string   `json:"phone,omitempty"`
	InstagramID *string   `json:"instagram_id,omitempty"`
	SnapchatID  *string   `json:"snapchat_id,omitempty"`
	Metadata    Metadata  `json:"metadata"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ToResponse converts Crush model to CrushResponse
func (c *Crush) ToResponse() CrushResponse {
	return CrushResponse{
		ID:          c.ID,
		UserID:      c.UserID,
		Name:        c.Name,
		CountryCode: c.CountryCode,
		Phone:       c.Phone,
		InstagramID: c.InstagramID,
		SnapchatID:  c.SnapchatID,
		Metadata:    c.Metadata,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

// CrushOnUserResponse represents a minimal response for crushes on a user
type CrushOnUserResponse struct {
	CreatedAt time.Time `json:"created_at"`
}

// ToMinimalResponse converts Crush model to CrushOnUserResponse
func (c *Crush) ToMinimalResponse() CrushOnUserResponse {
	return CrushOnUserResponse{
		CreatedAt: c.CreatedAt,
	}
}

// AllCrushesResponse represents the admin view of all crushes
type AllCrushesResponse struct {
	UserCountryCode  *string   `json:"user_country_code,omitempty"`
	UserPhone        *string   `json:"user_phone,omitempty"`
	CrushCountryCode *string   `json:"crush_country_code,omitempty"`
	CrushPhone       *string   `json:"crush_phone,omitempty"`
	InstagramID      *string   `json:"instagram_id,omitempty"`
	SnapchatID       *string   `json:"snapchat_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// PaginatedCrushesResponse represents paginated crushes response
type PaginatedCrushesResponse struct {
	Data       []AllCrushesResponse `json:"data"`
	Page       int                  `json:"page"`
	PageSize   int                  `json:"page_size"`
	Total      int64                `json:"total"`
	TotalPages int                  `json:"total_pages"`
	NextPage   *int                 `json:"next_page"`
	PrevPage   *int                 `json:"prev_page"`
}
