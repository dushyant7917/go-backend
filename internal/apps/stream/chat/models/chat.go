package models

import (
	"time"
)

// GenerateChatTokenRequest represents the request body for generating a Stream chat token
type GenerateChatTokenRequest struct {
	UserID *string `json:"user_id,omitempty"` // Optional: for tracking purposes
}

// ChatTokenResponse represents the response for Stream chat token generation
type ChatTokenResponse struct {
	UserID    string    `json:"user_id,omitempty"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}
