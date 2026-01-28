package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"go-backend/internal/apps/stream/chat/models"

	"github.com/google/uuid"
)

// ChatService defines the interface for Stream chat business logic
type ChatService interface {
	GenerateChatToken(req models.GenerateChatTokenRequest) (*models.ChatTokenResponse, error)
}

// chatService implements ChatService interface
type chatService struct{}

// NewChatService creates a new instance of ChatService
func NewChatService() ChatService {
	return &chatService{}
}

// GenerateChatToken generates a new Stream chat token for a user
func (s *chatService) GenerateChatToken(req models.GenerateChatTokenRequest) (*models.ChatTokenResponse, error) {
	// Get Stream credentials from environment
	apiKey := os.Getenv("STREAM_API_KEY")
	apiSecret := os.Getenv("STREAM_API_SECRET")

	if apiKey == "" || apiSecret == "" {
		return nil, errors.New("Stream credentials not configured")
	}

	// Generate user ID if not provided
	userID := uuid.New().String()
	if req.UserID != nil && *req.UserID != "" {
		userID = *req.UserID
	}

	// Token expiration time (24 hours from now)
	expirationTime := time.Now().Add(24 * time.Hour)
	expirationTimestamp := expirationTime.Unix()

	// Generate Stream chat token
	token, err := s.generateStreamChatToken(apiSecret, userID, expirationTimestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	// Return token response without saving to database
	response := &models.ChatTokenResponse{
		UserID:    userID,
		Token:     token,
		ExpiresAt: expirationTime,
		CreatedAt: time.Now(),
	}

	return response, nil
}

// generateStreamChatToken generates a Stream chat token using HMAC-SHA256
// Based on Stream's token generation algorithm
func (s *chatService) generateStreamChatToken(apiSecret, userID string, expirationTimestamp int64) (string, error) {
	// Create the header
	header := map[string]interface{}{
		"alg": "HS256",
		"typ": "JWT",
	}

	// Create the payload
	payload := map[string]interface{}{
		"user_id": userID,
		"exp":     expirationTimestamp,
	}

	// Encode header
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)

	// Encode payload
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// Create the signature
	message := headerB64 + "." + payloadB64
	h := hmac.New(sha256.New, []byte(apiSecret))
	h.Write([]byte(message))
	signature := base64.RawURLEncoding.EncodeToString(h.Sum(nil))

	// Construct the token
	token := message + "." + signature

	return token, nil
}
