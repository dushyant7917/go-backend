package notification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go-backend/pkg/utils"
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

// MetaEventData represents the data for an event sent to Meta Conversions API
// Used by both subscription and recurring payment services
type MetaEventData struct {
	DatasetID    string // Meta dataset_id (works for both web and mobile)
	AccessToken  string // Meta Access Token
	EventName    string // Event name (e.g., "Subscribe")
	EventTime    int64  // Unix timestamp
	UserData     UserData
	CustomData   CustomData
	AppData      AppData // App-specific data (sent when action_source=app)
	EventID      string  // Deduplication ID (optional but recommended)
	ActionSource string  // Where the event occurred (e.g., "website", "app")
}

// UserData contains user information for Meta Conversions API
type UserData struct {
	Email           string `json:"em,omitempty"`          // Hashed email (SHA256)
	Phone           string `json:"ph,omitempty"`          // Hashed phone with country code (SHA256)
	ExternalID      string `json:"external_id,omitempty"` // User ID from your system
	ClientIPAddress string `json:"client_ip_address,omitempty"`
	ClientUserAgent string `json:"client_user_agent,omitempty"`
}

// AppData contains app-specific information for Meta Conversions API events.
// Meta expects string values "True" or "False" for the tracking fields.
// extinfo is a device/app metadata array in "a2" format (16 fields).
type AppData struct {
	AdvertiserTrackingEnabled  string   `json:"advertiser_tracking_enabled,omitempty"`  // "True" or "False"
	ApplicationTrackingEnabled string   `json:"application_tracking_enabled,omitempty"` // "True" or "False"
	ExtInfo                    []string `json:"extinfo,omitempty"`                      // Device/app metadata array
}

// DefaultAppData builds the fallback AppData with hardcoded device values
// used when user metadata does not contain app_data.
func DefaultAppData() AppData {
	return AppData{
		AdvertiserTrackingEnabled:  "False",
		ApplicationTrackingEnabled: "False",
		ExtInfo:                    serverSideExtInfo(),
	}
}

// AppDataFromUserMetadata extracts app_data from user metadata.
// If metadata contains a valid "app_data" key, it is parsed into AppData.
// Otherwise, the default hardcoded AppData is returned.
func AppDataFromUserMetadata(metadata utils.Metadata) AppData {
	if metadata == nil {
		return DefaultAppData()
	}

	appDataRaw, ok := metadata["app_data"]
	if !ok || appDataRaw == nil {
		return DefaultAppData()
	}

	// Marshal the app_data value back to JSON, then unmarshal into AppData struct
	jsonBytes, err := json.Marshal(appDataRaw)
	if err != nil {
		return DefaultAppData()
	}

	var appData AppData
	if err := json.Unmarshal(jsonBytes, &appData); err != nil {
		return DefaultAppData()
	}

	return appData
}

// serverSideExtInfo builds the extinfo array for server-side events.
// Meta requires "a2" format with exactly 16 fields.
// Server-side has no device SDK, so representative Android values are used.
func serverSideExtInfo() []string {
	return []string{
		"a2",             // [0]  extinfo version
		"package.name",   // [1]  app package name / bundle ID
		"24",             // [2]  app build number
		"Version 1.0.23", // [3]  app version string
		"14",             // [4]  OS version (Android 14)
		"Redmi Note 15",  // [5]  device model
		"en_IN",          // [6]  locale
		"IST",            // [7]  timezone abbreviation
		"Jio",            // [8]  carrier
		"1080",           // [9]  screen width
		"2400",           // [10] screen height
		"2.75",           // [11] screen density
		"8",              // [12] CPU cores
		"128",            // [13] total storage GB
		"8",              // [14] free storage GB
		"Asia/Kolkata",   // [15] device timezone
	}
}

