package repository

import (
	"errors"

	"go-backend/internal/apps/r2/config/models"
	"go-backend/pkg/secure"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// R2ConfigRepository defines the interface for R2 config data operations
type R2ConfigRepository interface {
	Create(config *models.R2Config) error
	FindByID(id uuid.UUID) (*models.R2Config, error)
	FindByAppNameAndEnv(appName string, environment string) (*models.R2Config, error)
	FindAll(page, pageSize int) ([]models.R2Config, int64, error)
	Update(config *models.R2Config) error
	Delete(id uuid.UUID) error
}

// r2ConfigRepository implements R2ConfigRepository interface
type r2ConfigRepository struct {
	db *gorm.DB
}

// NewR2ConfigRepository creates a new instance of R2ConfigRepository
func NewR2ConfigRepository(db *gorm.DB) R2ConfigRepository {
	return &r2ConfigRepository{db: db}
}

// Create creates a new R2 config
func (r *r2ConfigRepository) Create(config *models.R2Config) error {
	// Encrypt sensitive fields before saving
	var err error
	config.AccountID, err = secure.EncryptString(config.AccountID)
	if err != nil {
		return err
	}
	config.AccessKeyID, err = secure.EncryptString(config.AccessKeyID)
	if err != nil {
		return err
	}
	config.SecretAccessKey, err = secure.EncryptString(config.SecretAccessKey)
	if err != nil {
		return err
	}

	return r.db.Create(config).Error
}

// FindByID finds an R2 config by ID
func (r *r2ConfigRepository) FindByID(id uuid.UUID) (*models.R2Config, error) {
	var config models.R2Config
	if err := r.db.Where("id = ?", id).First(&config).Error; err != nil {
		return nil, err
	}

	// Decrypt sensitive fields before returning
	var err error
	config.AccountID, err = secure.DecryptString(config.AccountID)
	if err != nil {
		return nil, err
	}
	config.AccessKeyID, err = secure.DecryptString(config.AccessKeyID)
	if err != nil {
		return nil, err
	}
	config.SecretAccessKey, err = secure.DecryptString(config.SecretAccessKey)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// FindByAppNameAndEnv finds an R2 config by app name and environment
func (r *r2ConfigRepository) FindByAppNameAndEnv(appName string, environment string) (*models.R2Config, error) {
	var config models.R2Config
	if err := r.db.Where("app_name = ? AND environment = ?", appName, environment).First(&config).Error; err != nil {
		return nil, err
	}

	// Decrypt sensitive fields before returning
	var err error
	config.AccountID, err = secure.DecryptString(config.AccountID)
	if err != nil {
		return nil, err
	}
	config.AccessKeyID, err = secure.DecryptString(config.AccessKeyID)
	if err != nil {
		return nil, err
	}
	config.SecretAccessKey, err = secure.DecryptString(config.SecretAccessKey)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// FindAll retrieves all R2 configs with pagination
func (r *r2ConfigRepository) FindAll(page, pageSize int) ([]models.R2Config, int64, error) {
	var configs []models.R2Config
	var total int64

	query := r.db.Model(&models.R2Config{})

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := query.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&configs).Error; err != nil {
		return nil, 0, err
	}

	// Decrypt sensitive fields for each config
	for i := range configs {
		var err error
		configs[i].AccountID, err = secure.DecryptString(configs[i].AccountID)
		if err != nil {
			return nil, 0, err
		}
		configs[i].AccessKeyID, err = secure.DecryptString(configs[i].AccessKeyID)
		if err != nil {
			return nil, 0, err
		}
		configs[i].SecretAccessKey, err = secure.DecryptString(configs[i].SecretAccessKey)
		if err != nil {
			return nil, 0, err
		}
	}

	return configs, total, nil
}

// Update updates an existing R2 config
func (r *r2ConfigRepository) Update(config *models.R2Config) error {
	// Encrypt sensitive fields before saving
	var err error
	config.AccountID, err = secure.EncryptString(config.AccountID)
	if err != nil {
		return err
	}
	config.AccessKeyID, err = secure.EncryptString(config.AccessKeyID)
	if err != nil {
		return err
	}
	config.SecretAccessKey, err = secure.EncryptString(config.SecretAccessKey)
	if err != nil {
		return err
	}

	result := r.db.Save(config)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("r2 config not found")
	}
	return nil
}

// Delete soft deletes an R2 config
func (r *r2ConfigRepository) Delete(id uuid.UUID) error {
	result := r.db.Delete(&models.R2Config{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("r2 config not found")
	}
	return nil
}
