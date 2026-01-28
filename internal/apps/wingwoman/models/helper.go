package models

import (
	"time"

	"github.com/google/uuid"
)

// HelperResponse represents a helper user in the WingWoman app
type HelperResponse struct {
	ID          uuid.UUID              `json:"id"`
	Name        *string                `json:"name,omitempty"`
	CountryCode *string                `json:"country_code,omitempty"`
	Phone       *string                `json:"phone,omitempty"`
	Email       *string                `json:"email,omitempty"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// PaginatedHelpersResponse represents paginated helpers response
type PaginatedHelpersResponse struct {
	Data       []HelperResponse `json:"data"`
	Page       int              `json:"page"`
	PageSize   int              `json:"page_size"`
	Total      int64            `json:"total"`
	TotalPages int              `json:"total_pages"`
	NextPage   *int             `json:"next_page"`
	PrevPage   *int             `json:"prev_page"`
}
