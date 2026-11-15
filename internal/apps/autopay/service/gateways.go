package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/apps/autopay/models"
	"go-backend/internal/apps/autopay/repository"
	"go-backend/pkg/secure"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ResolvedGateway is a gateway bound to one merchant account, with the config row it came from.
type ResolvedGateway struct {
	Config            *models.PaymentGatewayConfig
	Gateway           gateway.Gateway
	RequestsPerMinute int // effective: the config's override, else the adapter's documented limit
}

// GatewayResolver turns config ids into ready-to-use gateways.
type GatewayResolver interface {
	// Resolve returns the gateway of a config (active or not: existing mandates keep being served).
	Resolve(ctx context.Context, configID uuid.UUID) (*ResolvedGateway, error)
	// ActiveConfigs lists the active configs the crons iterate over.
	ActiveConfigs(ctx context.Context) ([]models.PaymentGatewayConfig, error)
	// Registered reports whether an adapter exists for the gateway name.
	Registered(name gateway.Name) bool
}

// ConfigGatewayProvider is the production GatewayResolver: it decrypts the stored credentials, builds the adapter
// from the Registry and caches the result briefly so cron runs do not decrypt per attempt while credential rotation
// is still picked up within a few minutes.
type ConfigGatewayProvider struct {
	repo     repository.Repository
	registry *gateway.Registry
	ttl      time.Duration
	now      func() time.Time

	mu    sync.Mutex
	cache map[uuid.UUID]cachedGateway
}

type cachedGateway struct {
	resolved *ResolvedGateway
	expires  time.Time
}

// NewConfigGatewayProvider returns a provider with a 5-minute cache.
func NewConfigGatewayProvider(repo repository.Repository, registry *gateway.Registry) *ConfigGatewayProvider {
	return &ConfigGatewayProvider{
		repo: repo, registry: registry, ttl: 5 * time.Minute,
		now: func() time.Time { return time.Now().UTC() }, cache: make(map[uuid.UUID]cachedGateway),
	}
}

func (p *ConfigGatewayProvider) Registered(name gateway.Name) bool { return p.registry.Has(name) }

func (p *ConfigGatewayProvider) ActiveConfigs(ctx context.Context) ([]models.PaymentGatewayConfig, error) {
	return p.repo.ListActiveConfigs()
}

func (p *ConfigGatewayProvider) Resolve(ctx context.Context, configID uuid.UUID) (*ResolvedGateway, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cache[configID]; ok && p.now().Before(c.expires) {
		return c.resolved, nil
	}
	cfg, err := p.repo.FindConfigByID(configID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: gateway config %s", ErrNotFound, configID)
		}
		return nil, fmt.Errorf("load gateway config %s: %w", configID, err)
	}
	gcfg, err := DecryptConfig(cfg)
	if err != nil {
		return nil, err
	}
	gw, err := p.registry.New(gcfg)
	if err != nil {
		return nil, fmt.Errorf("build %s gateway for config %s: %w", cfg.Gateway, configID, err)
	}
	r := &ResolvedGateway{
		Config: cfg, Gateway: gw,
		RequestsPerMinute: gcfg.EffectiveRequestsPerMinuteLimit(gw.Capabilities().RequestsPerMinuteLimit),
	}
	p.cache[configID] = cachedGateway{resolved: r, expires: p.now().Add(p.ttl)}
	return r, nil
}

// DecryptConfig turns a stored config row into the decrypted gateway.Config adapters receive.
func DecryptConfig(cfg *models.PaymentGatewayConfig) (gateway.Config, error) {
	out := gateway.Config{
		ID: cfg.ID.String(), Gateway: gateway.Name(cfg.Gateway), AppName: cfg.AppName, Environment: cfg.Environment,
		Credentials: make(map[string]string, len(cfg.EncryptedCredentials)),
	}
	if cfg.GatewayRatelimitRPM != nil {
		out.RequestsPerMinuteLimit = *cfg.GatewayRatelimitRPM
	}
	for k, v := range cfg.EncryptedCredentials {
		enc, _ := v.(string)
		plain, err := secure.DecryptString(enc)
		if err != nil {
			return gateway.Config{}, fmt.Errorf("decrypt credential %q of config %s: %w", k, cfg.ID, err)
		}
		out.Credentials[k] = plain
	}
	if cfg.EncryptedWebhookSecret != "" {
		secret, err := secure.DecryptString(cfg.EncryptedWebhookSecret)
		if err != nil {
			return gateway.Config{}, fmt.Errorf("decrypt webhook secret of config %s: %w", cfg.ID, err)
		}
		out.WebhookSecret = secret
	}
	return out, nil
}

// EncryptCredentials encrypts every credential value for storage.
func EncryptCredentials(credentials map[string]string) (map[string]interface{}, error) {
	out := make(map[string]interface{}, len(credentials))
	for k, v := range credentials {
		enc, err := secure.EncryptString(v)
		if err != nil {
			return nil, fmt.Errorf("encrypt credential %q: %w", k, err)
		}
		out[k] = enc
	}
	return out, nil
}
