// Package handler is the HTTP layer of the autopay system: /api/v1/autopay/* and the admin config routes.
package handler

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"go-backend/internal/apps/autopay/models"
	"go-backend/internal/apps/autopay/service"
	commonResponse "go-backend/internal/common/response"
	"go-backend/pkg/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AutopayService is what the handler needs from service.Service (an interface so routing can be tested without a database).
type AutopayService interface {
	Create(ctx context.Context, req service.CreateRequest) (*service.CreateResult, error)
	Verify(ctx context.Context, id uuid.UUID) (*service.MandateView, error)
	Cancel(ctx context.Context, id uuid.UUID) (*service.MandateView, error)
	Get(id uuid.UUID) (*service.MandateView, error)
	Status(userID uuid.UUID, appName string) (*service.StatusView, error)
	AvailableGateways(appName string) ([]string, error)
	HandleWebhook(ctx context.Context, configID uuid.UUID, headers http.Header, rawBody []byte) error
}

// Handler serves the autopay API.
type Handler struct{ svc AutopayService }

// New returns a Handler.
func New(svc AutopayService) *Handler { return &Handler{svc: svc} }

// respondServiceError maps service errors to HTTP statuses.
func respondServiceError(c *gin.Context, op string, err error) {
	log.Printf("[%s] error: %v", op, err)
	switch {
	case errors.Is(err, service.ErrInvalidRequest):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, service.ErrGatewayNotAvailable):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "gateway_not_available"})
	case errors.Is(err, service.ErrGatewayUnavailable):
		commonResponse.Error(c, http.StatusServiceUnavailable, err, err.Error())
	case errors.Is(err, service.ErrInvalidWebhookSignature):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid webhook signature"})
	default:
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
	}
}

// createRequest is the body of POST /api/v1/autopay. `gateway` is sent once, here; later calls use the id.
type createRequest struct {
	UserID              uuid.UUID `json:"user_id" binding:"required"`
	AppName             string    `json:"app_name" binding:"required,min=1,max=100"`
	Gateway             string    `json:"gateway" binding:"required"`
	PaymentType         string    `json:"payment_type" binding:"required,oneof=recurring one_time"`
	Currency            string    `json:"currency"`
	AuthorizationAmount int64     `json:"authorization_amount"` // recurring, minor units
	MaxDebitAmount      int64     `json:"max_debit_amount"`     // recurring, minor units
	Amount              int64     `json:"amount"`               // one_time, minor units
	StartAt             time.Time `json:"start_at"`             // recurring only (required there, checked by the service); a one-time payment starts now
	BillingFrequency    string    `json:"billing_frequency" binding:"required,oneof=daily weekly fortnightly bimonthly monthly quarterly half_yearly yearly"`
	OneTimeKind         string    `json:"one_time_kind" binding:"omitempty,oneof=order payment_link"`
	ReturnURL           string    `json:"return_url"`
}

type createResponse struct {
	AutopayMandateID uuid.UUID      `json:"autopay_mandate_id"`
	Gateway          string         `json:"gateway"`
	PaymentType      string         `json:"payment_type"`
	Amount           int64          `json:"amount"`
	Currency         string         `json:"currency"`
	ClientPayload    map[string]any `json:"client_payload"`
	LinkURL          string         `json:"link_url,omitempty"`
	Mode             string         `json:"mode"` // sandbox | production
}

// Create handles POST /api/v1/autopay.
func (h *Handler) Create(c *gin.Context) {
	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[AutopayCreate] user_id=%s app_name=%s gateway=%s type=%s", req.UserID, req.AppName, req.Gateway, req.PaymentType)
	res, err := h.svc.Create(c.Request.Context(), service.CreateRequest{
		UserID: req.UserID, AppName: req.AppName, Gateway: req.Gateway, PaymentType: models.PaymentType(req.PaymentType), Currency: req.Currency,
		AuthorizationAmount: req.AuthorizationAmount, MaxDebitAmount: req.MaxDebitAmount, Amount: req.Amount, StartAt: req.StartAt.UTC(),
		BillingFrequency: req.BillingFrequency, OneTimeKind: req.OneTimeKind, ReturnURL: req.ReturnURL,
	})
	if err != nil {
		respondServiceError(c, "AutopayCreate", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": createResponse{
		AutopayMandateID: res.AutopayMandateID, Gateway: res.Gateway, PaymentType: string(res.PaymentType), Amount: res.Amount,
		Currency: res.Currency, ClientPayload: res.ClientPayload, LinkURL: res.LinkURL, Mode: res.Mode,
	}})
}

func parseID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("autopay_mandate_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid autopay_mandate_id"})
		return uuid.Nil, false
	}
	return id, true
}

// byID wraps the three endpoints that act on one autopay by its id: parse :autopay_mandate_id, run the action, answer.
func byID(op string, action func(ctx context.Context, id uuid.UUID) (*service.MandateView, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		view, err := action(c.Request.Context(), id)
		if err != nil {
			respondServiceError(c, op, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": view})
	}
}

// Verify handles POST /api/v1/autopay/:autopay_mandate_id/verify.
func (h *Handler) Verify(c *gin.Context) { byID("AutopayVerify", h.svc.Verify)(c) }

// Cancel handles POST /api/v1/autopay/:autopay_mandate_id/cancel.
func (h *Handler) Cancel(c *gin.Context) { byID("AutopayCancel", h.svc.Cancel)(c) }

// Get handles GET /api/v1/autopay/:autopay_mandate_id.
func (h *Handler) Get(c *gin.Context) {
	byID("AutopayGet", func(_ context.Context, id uuid.UUID) (*service.MandateView, error) { return h.svc.Get(id) })(c)
}

// Status handles GET /api/v1/autopay/status?user_id=&app_name=.
func (h *Handler) Status(c *gin.Context) {
	userID, err := uuid.Parse(c.Query("user_id"))
	appName := c.Query("app_name")
	if err != nil || appName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id (UUID) and app_name are required"})
		return
	}
	view, err := h.svc.Status(userID, appName)
	if err != nil {
		respondServiceError(c, "AutopayStatus", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view})
}

// Gateways handles GET /api/v1/autopay/gateways?app_name= : the gateways currently accepting new autopays for the app.
func (h *Handler) Gateways(c *gin.Context) {
	appName := c.Query("app_name")
	if appName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}
	gateways, err := h.svc.AvailableGateways(appName)
	if err != nil {
		respondServiceError(c, "AutopayGateways", err)
		return
	}
	if gateways == nil {
		gateways = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"gateways": gateways}})
}

