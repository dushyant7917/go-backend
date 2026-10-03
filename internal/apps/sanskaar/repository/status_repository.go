package repository

import (
	"time"

	"go-backend/internal/apps/sanskaar/models"
	"go-backend/internal/common/pagination"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// StatusRepository defines the interface for status data operations
type StatusRepository interface {
	Create(status *models.Status) error
	FindByID(id uuid.UUID) (*models.Status, error)
	Update(status *models.Status) error
	FindWithFilters(category, deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize int, page, pageSize int) ([]models.Status, int64, error)
}

type statusRepository struct {
	db *gorm.DB
}

// NewStatusRepository creates a new instance of StatusRepository
func NewStatusRepository(db *gorm.DB) StatusRepository {
	return &statusRepository{db: db}
}

// Create creates a new status in the database
func (r *statusRepository) Create(status *models.Status) error {
	return r.db.Create(status).Error
}

// FindByID retrieves a status by its ID
func (r *statusRepository) FindByID(id uuid.UUID) (*models.Status, error) {
	var status models.Status
	if err := r.db.First(&status, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &status, nil
}

// Update updates an existing status
func (r *statusRepository) Update(status *models.Status) error {
	return r.db.Save(status).Error
}

// FindWithFilters retrieves statuses with optional category/deity filters and pagination. When
// userCreatedAt is set, pagination is restricted to the progressive-unlock eligible window
// (see pagination.FetchEligiblePage).
func (r *statusRepository) FindWithFilters(category, deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize int, page, pageSize int) ([]models.Status, int64, error) {
	query := r.db.Model(&models.Status{})
	if category != "" {
		query = query.Where("category = ?", category)
	}
	if deity != "" {
		query = query.Where("deity = ?", deity)
	}

	return pagination.FetchEligiblePage[models.Status](query, userCreatedAt, firstDayUnlockCount, dailyUnlockBatchSize, time.Now(), page, pageSize)
}
