package repository

import (
	"go-backend/internal/apps/user/models"

	"gorm.io/gorm"
)

// HelperRepository defines the interface for helper data operations
type HelperRepository interface {
	FindHelpersPaginated(page, pageSize int) ([]models.User, int64, error)
}

// helperRepository implements HelperRepository
type helperRepository struct {
	db *gorm.DB
}

// NewHelperRepository creates a new instance of HelperRepository
func NewHelperRepository(db *gorm.DB) HelperRepository {
	return &helperRepository{db: db}
}

// FindHelpersPaginated retrieves helpers (users with metadata['type']='helper' and app_name='WingWoman') with pagination
func (r *helperRepository) FindHelpersPaginated(page, pageSize int) ([]models.User, int64, error) {
	var users []models.User
	var total int64

	query := r.db.Model(&models.User{}).Where("app_name = ? AND metadata->>'type' = ?", "WingWoman", "helper")

	// Get total count
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Calculate offset
	offset := (page - 1) * pageSize

	// Get paginated results
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&users).Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}
