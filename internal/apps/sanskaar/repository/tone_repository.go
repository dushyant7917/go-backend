package repository

import (
	"time"

	"go-backend/internal/apps/sanskaar/models"
	"go-backend/internal/common/pagination"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ToneRepository defines the interface for tone data operations
type ToneRepository interface {
	Create(tone *models.Tone) error
	FindByID(id uuid.UUID) (*models.Tone, error)
	Update(tone *models.Tone) error
	FindWithFilters(deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize int, page, pageSize int) ([]models.Tone, int64, error)
}

type toneRepository struct {
	db *gorm.DB
}

// NewToneRepository creates a new instance of ToneRepository
func NewToneRepository(db *gorm.DB) ToneRepository {
	return &toneRepository{db: db}
}

// Create creates a new tone in the database
func (r *toneRepository) Create(tone *models.Tone) error {
	return r.db.Create(tone).Error
}

// FindByID retrieves a tone by its ID
func (r *toneRepository) FindByID(id uuid.UUID) (*models.Tone, error) {
	var tone models.Tone
	if err := r.db.First(&tone, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &tone, nil
}

// Update updates an existing tone
func (r *toneRepository) Update(tone *models.Tone) error {
	return r.db.Save(tone).Error
}

// FindWithFilters retrieves tones with an optional deity filter and pagination. When
// userCreatedAt is set, pagination is restricted to the progressive-unlock eligible window
// (see pagination.FetchEligiblePage).
func (r *toneRepository) FindWithFilters(deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize int, page, pageSize int) ([]models.Tone, int64, error) {
	query := r.db.Model(&models.Tone{})
	if deity != "" {
		query = query.Where("deity = ?", deity)
	}

	return pagination.FetchEligiblePage[models.Tone](query, userCreatedAt, firstDayUnlockCount, dailyUnlockBatchSize, time.Now(), page, pageSize)
}
