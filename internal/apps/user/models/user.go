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

// User represents an application user
type User struct {
	ID          uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()" json:"id"`
	Name        *string        `gorm:"size:255" json:"name,omitempty"`
	CountryCode *string        `gorm:"size:10" json:"country_code,omitempty"`
	Phone       *string        `gorm:"size:20" json:"phone,omitempty"`
	Email       *string        `gorm:"size:255" json:"email,omitempty"`
	AppName     string         `gorm:"not null;size:100" json:"app_name"`
	Metadata    Metadata       `gorm:"type:jsonb;not null;default:'{}';" json:"metadata"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"deleted_at,omitempty"`
}

// BeforeCreate hook to generate UUID before creating record
func (u *User) BeforeCreate(tx *gorm.DB) error {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return nil
}

// CreateUserRequest represents the request body for creating a user
type CreateUserRequest struct {
	Name        *string  `json:"name,omitempty"`
	CountryCode *string  `json:"country_code,omitempty"`
	Phone       *string  `json:"phone,omitempty"`
	Email       *string  `json:"email,omitempty" binding:"omitempty,email"`
	AppName     string   `json:"app_name" binding:"required,min=1,max=100"`
	Metadata    Metadata `json:"metadata,omitempty"`
}

// UpdateUserRequest represents the request body for updating a user
// At least one of (email) or (country_code + phone) must be present after update
type UpdateUserRequest struct {
	Name        *string  `json:"name,omitempty"`
	CountryCode *string  `json:"country_code,omitempty"`
	Phone       *string  `json:"phone,omitempty"`
	Email       *string  `json:"email,omitempty" binding:"omitempty,email"`
	AppName     *string  `json:"app_name,omitempty"`
	Metadata    Metadata `json:"metadata,omitempty"`
}

// UserResponse represents the response payload for user operations
type UserResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        *string   `json:"name,omitempty"`
	CountryCode *string   `json:"country_code,omitempty"`
	Phone       *string   `json:"phone,omitempty"`
	Email       *string   `json:"email,omitempty"`
	AppName     string    `json:"app_name"`
	Metadata    Metadata  `json:"metadata"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ToResponse converts User model to UserResponse
func (u *User) ToResponse() UserResponse {
	return UserResponse{
		ID:          u.ID,
		Name:        u.Name,
		CountryCode: u.CountryCode,
		Phone:       u.Phone,
		Email:       u.Email,
		AppName:     u.AppName,
		Metadata:    u.Metadata,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
}
