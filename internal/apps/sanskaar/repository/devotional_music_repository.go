package repository

import (
	"time"

	"go-backend/internal/apps/sanskaar/models"
	"go-backend/internal/common/pagination"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DevotionalMusicRepository defines the interface for devotional music data operations
type DevotionalMusicRepository interface {
	Create(music *models.DevotionalMusic) error
	FindByID(id uuid.UUID) (*models.DevotionalMusic, error)
	Update(music *models.DevotionalMusic) error
	FindWithFilters(category, deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize int, page, pageSize int) ([]models.DevotionalMusic, int64, error)
}

type devotionalMusicRepository struct {
	db *gorm.DB
}

// NewDevotionalMusicRepository creates a new instance of DevotionalMusicRepository
func NewDevotionalMusicRepository(db *gorm.DB) DevotionalMusicRepository {
	return &devotionalMusicRepository{db: db}
}

// Create creates a new devotional music track in the database
func (r *devotionalMusicRepository) Create(music *models.DevotionalMusic) error {
	return r.db.Create(music).Error
}

// FindByID retrieves a devotional music track by its ID
func (r *devotionalMusicRepository) FindByID(id uuid.UUID) (*models.DevotionalMusic, error) {
	var music models.DevotionalMusic
	if err := r.db.First(&music, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &music, nil
}

// Update updates an existing devotional music track
func (r *devotionalMusicRepository) Update(music *models.DevotionalMusic) error {
	return r.db.Save(music).Error
}

// FindWithFilters retrieves devotional music tracks with optional category/deity filters and
// pagination. When userCreatedAt is set, pagination is restricted to the progressive-unlock
// eligible window (see pagination.FetchEligiblePage).
func (r *devotionalMusicRepository) FindWithFilters(category, deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize int, page, pageSize int) ([]models.DevotionalMusic, int64, error) {
	query := r.db.Model(&models.DevotionalMusic{})
	if category != "" {
		query = query.Where("category = ?", category)
	}
	if deity != "" {
		query = query.Where("deity = ?", deity)
	}

	return pagination.FetchEligiblePage[models.DevotionalMusic](query, userCreatedAt, firstDayUnlockCount, dailyUnlockBatchSize, time.Now(), page, pageSize)
}
