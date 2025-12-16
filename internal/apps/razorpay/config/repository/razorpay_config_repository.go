package repository

import (
	"errors"

	"go-backend/internal/apps/razorpay/config/models"
	"go-backend/pkg/secure"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RazorpayConfigRepository defines the interface for razorpay config data operations
type RazorpayConfigRepository interface {
	Create(config *models.RazorpayConfig) error
	FindByID(id uuid.UUID) (*models.RazorpayConfig, error)
	FindByAppNameAndEnv(appName string, environment string) (*models.RazorpayConfig, error)
	FindAll(page, pageSize int, activeOnly bool) ([]models.RazorpayConfig, int64, error)
	Update(config *models.RazorpayConfig) error
	Delete(id uuid.UUID) error
}

// razorpayConfigRepository implements RazorpayConfigRepository interface
type razorpayConfigRepository struct {
	db *gorm.DB
}

// NewRazorpayConfigRepository creates a new instance of RazorpayConfigRepository
func NewRazorpayConfigRepository(db *gorm.DB) RazorpayConfigRepository {
	return &razorpayConfigRepository{db: db}
}

// Create creates a new razorpay config
func (r *razorpayConfigRepository) Create(config *models.RazorpayConfig) error {
	// Encrypt sensitive fields before saving
	var err error
	config.RazorpayKeyID, err = secure.EncryptString(config.RazorpayKeyID)
	if err != nil {
		return err
	}
	config.RazorpayKeySecret, err = secure.EncryptString(config.RazorpayKeySecret)
	if err != nil {
		return err
	}
	config.RazorpayWebhookSecret, err = secure.EncryptString(config.RazorpayWebhookSecret)
	if err != nil {
		return err
	}

	return r.db.Create(config).Error
}

// FindByID finds a razorpay config by ID
func (r *razorpayConfigRepository) FindByID(id uuid.UUID) (*models.RazorpayConfig, error) {
	var config models.RazorpayConfig
	if err := r.db.Where("id = ?", id).First(&config).Error; err != nil {
		return nil, err
	}

	// Decrypt sensitive fields before returning
	var err error
	config.RazorpayKeyID, err = secure.DecryptString(config.RazorpayKeyID)
	if err != nil {
		return nil, err
	}
	config.RazorpayKeySecret, err = secure.DecryptString(config.RazorpayKeySecret)
	if err != nil {
		return nil, err
	}
	config.RazorpayWebhookSecret, err = secure.DecryptString(config.RazorpayWebhookSecret)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// FindByAppNameAndEnv finds a razorpay config by app name and environment
func (r *razorpayConfigRepository) FindByAppNameAndEnv(appName string, environment string) (*models.RazorpayConfig, error) {
	var config models.RazorpayConfig
	if err := r.db.Where("app_name = ? AND environment = ? AND is_active = true", appName, environment).First(&config).Error; err != nil {
		return nil, err
	}

	// Decrypt sensitive fields before returning
	var err error
	config.RazorpayKeyID, err = secure.DecryptString(config.RazorpayKeyID)
	if err != nil {
		return nil, err
	}
	config.RazorpayKeySecret, err = secure.DecryptString(config.RazorpayKeySecret)
	if err != nil {
		return nil, err
	}
	config.RazorpayWebhookSecret, err = secure.DecryptString(config.RazorpayWebhookSecret)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// FindAll retrieves all razorpay configs with pagination
func (r *razorpayConfigRepository) FindAll(page, pageSize int, activeOnly bool) ([]models.RazorpayConfig, int64, error) {
	var configs []models.RazorpayConfig
	var total int64

	query := r.db.Model(&models.RazorpayConfig{})

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

	// Decrypt sensitive fields for each config
	for i := range configs {
		var err error
		configs[i].RazorpayKeyID, err = secure.DecryptString(configs[i].RazorpayKeyID)
		if err != nil {
			return nil, 0, err
		}
		configs[i].RazorpayKeySecret, err = secure.DecryptString(configs[i].RazorpayKeySecret)
		if err != nil {
			return nil, 0, err
		}
		configs[i].RazorpayWebhookSecret, err = secure.DecryptString(configs[i].RazorpayWebhookSecret)
		if err != nil {
			return nil, 0, err
		}
	}

	return configs, total, nil
}

// Update updates an existing razorpay config
func (r *razorpayConfigRepository) Update(config *models.RazorpayConfig) error {
	// Encrypt sensitive fields before saving
	var err error
	config.RazorpayKeyID, err = secure.EncryptString(config.RazorpayKeyID)
	if err != nil {
		return err
	}
	config.RazorpayKeySecret, err = secure.EncryptString(config.RazorpayKeySecret)
	if err != nil {
		return err
	}
	config.RazorpayWebhookSecret, err = secure.EncryptString(config.RazorpayWebhookSecret)
	if err != nil {
		return err
	}

	result := r.db.Save(config)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("razorpay config not found")
	}
	return nil
}

// Delete soft deletes a razorpay config
func (r *razorpayConfigRepository) Delete(id uuid.UUID) error {
	result := r.db.Delete(&models.RazorpayConfig{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("razorpay config not found")
	}
	return nil
}
