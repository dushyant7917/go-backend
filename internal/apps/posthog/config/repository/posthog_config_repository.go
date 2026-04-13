package repository

import (
	"go-backend/internal/apps/posthog/config/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PostHogConfigRepository defines the interface for PostHog config data operations
type PostHogConfigRepository interface {
	Create(config *models.PostHogConfig) error
	FindByID(id uuid.UUID) (*models.PostHogConfig, error)
	FindByAppNameAndEnv(appName string, environment string) (*models.PostHogConfig, error)
	FindAll(page, pageSize int, activeOnly bool) ([]models.PostHogConfig, int64, error)
	Update(config *models.PostHogConfig) error
	Delete(id uuid.UUID) error
}

// postHogConfigRepository implements PostHogConfigRepository interface
type postHogConfigRepository struct {
	db *gorm.DB
}

// NewPostHogConfigRepository creates a new instance of PostHogConfigRepository
func NewPostHogConfigRepository(db *gorm.DB) PostHogConfigRepository {
	return &postHogConfigRepository{db: db}
}

// Create creates a new PostHog config
func (r *postHogConfigRepository) Create(config *models.PostHogConfig) error {
	return r.db.Create(config).Error
}

// FindByID retrieves a PostHog config by its ID
func (r *postHogConfigRepository) FindByID(id uuid.UUID) (*models.PostHogConfig, error) {
	var config models.PostHogConfig
	if err := r.db.Where("id = ?", id).First(&config).Error; err != nil {
		return nil, err
	}
	return &config, nil
}

// FindByAppNameAndEnv finds a PostHog config by app name and environment
func (r *postHogConfigRepository) FindByAppNameAndEnv(appName string, environment string) (*models.PostHogConfig, error) {
	var config models.PostHogConfig
	if err := r.db.Where("app_name = ? AND environment = ? AND is_active = true", appName, environment).First(&config).Error; err != nil {
		return nil, err
	}
	return &config, nil
}

// FindAll retrieves all PostHog configs with pagination
func (r *postHogConfigRepository) FindAll(page, pageSize int, activeOnly bool) ([]models.PostHogConfig, int64, error) {
	var configs []models.PostHogConfig
	var total int64

	query := r.db.Model(&models.PostHogConfig{})

	if activeOnly {
		query = query.Where("is_active = true")
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&configs).Error; err != nil {
		return nil, 0, err
	}

	return configs, total, nil
}

// Update updates an existing PostHog config
func (r *postHogConfigRepository) Update(config *models.PostHogConfig) error {
	return r.db.Save(config).Error
}

// Delete soft deletes a PostHog config
func (r *postHogConfigRepository) Delete(id uuid.UUID) error {
	return r.db.Where("id = ?", id).Delete(&models.PostHogConfig{}).Error
}
