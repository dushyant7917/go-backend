package service

import (
	"errors"
	"fmt"
	"strings"

	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/apps/autopay/models"
	"go-backend/internal/apps/autopay/repository"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ConfigService administers payment_gateway_configs: one row is one merchant account (gateway + app + environment).
type ConfigService struct {
	repo     repository.Repository
	registry *gateway.Registry
}

// NewConfigService returns a ConfigService.
func NewConfigService(repo repository.Repository, registry *gateway.Registry) *ConfigService {
	return &ConfigService{repo: repo, registry: registry}
}

// CreateConfigRequest creates a config. Credentials are gateway-specific (see the adapters) and are stored encrypted.
type CreateConfigRequest struct {
	Gateway             string
	AppName             string
	Environment         string
	Credentials         map[string]string
	WebhookSecret       string
	GatewayRatelimitRPM *int
	Metadata            utils.Metadata
}

// UpdateConfigRequest changes the flags, limit and secrets of a config. Nil fields are left as they are.
type UpdateConfigRequest struct {
	IsActive            *bool
	AcceptsNewAutopay   *bool
	GatewayRatelimitRPM *int
	Credentials         map[string]string
	WebhookSecret       *string
}

// Create validates and stores a config. The adapter factory is run once with the decrypted-equivalent credentials, so
// missing or malformed credentials are rejected here instead of failing at the first payment.
func (s *ConfigService) Create(req CreateConfigRequest) (*models.PaymentGatewayConfig, error) {
	switch {
	case req.AppName == "" || req.Environment == "":
		return nil, fmt.Errorf("%w: app_name and environment are required", ErrInvalidRequest)
	case !s.registry.Has(gateway.Name(req.Gateway)):
		return nil, fmt.Errorf("%w: unknown gateway %q (registered: %v)", ErrInvalidRequest, req.Gateway, s.registry.Names())
	case req.GatewayRatelimitRPM != nil && *req.GatewayRatelimitRPM <= 0:
		return nil, fmt.Errorf("%w: gateway_ratelimit_rpm must be positive", ErrInvalidRequest)
	}
	if err := s.validateCredentials(req.Gateway, req.AppName, req.Environment, req.Credentials, req.WebhookSecret); err != nil {
		return nil, err
	}
	encrypted, err := EncryptCredentials(req.Credentials)
	if err != nil {
		return nil, err
	}
	cfg := &models.PaymentGatewayConfig{
		Gateway: req.Gateway, AppName: req.AppName, Environment: req.Environment, EncryptedCredentials: encrypted,
		IsActive: true, AcceptsNewAutopay: true, GatewayRatelimitRPM: req.GatewayRatelimitRPM, Metadata: req.Metadata,
	}
	if req.WebhookSecret != "" {
		if cfg.EncryptedWebhookSecret, err = encryptOne(req.WebhookSecret); err != nil {
			return nil, err
		}
	}
	if err := s.repo.CreateConfig(cfg); err != nil {
		if strings.Contains(err.Error(), "uq_payment_gateway_configs_gateway_app_name_environment") {
			return nil, fmt.Errorf("%w: a %s config for app %q in environment %q already exists", ErrInvalidRequest, req.Gateway, req.AppName, req.Environment)
		}
		return nil, fmt.Errorf("create gateway config: %w", err)
	}
	return cfg, nil
}

// validateCredentials runs the adapter factory once, so missing or malformed credentials are rejected when they are
// entered or rotated instead of failing at the next payment.
func (s *ConfigService) validateCredentials(gw, appName, environment string, credentials map[string]string, webhookSecret string) error {
	if _, err := s.registry.New(gateway.Config{
		ID: uuid.NewString(), Gateway: gateway.Name(gw), AppName: appName, Environment: environment,
		Credentials: credentials, WebhookSecret: webhookSecret,
	}); err != nil {
		return fmt.Errorf("%w: credentials rejected by the %s adapter: %v", ErrInvalidRequest, gw, err)
	}
	return nil
}

// Update applies the flags and secrets of UpdateConfigRequest. Credentials are replaced as a whole (send every key).
func (s *ConfigService) Update(id uuid.UUID, req UpdateConfigRequest) (*models.PaymentGatewayConfig, error) {
	cfg, err := s.repo.FindConfigByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: gateway config %s", ErrNotFound, id)
		}
		return nil, err
	}
	if req.IsActive != nil {
		cfg.IsActive = *req.IsActive
	}
	if req.AcceptsNewAutopay != nil {
		cfg.AcceptsNewAutopay = *req.AcceptsNewAutopay
	}
	if req.GatewayRatelimitRPM != nil {
		if *req.GatewayRatelimitRPM <= 0 {
			return nil, fmt.Errorf("%w: gateway_ratelimit_rpm must be positive", ErrInvalidRequest)
		}
		cfg.GatewayRatelimitRPM = req.GatewayRatelimitRPM
	}
	if len(req.Credentials) > 0 {
		var webhookSecret string
		if req.WebhookSecret != nil {
			webhookSecret = *req.WebhookSecret
		}
		if err := s.validateCredentials(cfg.Gateway, cfg.AppName, cfg.Environment, req.Credentials, webhookSecret); err != nil {
			return nil, err
		}
		if cfg.EncryptedCredentials, err = EncryptCredentials(req.Credentials); err != nil {
			return nil, err
		}
	}
	if req.WebhookSecret != nil {
		if cfg.EncryptedWebhookSecret, err = encryptOne(*req.WebhookSecret); err != nil {
			return nil, err
		}
	}
	if err := s.repo.UpdateConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// List returns all configs (secrets are never serialized).
func (s *ConfigService) List() ([]models.PaymentGatewayConfig, error) { return s.repo.ListConfigs() }

func encryptOne(plain string) (string, error) {
	m, err := EncryptCredentials(map[string]string{"v": plain})
	if err != nil {
		return "", err
	}
	return m["v"].(string), nil
}

// AvailableGateways lists the gateways that currently accept new autopays for an app in this environment, so an app
// can pick or fall back without a release.
func (s *Service) AvailableGateways(appName string) ([]string, error) {
	configs, err := s.repo.ListActiveConfigs()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range configs {
		if c.AppName == appName && c.Environment == s.env && c.AcceptsNewAutopay && s.gateways.Registered(gateway.Name(c.Gateway)) {
			out = append(out, c.Gateway)
		}
	}
	return out, nil
}
