package analytics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// PostHogClient handles sending events to PostHog
type PostHogClient struct {
	httpClient *http.Client
}

// NewPostHogClient creates a new PostHog client
func NewPostHogClient() *PostHogClient {
	return &PostHogClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// EventData represents an event to be sent to PostHog
type EventData struct {
	APIKey     string                 `json:"api_key"`
	Event      string                 `json:"event"`
	DistinctID string                 `json:"distinct_id"`
	Properties map[string]interface{} `json:"properties"`
	Timestamp  string                 `json:"timestamp,omitempty"`
}

// batchEventRequest represents a batch event request to PostHog
type batchEventRequest struct {
	APIKey              string      `json:"api_key"`
	HistoricalMigration bool        `json:"historical_migration"`
	Events              []eventItem `json:"batch"`
}

// eventItem represents a single event in a batch request
type eventItem struct {
	Event      string                 `json:"event"`
	Properties map[string]interface{} `json:"properties"`
	DistinctID string                 `json:"distinct_id"`
	Timestamp  string                 `json:"timestamp"`
}

// SendEvent sends a single event to PostHog
func (c *PostHogClient) SendEvent(host, apiKey, eventName string, distinctID string, properties map[string]interface{}) error {
	if apiKey == "" {
		return fmt.Errorf("api_key is required")
	}
	if host == "" {
		return fmt.Errorf("host is required")
	}
	if eventName == "" {
		return fmt.Errorf("event name is required")
	}

	url := fmt.Sprintf("%s/batch/", host)

	// Add timestamp if not present
	timestamp := time.Now().UTC().Format(time.RFC3339)

	payload := batchEventRequest{
		APIKey:              apiKey,
		HistoricalMigration: false,
		Events: []eventItem{
			{
				Event:      eventName,
				DistinctID: distinctID,
				Properties: properties,
				Timestamp:  timestamp,
			},
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("posthog API returned status %d", resp.StatusCode)
	}

	fmt.Printf("[PostHog] Successfully sent event: %s for distinct_id: %s\n", eventName, distinctID)
	return nil
}

// RecurringPaymentEventProperties contains common properties for recurring payment events
type RecurringPaymentEventProperties struct {
	UserID       uuid.UUID
	AppName      string
	Amount       float64 // Amount in INR (not paise)
	StateID      *string
	LanguageCode *string
	ErrorCode    *string
}

// ToProperties converts RecurringPaymentEventProperties to a map
func (p RecurringPaymentEventProperties) ToProperties() map[string]interface{} {
	props := map[string]interface{}{
		"user_id":  p.UserID.String(),
		"app_name": p.AppName,
		"amount":   p.Amount,
	}

	if p.StateID != nil {
		props["state_id"] = *p.StateID
	}
	if p.LanguageCode != nil {
		props["language_code"] = *p.LanguageCode
	}
	if p.ErrorCode != nil {
		props["error_code"] = *p.ErrorCode
	}

	return props
}
