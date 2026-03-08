package service

import (
	"fmt"
	"sync"

	"go-backend/internal/apps/r2/config/repository"
	"go-backend/pkg/storage"
	"go-backend/pkg/utils"
)

// R2ClientFactory creates and caches R2 clients per app
type R2ClientFactory struct {
	configRepo  repository.R2ConfigRepository
	clientCache map[string]*storage.R2Client // Cache R2 clients by app_name:environment
	cacheMutex  sync.RWMutex                 // Protect concurrent access to cache
}

// NewR2ClientFactory creates a new instance of R2ClientFactory
func NewR2ClientFactory(configRepo repository.R2ConfigRepository) *R2ClientFactory {
	return &R2ClientFactory{
		configRepo:  configRepo,
		clientCache: make(map[string]*storage.R2Client),
	}
}

// GetClient returns an R2 client for the given app name
// It fetches credentials from the database and caches the client
func (f *R2ClientFactory) GetClient(appName string) (*storage.R2Client, error) {
	// Use app_name + environment as cache key
	env := utils.GetRazorpayEnvironment() // Reuse the same environment logic (test/live)
	cacheKey := appName + ":" + env

	// Try to get from cache with read lock
	f.cacheMutex.RLock()
	cachedClient, exists := f.clientCache[cacheKey]
	f.cacheMutex.RUnlock()

	if exists {
		return cachedClient, nil
	}

	// Create new client with write lock
	f.cacheMutex.Lock()
	defer f.cacheMutex.Unlock()

	// Double-check after acquiring write lock
	if cachedClient, exists := f.clientCache[cacheKey]; exists {
		return cachedClient, nil
	}

	// Fetch config from database
	config, err := f.configRepo.FindByAppNameAndEnv(appName, env)
	if err != nil {
		return nil, fmt.Errorf("failed to find R2 config for app %s: %w", appName, err)
	}

	// Create new R2 client with config credentials
	r2Client, err := storage.NewR2Client(storage.R2Config{
		AccountID:       config.AccountID,
		AccessKeyID:     config.AccessKeyID,
		SecretAccessKey: config.SecretAccessKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create R2 client for app %s: %w", appName, err)
	}

	// Cache the client
	f.clientCache[cacheKey] = r2Client
	fmt.Printf("[R2ClientFactory] Created and cached new R2 client for app: %s\n", appName)

	return r2Client, nil
}

// InvalidateClient removes a cached client (useful when config is updated)
func (f *R2ClientFactory) InvalidateClient(appName string) {
	env := utils.GetRazorpayEnvironment()
	cacheKey := appName + ":" + env

	f.cacheMutex.Lock()
	defer f.cacheMutex.Unlock()

	delete(f.clientCache, cacheKey)
	fmt.Printf("[R2ClientFactory] Invalidated R2 client cache for app: %s\n", appName)
}