// MetaEventParams contains the parameters needed to build a Meta CAPI event.
// This is used by both subscription and recurring payment services to avoid
// duplicating the MetaEventData construction logic.
type MetaEventParams struct {
	DatasetID    string   // Meta dataset_id from config
	AccessToken  string   // Meta access token from config
	EventName    string   // Event name (e.g., "SubscriptionCharged", "RecurringPaymentStarted")
	ActionSource string   // Where the event occurred: "app" or "website"
	Phone        string   // Raw phone number (will be hashed)
	UserID       string   // External user ID
	Currency     string   // ISO 4217 currency code (e.g., "INR")
	Value        float64  // Monetary value in decimal (e.g., 99.00)
	ContentName  string   // Display name for the product/event
	ContentID    string   // ID for the content item (plan ID, recurring payment ID, etc.)
	DedupSource  string   // Source ID for event deduplication (payment ID, subscription ID, etc.)
	AppData      *AppData // Optional: if nil and ActionSource is "app", default AppData is used
}

// NewAppEventData constructs a MetaEventData for a CAPI event
// with all common fields pre-filled. If ActionSource is "app", app_data
// is included: if params.AppData is provided it is used directly,
// otherwise a default AppData with hardcoded device values is generated.
// The caller provides parameters via MetaEventParams.
func NewAppEventData(params MetaEventParams) MetaEventData {
	eventTime := time.Now().Unix()

	event := MetaEventData{
		DatasetID:    params.DatasetID,
		AccessToken:  params.AccessToken,
		EventName:    params.EventName,
		EventTime:    eventTime,
		ActionSource: params.ActionSource,
		UserData: UserData{
			Phone:      HashPhone(params.Phone),
			ExternalID: params.UserID,
		},
		CustomData: CustomData{
			Currency:    params.Currency,
			Value:       params.Value,
			ContentName: params.ContentName,
			ContentType: "product",
			Contents: []Content{
				{
					ID:       params.ContentID,
					Quantity: 1,
					Price:    params.Value,
				},
			},
		},
		EventID: GenerateEventID(params.DedupSource, eventTime),
	}

	if params.ActionSource == "app" {
		if params.AppData != nil {
			event.AppData = *params.AppData
		} else {
			event.AppData = DefaultAppData()
		}
	}

	return event
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
	AppData      AppData    `json:"app_data,omitempty"`
	EventID      string     `json:"event_id,omitempty"`
	ActionSource string     `json:"action_source"`
}

// conversionAPIResponse represents the response from Conversions API
type conversionAPIResponse struct {
	EventsReceived int                 `json:"events_received"`
	Messages       []string            `json:"messages,omitempty"`
	FBTRACE_ID     string              `json:"fbtrace_id,omitempty"`
	Error          *conversionAPIError `json:"error,omitempty"`
}

// conversionAPIError represents the error object in Meta API response
type conversionAPIError struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Code      int    `json:"code"`
	FBTraceID string `json:"fbtrace_id"`
}

// SendEvent sends an event to Meta via Conversions API
func (c *MetaDatasetClient) SendEvent(event MetaEventData) error {
	if event.DatasetID == "" {
		return fmt.Errorf("dataset_id is required")
	}
	if event.AccessToken == "" {
		return fmt.Errorf("access_token is required")
	}
	if event.EventName == "" {
		return fmt.Errorf("event_name is required")
	}

	if event.ActionSource == "" {
		event.ActionSource = "website"
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
				AppData:      event.AppData,
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
	fmt.Printf("[Meta Dataset] Event: %s, Value: %.2f %s, ActionSource: %s\n",
		event.EventName, event.CustomData.Value, event.CustomData.Currency, event.ActionSource)

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
		errMsg := fmt.Sprintf("status %d", resp.StatusCode)
		if len(apiResp.Messages) > 0 {
			errMsg = fmt.Sprintf("%s, messages: %v", errMsg, apiResp.Messages)
		}
		if apiResp.Error != nil {
			errMsg = fmt.Sprintf("%s, error: [%d] %s: %s (fbtrace_id: %s)",
				errMsg, apiResp.Error.Code, apiResp.Error.Type, apiResp.Error.Message, apiResp.Error.FBTraceID)
		}
		fmt.Printf("[Meta Dataset] Full response body for failed request: events_received=%d\n", apiResp.EventsReceived)
		return fmt.Errorf("meta conversions API returned %s", errMsg)
	}

	fmt.Printf("[Meta Dataset] Successfully sent event. Events received: %d, FBTRACE_ID: %s\n",
		apiResp.EventsReceived, apiResp.FBTRACE_ID)

	return nil
}
