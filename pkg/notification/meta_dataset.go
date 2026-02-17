package notification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	// MetaConversionsAPIURL is the Facebook Conversions API endpoint
	MetaConversionsAPIURL = "https://graph.facebook.com/v21.0"
)

// MetaDatasetClient handles sending events to Meta via Conversions API using dataset_id
type MetaDatasetClient struct {
	httpClient *http.Client
}

// NewMetaDatasetClient creates a new Meta dataset client
func NewMetaDatasetClient() *MetaDatasetClient {
	return &MetaDatasetClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SubscriptionEventData represents the data for a subscription event sent to Meta Conversions API
type SubscriptionEventData struct {
	DatasetID    string // Meta dataset_id (works for both web and mobile)
	AccessToken  string // Meta Access Token
	EventName    string // Event name (e.g., "Subscribe")
	EventTime    int64  // Unix timestamp
	UserData     UserData
	CustomData   CustomData
	EventID      string // Deduplication ID (optional but recommended)
	ActionSource string // Where the event occurred (e.g., "website", "app")
}

// UserData contains user information for Meta Conversions API
type UserData struct {
	Email           string `json:"em,omitempty"`          // Hashed email (SHA256)
	Phone           string `json:"ph,omitempty"`          // Hashed phone with country code (SHA256)
	ExternalID      string `json:"external_id,omitempty"` // User ID from your system
	ClientIPAddress string `json:"client_ip_address,omitempty"`
	ClientUserAgent string `json:"client_user_agent,omitempty"`
}

// CustomData contains purchase-specific information
type CustomData struct {
	Currency    string    `json:"currency"`
	Value       float64   `json:"value"`
	ContentName string    `json:"content_name,omitempty"` // Product name
	ContentType string    `json:"content_type,omitempty"` // e.g., "product"
	Contents    []Content `json:"contents,omitempty"`
}

// Content represents individual items in the purchase
type Content struct {
	ID       string  `json:"id"`
	Quantity int     `json:"quantity"`
	Price    float64 `json:"item_price,omitempty"`
}

// conversionAPIRequest represents the request payload for Conversions API
type conversionAPIRequest struct {
	Data []conversionAPIEvent `json:"data"`
}

// conversionAPIEvent represents a single event in Conversions API
type conversionAPIEvent struct {
	EventName    string     `json:"event_name"`
	EventTime    int64      `json:"event_time"`
	UserData     UserData   `json:"user_data"`
	CustomData   CustomData `json:"custom_data"`
	EventID      string     `json:"event_id,omitempty"`
	ActionSource string     `json:"action_source"`
}

// conversionAPIResponse represents the response from Conversions API
type conversionAPIResponse struct {
	EventsReceived int      `json:"events_received"`
	Messages       []string `json:"messages,omitempty"`
	FBTRACE_ID     string   `json:"fbtrace_id,omitempty"`
}

// SendSubscriptionEvent sends a subscription-related event to Meta via Conversions API
func (c *MetaDatasetClient) SendSubscriptionEvent(event SubscriptionEventData) error {
	if event.DatasetID == "" {
		return fmt.Errorf("dataset_id is required")
	}
	if event.AccessToken == "" {
		return fmt.Errorf("access_token is required")
	}
	if event.EventName == "" {
		event.EventName = "SubscriptionCharged"
	}
	if event.ActionSource == "" {
		event.ActionSource = "other"
	}

	// Construct API URL: /v21.0/{dataset_id}/events
	url := fmt.Sprintf("%s/%s/events?access_token=%s", MetaConversionsAPIURL, event.DatasetID, event.AccessToken)

	// Prepare request payload
	payload := conversionAPIRequest{
		Data: []conversionAPIEvent{
			{
				EventName:    event.EventName,
				EventTime:    event.EventTime,
				UserData:     event.UserData,
				CustomData:   event.CustomData,
				EventID:      event.EventID,
				ActionSource: event.ActionSource,
			},
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	fmt.Printf("[Meta Dataset] Sending %s event to dataset_id: %s\n", event.EventName, event.DatasetID)
	fmt.Printf("[Meta Dataset] Event: %s, Value: %.2f %s\n", event.EventName, event.CustomData.Value, event.CustomData.Currency)

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

	var apiResp conversionAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("meta conversions API returned status %d: %v", resp.StatusCode, apiResp.Messages)
	}

	fmt.Printf("[Meta Dataset] Successfully sent event. Events received: %d, FBTRACE_ID: %s\n",
		apiResp.EventsReceived, apiResp.FBTRACE_ID)

	return nil
}
