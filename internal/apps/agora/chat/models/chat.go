package models

import (
	"time"

	"github.com/google/uuid"
)

// GenerateChatTokenRequest represents the request body for generating a chat token
type GenerateChatTokenRequest struct {
	UserID   uuid.UUID `json:"user_id" binding:"required"`
	AppName  *string   `json:"app_name,omitempty"`  // Optional: for tracking purposes
	AgoraUID *string   `json:"agora_uid,omitempty"` // Optional: defaults to user_id if not provided
}

// ChatTokenResponse represents the response for chat token generation
type ChatTokenResponse struct {
	UserID    uuid.UUID `json:"user_id"`
	AppName   string    `json:"app_name,omitempty"`
	AgoraUID  string    `json:"agora_uid"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}
