package service

import (
	"errors"

	"go-backend/internal/apps/r2/config/models"
	"go-backend/internal/apps/r2/config/repository"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// R2ConfigService defines the interface for R2 config business logic
type R2ConfigService interface {
	CreateConfig(req models.CreateR2ConfigRequest) (*models.R2ConfigResponse, error)
	GetConfigByID(id uuid.UUID) (*models.R2ConfigResponse, error)
	GetConfigByAppNameAndEnv(appName string, environment string) (*models.R2Config, error)
	GetAllConfigs(page, pageSize int) (*models.PaginatedR2ConfigsResponse, error)
	UpdateConfig(id uuid.UUID, req models.UpdateR2ConfigRequest) (*models.R2ConfigResponse, error)
	DeleteConfig(id uuid.UUID) error
}

// r2ConfigService implements R2ConfigService interface
type r2ConfigService struct {
	repo repository.R2ConfigRepository
}

// NewR2ConfigService creates a new instance of R2ConfigService
func NewR2ConfigService(repo repository.R2ConfigRepository) R2ConfigService {
	return &r2ConfigService{repo: repo}
}

// CreateConfig creates a new R2 config
func (s *r2ConfigService) CreateConfig(req models.CreateR2ConfigRequest) (*models.R2ConfigResponse, error) {
	config := &models.R2Config{
		AppName:         req.AppName,
		Environment:     req.Environment,
		AccountID:       req.AccountID,
		AccessKeyID:     req.AccessKeyID,
		SecretAccessKey: req.SecretAccessKey,
		Metadata:        req.Metadata,
	}

	if err := s.repo.Create(config); err != nil {
		return nil, err
	}

	response := config.ToResponse()
	return &response, nil
}

// GetConfigByID retrieves an R2 config by ID
func (s *r2ConfigService) GetConfigByID(id uuid.UUID) (*models.R2ConfigResponse, error) {
	config, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("r2 config not found")
		}
		return nil, err
	}

	response := config.ToResponse()
	return &response, nil
}

// GetConfigByAppNameAndEnv retrieves an R2 config by app name and environment
// Returns the full config including sensitive credentials for internal use
func (s *r2ConfigService) GetConfigByAppNameAndEnv(appName string, environment string) (*models.R2Config, error) {
	config, err := s.repo.FindByAppNameAndEnv(appName, environment)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("r2 config not found")
		}
		return nil, err
	}

	return config, nil
}

// GetAllConfigs retrieves all R2 configs with pagination
func (s *r2ConfigService) GetAllConfigs(page, pageSize int) (*models.PaginatedR2ConfigsResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	configs, total, err := s.repo.FindAll(page, pageSize)
	if err != nil {
		return nil, err
	}

	// Convert to response format (excludes sensitive data)
	responses := make([]models.R2ConfigResponse, len(configs))
	for i, config := range configs {
		responses[i] = config.ToResponse()
	}

	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
	var nextPage, prevPage *int

	if page < totalPages {
		next := page + 1
		nextPage = &next
	}
	if page > 1 {
		prev := page - 1
		prevPage = &prev
	}

	return &models.PaginatedR2ConfigsResponse{
		Data:       responses,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		NextPage:   nextPage,
		PrevPage:   prevPage,
	}, nil
}

// UpdateConfig updates an existing R2 config
func (s *r2ConfigService) UpdateConfig(id uuid.UUID, req models.UpdateR2ConfigRequest) (*models.R2ConfigResponse, error) {
	config, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("r2 config not found")
		}
		return nil, err
	}

	// Apply updates if provided
	if req.AccountID != nil {
		config.AccountID = *req.AccountID
	}
	if req.AccessKeyID != nil {
		config.AccessKeyID = *req.AccessKeyID
	}
	if req.SecretAccessKey != nil {
		config.SecretAccessKey = *req.SecretAccessKey
	}
	if req.Metadata != nil && len(req.Metadata) > 0 {
		if config.Metadata == nil {
			config.Metadata = make(models.R2Metadata)
		}
		for key, value := range req.Metadata {
			config.Metadata[key] = value
		}
	}

	if err := s.repo.Update(config); err != nil {
		return nil, err
	}

	response := config.ToResponse()
	return &response, nil
}

// DeleteConfig deletes an R2 config
func (s *r2ConfigService) DeleteConfig(id uuid.UUID) error {
	return s.repo.Delete(id)
}
