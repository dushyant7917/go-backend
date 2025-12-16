package service

import (
	"errors"

	"go-backend/internal/apps/razorpay/config/models"
	"go-backend/internal/apps/razorpay/config/repository"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RazorpayConfigService defines the interface for razorpay config business logic
type RazorpayConfigService interface {
	CreateRazorpayConfig(req models.CreateRazorpayConfigRequest) (*models.RazorpayConfigResponse, error)
	GetRazorpayConfigByID(id uuid.UUID) (*models.RazorpayConfigResponse, error)
	GetRazorpayConfigByAppNameAndEnv(appName string, environment string) (*models.RazorpayConfigResponse, error)
	GetAllRazorpayConfigs(page, pageSize int, activeOnly bool) (*models.PaginatedRazorpayConfigsResponse, error)
	UpdateRazorpayConfig(id uuid.UUID, req models.UpdateRazorpayConfigRequest) (*models.RazorpayConfigResponse, error)
	DeleteRazorpayConfig(id uuid.UUID) error
}

// razorpayConfigService implements RazorpayConfigService interface
type razorpayConfigService struct {
	repo repository.RazorpayConfigRepository
}

// NewRazorpayConfigService creates a new instance of RazorpayConfigService
func NewRazorpayConfigService(repo repository.RazorpayConfigRepository) RazorpayConfigService {
	return &razorpayConfigService{repo: repo}
}

// CreateRazorpayConfig creates a new razorpay config
func (s *razorpayConfigService) CreateRazorpayConfig(req models.CreateRazorpayConfigRequest) (*models.RazorpayConfigResponse, error) {
	// Check if app_name + environment combination already exists
	existingConfig, err := s.repo.FindByAppNameAndEnv(req.AppName, req.Environment)
	if err == nil && existingConfig != nil {
		return nil, errors.New("app_name and environment combination already exists")
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	metadata := models.Metadata{}
	if req.Metadata != nil {
		metadata = req.Metadata
	}

	config := &models.RazorpayConfig{
		AppName:               req.AppName,
		Environment:           req.Environment,
		RazorpayKeyID:         req.RazorpayKeyID,
		RazorpayKeySecret:     req.RazorpayKeySecret,
		RazorpayWebhookSecret: req.RazorpayWebhookSecret,
		IsActive:              isActive,
		Metadata:              metadata,
	}

	if err := s.repo.Create(config); err != nil {
		return nil, err
	}

	response := config.ToResponse()
	return &response, nil
}

// GetRazorpayConfigByID retrieves a razorpay config by ID
func (s *razorpayConfigService) GetRazorpayConfigByID(id uuid.UUID) (*models.RazorpayConfigResponse, error) {
	config, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("razorpay config not found")
		}
		return nil, err
	}

	response := config.ToResponse()
	return &response, nil
}

// GetRazorpayConfigByAppNameAndEnv retrieves a razorpay config by app name and environment
func (s *razorpayConfigService) GetRazorpayConfigByAppNameAndEnv(appName string, environment string) (*models.RazorpayConfigResponse, error) {
	config, err := s.repo.FindByAppNameAndEnv(appName, environment)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("razorpay config not found")
		}
		return nil, err
	}

	response := config.ToResponse()
	return &response, nil
}

// GetAllRazorpayConfigs retrieves all razorpay configs with pagination
func (s *razorpayConfigService) GetAllRazorpayConfigs(page, pageSize int, activeOnly bool) (*models.PaginatedRazorpayConfigsResponse, error) {
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
	configResponses := make([]models.RazorpayConfigResponse, len(configs))
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

	return &models.PaginatedRazorpayConfigsResponse{
		Data:       configResponses,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		NextPage:   nextPage,
		PrevPage:   prevPage,
	}, nil
}

// UpdateRazorpayConfig updates an existing razorpay config
func (s *razorpayConfigService) UpdateRazorpayConfig(id uuid.UUID, req models.UpdateRazorpayConfigRequest) (*models.RazorpayConfigResponse, error) {
	config, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("razorpay config not found")
		}
		return nil, err
	}

	// Update fields if provided
	if req.RazorpayKeyID != nil {
		config.RazorpayKeyID = *req.RazorpayKeyID
	}
	if req.RazorpayKeySecret != nil {
		config.RazorpayKeySecret = *req.RazorpayKeySecret
	}
	if req.RazorpayWebhookSecret != nil {
		config.RazorpayWebhookSecret = *req.RazorpayWebhookSecret
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

// DeleteRazorpayConfig soft deletes a razorpay config
func (s *razorpayConfigService) DeleteRazorpayConfig(id uuid.UUID) error {
	return s.repo.Delete(id)
}