// maxWebhookBodyBytes is far above any gateway webhook (a few KB) and far below anything abusive.
const maxWebhookBodyBytes = 1 << 20

// HandleWebhook handles POST /api/v1/autopay/webhooks/:config_id. The signature is checked over the raw body
// with that config's secret before any field is trusted.
func (h *Handler) HandleWebhook(c *gin.Context) {
	configID, err := uuid.Parse(c.Param("config_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown webhook endpoint"})
		return
	}
	// Unauthenticated until the signature verifies: cap what is read.
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxWebhookBodyBytes))
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "failed to read request body"})
		return
	}
	if err := h.svc.HandleWebhook(c.Request.Context(), configID, c.Request.Header, body); err != nil {
		respondServiceError(c, "AutopayWebhook", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "webhook accepted"})
}

// RegisterRoutes registers /autopay on the v1 group.
func RegisterRoutes(v1 *gin.RouterGroup, h *Handler) {
	g := v1.Group("/autopay")
	{
		g.POST("", h.Create)
		g.GET("/status", h.Status)
		g.GET("/gateways", h.Gateways)
		g.POST("/webhooks/:config_id", h.HandleWebhook)
		g.GET("/:autopay_mandate_id", h.Get)
		g.POST("/:autopay_mandate_id/verify", h.Verify)
		g.POST("/:autopay_mandate_id/cancel", h.Cancel)
	}
}

// ==================== Admin: gateway configs ====================

// ConfigService is what the config handler needs from service.ConfigService.
type ConfigService interface {
	Create(req service.CreateConfigRequest) (*models.PaymentGatewayConfig, error)
	Update(id uuid.UUID, req service.UpdateConfigRequest) (*models.PaymentGatewayConfig, error)
	List() ([]models.PaymentGatewayConfig, error)
}

// ConfigHandler serves /payment-gateway-configs.
type ConfigHandler struct{ svc ConfigService }

// NewConfigHandler returns a ConfigHandler.
func NewConfigHandler(svc ConfigService) *ConfigHandler { return &ConfigHandler{svc: svc} }

type createConfigRequest struct {
	Gateway             string            `json:"gateway" binding:"required"`
	AppName             string            `json:"app_name" binding:"required,min=1,max=100"`
	Environment         string            `json:"environment" binding:"required"`
	Credentials         map[string]string `json:"credentials" binding:"required"`
	WebhookSecret       string            `json:"webhook_secret"`
	GatewayRatelimitRPM *int              `json:"gateway_ratelimit_rpm"`
	Metadata            utils.Metadata    `json:"metadata"`
}

// Create handles POST /api/v1/payment-gateway-configs (registered before CORS in main.go).
func (h *ConfigHandler) Create(c *gin.Context) {
	var req createConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cfg, err := h.svc.Create(service.CreateConfigRequest{
		Gateway: req.Gateway, AppName: req.AppName, Environment: req.Environment, Credentials: req.Credentials,
		WebhookSecret: req.WebhookSecret, GatewayRatelimitRPM: req.GatewayRatelimitRPM, Metadata: req.Metadata,
	})
	if err != nil {
		respondServiceError(c, "PaymentGatewayConfigCreate", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": cfg})
}

// List handles GET /api/v1/payment-gateway-configs.
func (h *ConfigHandler) List(c *gin.Context) {
	cfgs, err := h.svc.List()
	if err != nil {
		respondServiceError(c, "PaymentGatewayConfigList", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": cfgs})
}

type updateConfigRequest struct {
	IsActive            *bool             `json:"is_active"`
	AcceptsNewAutopay   *bool             `json:"accepts_new_autopay"`
	GatewayRatelimitRPM *int              `json:"gateway_ratelimit_rpm"`
	Credentials         map[string]string `json:"credentials"`
	WebhookSecret       *string           `json:"webhook_secret"`
}

// Update handles PATCH /api/v1/payment-gateway-configs/:id (flags, limit, rotated credentials/secret).
func (h *ConfigHandler) Update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req updateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cfg, err := h.svc.Update(id, service.UpdateConfigRequest{
		IsActive: req.IsActive, AcceptsNewAutopay: req.AcceptsNewAutopay, GatewayRatelimitRPM: req.GatewayRatelimitRPM,
		Credentials: req.Credentials, WebhookSecret: req.WebhookSecret,
	})
	if err != nil {
		respondServiceError(c, "PaymentGatewayConfigUpdate", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": cfg})
}

// RegisterConfigRoutes registers the non-POST config routes on the v1 group; POST is registered in main.go before CORS.
func RegisterConfigRoutes(v1 *gin.RouterGroup, h *ConfigHandler) {
	g := v1.Group("/payment-gateway-configs")
	{
		g.GET("", h.List)
		g.PATCH("/:id", h.Update)
	}
}
