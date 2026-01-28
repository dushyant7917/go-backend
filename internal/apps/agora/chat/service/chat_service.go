package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"time"

	"go-backend/internal/apps/agora/chat/models"
)

// ChatService defines the interface for chat business logic
type ChatService interface {
	GenerateChatToken(req models.GenerateChatTokenRequest) (*models.ChatTokenResponse, error)
}

// chatService implements ChatService interface
type chatService struct{}

// NewChatService creates a new instance of ChatService
func NewChatService() ChatService {
	return &chatService{}
}

// GenerateChatToken generates a new Agora chat token for a user
func (s *chatService) GenerateChatToken(req models.GenerateChatTokenRequest) (*models.ChatTokenResponse, error) {
	// Get Agora credentials from environment
	appID := os.Getenv("AGORA_APP_ID")
	appCertificate := os.Getenv("AGORA_APP_CERTIFICATE")

	if appID == "" || appCertificate == "" {
		return nil, errors.New("Agora credentials not configured")
	}

	// Use agora_uid if provided, otherwise use user_id
	agoraUID := req.UserID.String()
	if req.AgoraUID != nil && *req.AgoraUID != "" {
		agoraUID = *req.AgoraUID
	}

	// Get app_name if provided
	appName := ""
	if req.AppName != nil {
		appName = *req.AppName
	}

	// Token expiration time (24 hours from now)
	expirationTime := time.Now().Add(24 * time.Hour)
	expirationTimestamp := expirationTime.Unix()

	// Generate Agora chat token
	token, err := s.generateAgoraChatToken(appID, appCertificate, agoraUID, expirationTimestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	// Return token response without saving to database
	response := &models.ChatTokenResponse{
		UserID:    req.UserID,
		AppName:   appName,
		AgoraUID:  agoraUID,
		Token:     token,
		ExpiresAt: expirationTime,
		CreatedAt: time.Now(),
	}

	return response, nil
}

// generateAgoraChatToken generates an Agora chat token using HMAC-SHA256
// Based on Agora's token generation algorithm for Chat
func (s *chatService) generateAgoraChatToken(appID, appCertificate, userAccount string, expireTimestamp int64) (string, error) {
	// Agora Chat Token Format: version:appID:expireTimestamp:signature
	// version = "007" for current token version
	version := "007"

	// Create the message to sign
	message := fmt.Sprintf("%s%s%s%d", appID, appID, userAccount, expireTimestamp)

	// Generate HMAC-SHA256 signature
	h := hmac.New(sha256.New, []byte(appCertificate))
	h.Write([]byte(message))
	signature := hex.EncodeToString(h.Sum(nil))

	// Construct the token
	token := fmt.Sprintf("%s:%s:%s:%s:%d", version, appID, signature, userAccount, expireTimestamp)

	return token, nil
}
