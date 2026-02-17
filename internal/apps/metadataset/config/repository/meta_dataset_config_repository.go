package repository

import (
	"go-backend/internal/apps/metadataset/config/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MetaDatasetConfigRepository defines the interface for meta dataset config data operations
type MetaDatasetConfigRepository interface {
	Create(config *models.MetaDatasetConfig) error
	FindByID(id uuid.UUID) (*models.MetaDatasetConfig, error)
	FindByAppNameAndEnv(appName string, environment string) (*models.MetaDatasetConfig, error)
	FindAll(page, pageSize int, activeOnly bool) ([]models.MetaDatasetConfig, int64, error)
	Update(config *models.MetaDatasetConfig) error
	Delete(id uuid.UUID) error
}

// metaDatasetConfigRepository implements MetaDatasetConfigRepository interface
type metaDatasetConfigRepository struct {
	db *gorm.DB
}

// NewMetaDatasetConfigRepository creates a new instance of MetaDatasetConfigRepository
func NewMetaDatasetConfigRepository(db *gorm.DB) MetaDatasetConfigRepository {
	return &metaDatasetConfigRepository{db: db}
}

// Create creates a new meta dataset config
func (r *metaDatasetConfigRepository) Create(config *models.MetaDatasetConfig) error {
	return r.db.Create(config).Error
}

// FindByID retrieves a meta dataset config by its ID
func (r *metaDatasetConfigRepository) FindByID(id uuid.UUID) (*models.MetaDatasetConfig, error) {
	var config models.MetaDatasetConfig
	if err := r.db.Where("id = ?", id).First(&config).Error; err != nil {
		return nil, err
	}
	return &config, nil
}

// FindByAppNameAndEnv finds a meta dataset config by app name and environment
func (r *metaDatasetConfigRepository) FindByAppNameAndEnv(appName string, environment string) (*models.MetaDatasetConfig, error) {
	var config models.MetaDatasetConfig
	if err := r.db.Where("app_name = ? AND environment = ? AND is_active = true", appName, environment).First(&config).Error; err != nil {
		return nil, err
	}
	return &config, nil
}

// FindAll retrieves all meta dataset configs with pagination
func (r *metaDatasetConfigRepository) FindAll(page, pageSize int, activeOnly bool) ([]models.MetaDatasetConfig, int64, error) {
	var configs []models.MetaDatasetConfig
	var total int64

	query := r.db.Model(&models.MetaDatasetConfig{})

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

// Update updates an existing meta dataset config
func (r *metaDatasetConfigRepository) Update(config *models.MetaDatasetConfig) error {
	return r.db.Save(config).Error
}

// Delete soft deletes a meta dataset config
func (r *metaDatasetConfigRepository) Delete(id uuid.UUID) error {
	return r.db.Where("id = ?", id).Delete(&models.MetaDatasetConfig{}).Error
}
