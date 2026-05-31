package notification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const whatsappGraphAPIVersion = "v25.0"

// WhatsAppClient sends messages via the Meta WhatsApp Business Cloud API.
type WhatsAppClient struct {
	accessToken   string
	phoneNumberID string
	httpClient    *http.Client
}

// NewWhatsAppClient creates a new client. accessToken is the Bearer token and
// phoneNumberID is the WhatsApp Business phone number ID (e.g. "1192724260583472").
func NewWhatsAppClient(accessToken, phoneNumberID string) *WhatsAppClient {
	return &WhatsAppClient{
		accessToken:   accessToken,
		phoneNumberID: phoneNumberID,
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}
}

// TemplateLanguage specifies the language for a template.
type TemplateLanguage struct {
	Code string `json:"code"`
}

// TemplateImage is the image payload for a header parameter.
type TemplateImage struct {
	Link string `json:"link"`
}

// TemplateParameter is one parameter within a template component.
// Set Type to "image" and populate Image, or Type to "text" and populate Text.
// ParameterName is optional and used for named body parameters.
type TemplateParameter struct {
	Type          string         `json:"type"`
	Text          string         `json:"text,omitempty"`
	ParameterName string         `json:"parameter_name,omitempty"`
	Image         *TemplateImage `json:"image,omitempty"`
}

// TemplateComponent represents a header, body, or button component.
// SubType and Index are only required for button components.
type TemplateComponent struct {
	Type       string              `json:"type"`
	SubType    string              `json:"sub_type,omitempty"`
	Index      string              `json:"index,omitempty"`
	Parameters []TemplateParameter `json:"parameters"`
}

// WhatsAppTemplate is the full template definition to send.
type WhatsAppTemplate struct {
	Name       string              `json:"name"`
	Language   TemplateLanguage    `json:"language"`
	Components []TemplateComponent `json:"components"`
}

type whatsappMessageRequest struct {
	MessagingProduct string           `json:"messaging_product"`
	To               string           `json:"to"`
	Type             string           `json:"type"`
	Template         WhatsAppTemplate `json:"template"`
}

type whatsappMessageResponse struct {
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
	Error *struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
		Type    string `json:"type"`
	} `json:"error"`
}

// SendTemplate sends a WhatsApp template message.
// to must be the full phone number with country code and no '+', e.g. "919876543210".
func (c *WhatsAppClient) SendTemplate(to string, tmpl WhatsAppTemplate) error {
	payload := whatsappMessageRequest{
		MessagingProduct: "whatsapp",
		To:               to,
		Type:             "template",
		Template:         tmpl,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("https://graph.facebook.com/%s/%s/messages", whatsappGraphAPIVersion, c.phoneNumberID)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	var result whatsappMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if result.Error != nil {
		return fmt.Errorf("WhatsApp API error (code %d, type %s): %s",
			result.Error.Code, result.Error.Type, result.Error.Message)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected HTTP status: %d", resp.StatusCode)
	}

	return nil
}
