package notification

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	ExpoPushURL = "https://exp.host/--/api/v2/push/send"
)

// ExpoMessage represents a push notification message for Expo
type ExpoMessage struct {
	To       string                 `json:"to"`
	Title    string                 `json:"title,omitempty"`
	Body     string                 `json:"body,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`
	Sound    string                 `json:"sound,omitempty"`
	Badge    int                    `json:"badge,omitempty"`
	Priority string                 `json:"priority,omitempty"`
}

// ExpoResponse represents the response from Expo Push API
type ExpoResponse struct {
	Data []struct {
		Status  string `json:"status"`
		ID      string `json:"id,omitempty"`
		Message string `json:"message,omitempty"`
		Details struct {
			Error string `json:"error,omitempty"`
		} `json:"details,omitempty"`
	} `json:"data"`
}

// ExpoPushClient handles sending push notifications via Expo
type ExpoPushClient struct {
	httpClient *http.Client
}

// NewExpoPushClient creates a new Expo push notification client
func NewExpoPushClient() *ExpoPushClient {
	return &ExpoPushClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SendNotification sends a single push notification
func (c *ExpoPushClient) SendNotification(message ExpoMessage) error {
	messages := []ExpoMessage{message}
	results, err := c.SendBatch(messages)
	if err != nil {
		return err
	}
	if len(results) > 0 && results[0] != nil {
		return results[0]
	}
	return nil
}

// SendBatch sends multiple push notifications in a single request
func (c *ExpoPushClient) SendBatch(messages []ExpoMessage) ([]error, error) {
	if len(messages) == 0 {
		return nil, errors.New("no messages to send")
	}

	// Expo allows up to 100 messages per request
	const maxBatchSize = 100
	if len(messages) > maxBatchSize {
		return nil, fmt.Errorf("batch size exceeds maximum of %d messages", maxBatchSize)
	}

	payload, err := json.Marshal(messages)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal messages: %w", err)
	}

	req, err := http.NewRequest("POST", ExpoPushURL, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("expo API returned status code: %d", resp.StatusCode)
	}

	var expoResp ExpoResponse
	if err := json.NewDecoder(resp.Body).Decode(&expoResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Process results for each message
	results := make([]error, len(messages))
	for i, data := range expoResp.Data {
		if data.Status != "ok" {
			errorMsg := data.Message
			if data.Details.Error != "" {
				errorMsg = fmt.Sprintf("%s: %s", data.Message, data.Details.Error)
			}
			results[i] = errors.New(errorMsg)
		}
	}

	return results, nil
}

// ValidatePushToken checks if a token is in valid Expo format
func ValidatePushToken(token string) bool {
	if token == "" {
		return false
	}
	// Expo push tokens start with ExponentPushToken[ or ExpoPushToken[
	if len(token) > 18 {
		prefix := token[:18]
		return prefix == "ExponentPushToken[" || prefix[:13] == "ExpoPushToken"
	}
	return false
}
