package service

import (
	"errors"

	"go-backend/internal/apps/posthog/config/models"
	"go-backend/internal/apps/posthog/config/repository"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PostHogConfigService defines the interface for PostHog config business logic
type PostHogConfigService interface {
	CreatePostHogConfig(req models.CreatePostHogConfigRequest) (*models.PostHogConfigResponse, error)
	GetPostHogConfigByID(id uuid.UUID) (*models.PostHogConfigResponse, error)
	GetPostHogConfigByAppNameAndEnv(appName string, environment string) (*models.PostHogConfigResponse, error)
	GetAllPostHogConfigs(page, pageSize int, activeOnly bool) (*models.PaginatedPostHogConfigsResponse, error)
	UpdatePostHogConfig(id uuid.UUID, req models.UpdatePostHogConfigRequest) (*models.PostHogConfigResponse, error)
	DeletePostHogConfig(id uuid.UUID) error
}

// postHogConfigService implements PostHogConfigService interface
type postHogConfigService struct {
	repo repository.PostHogConfigRepository
}

// NewPostHogConfigService creates a new instance of PostHogConfigService
func NewPostHogConfigService(repo repository.PostHogConfigRepository) PostHogConfigService {
	return &postHogConfigService{repo: repo}
}

// CreatePostHogConfig creates a new PostHog config
func (s *postHogConfigService) CreatePostHogConfig(req models.CreatePostHogConfigRequest) (*models.PostHogConfigResponse, error) {
	// Check if app_name + environment combination already exists
	existingConfig, err := s.repo.FindByAppNameAndEnv(req.AppName, req.Environment)
	if err == nil && existingConfig != nil {
		return nil, errors.New("app_name and environment combination already exists")
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	metadata := utils.Metadata{}
	if req.Metadata != nil {
		metadata = req.Metadata
	}

	config := &models.PostHogConfig{
		AppName:     req.AppName,
		Environment: req.Environment,
		APIKey:      req.APIKey,
		Host:        req.Host,
		IsActive:    isActive,
		Metadata:    metadata,
	}

	if err := s.repo.Create(config); err != nil {
		return nil, err
	}

	response := config.ToResponse()
	return &response, nil
}

// GetPostHogConfigByID retrieves a PostHog config by ID
func (s *postHogConfigService) GetPostHogConfigByID(id uuid.UUID) (*models.PostHogConfigResponse, error) {
	config, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("posthog config not found")
		}
		return nil, err
	}

	response := config.ToResponse()
	return &response, nil
}

// GetPostHogConfigByAppNameAndEnv retrieves a PostHog config by app name and environment
func (s *postHogConfigService) GetPostHogConfigByAppNameAndEnv(appName string, environment string) (*models.PostHogConfigResponse, error) {
	config, err := s.repo.FindByAppNameAndEnv(appName, environment)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("posthog config not found")
		}
		return nil, err
	}

	response := config.ToResponse()
	return &response, nil
}

// GetAllPostHogConfigs retrieves all PostHog configs with pagination
func (s *postHogConfigService) GetAllPostHogConfigs(page, pageSize int, activeOnly bool) (*models.PaginatedPostHogConfigsResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}

	configs, total, err := s.repo.FindAll(page, pageSize, activeOnly)
	if err != nil {
		return nil, err
	}

	// Convert to response format
	configResponses := make([]models.PostHogConfigResponse, len(configs))
	for i, config := range configs {
		configResponses[i] = config.ToResponse()
	}

	// Calculate pagination metadata
	totalPages := int(total) / pageSize
	if int(total)%pageSize != 0 {
		totalPages++
	}

	var nextPage *int
	var prevPage *int

	if page < totalPages {
		next := page + 1
		nextPage = &next
	}

	if page > 1 {
		prev := page - 1
		prevPage = &prev
	}

	return &models.PaginatedPostHogConfigsResponse{
		Data:       configResponses,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		NextPage:   nextPage,
		PrevPage:   prevPage,
	}, nil
}

// UpdatePostHogConfig updates an existing PostHog config
func (s *postHogConfigService) UpdatePostHogConfig(id uuid.UUID, req models.UpdatePostHogConfigRequest) (*models.PostHogConfigResponse, error) {
	config, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("posthog config not found")
		}
		return nil, err
	}

	// Update fields if provided
	if req.APIKey != nil {
		config.APIKey = *req.APIKey
	}
	if req.Host != nil {
		config.Host = *req.Host
	}
	if req.IsActive != nil {
		config.IsActive = *req.IsActive
	}
	if req.Metadata != nil {
		config.Metadata = req.Metadata
	}

	if err := s.repo.Update(config); err != nil {
		return nil, err
	}

	response := config.ToResponse()
	return &response, nil
}

// DeletePostHogConfig soft deletes a PostHog config
func (s *postHogConfigService) DeletePostHogConfig(id uuid.UUID) error {
	return s.repo.Delete(id)
}
