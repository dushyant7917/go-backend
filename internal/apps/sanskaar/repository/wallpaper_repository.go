package repository

import (
	"time"

	"go-backend/internal/apps/sanskaar/models"
	"go-backend/internal/common/pagination"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// WallpaperRepository defines the interface for wallpaper data operations
type WallpaperRepository interface {
	Create(wallpaper *models.Wallpaper) error
	FindByID(id uuid.UUID) (*models.Wallpaper, error)
	Update(wallpaper *models.Wallpaper) error
	FindWithFilters(deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize int, page, pageSize int) ([]models.Wallpaper, int64, error)
}

type wallpaperRepository struct {
	db *gorm.DB
}

// NewWallpaperRepository creates a new instance of WallpaperRepository
func NewWallpaperRepository(db *gorm.DB) WallpaperRepository {
	return &wallpaperRepository{db: db}
}

// Create creates a new wallpaper in the database
func (r *wallpaperRepository) Create(wallpaper *models.Wallpaper) error {
	return r.db.Create(wallpaper).Error
}

// FindByID retrieves a wallpaper by its ID
func (r *wallpaperRepository) FindByID(id uuid.UUID) (*models.Wallpaper, error) {
	var wallpaper models.Wallpaper
	if err := r.db.First(&wallpaper, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &wallpaper, nil
}

// Update updates an existing wallpaper
func (r *wallpaperRepository) Update(wallpaper *models.Wallpaper) error {
	return r.db.Save(wallpaper).Error
}

// FindWithFilters retrieves wallpapers with an optional deity filter and pagination. When
// userCreatedAt is set, pagination is restricted to the progressive-unlock eligible window
// (see pagination.FetchEligiblePage).
func (r *wallpaperRepository) FindWithFilters(deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize int, page, pageSize int) ([]models.Wallpaper, int64, error) {
	query := r.db.Model(&models.Wallpaper{})
	if deity != "" {
		query = query.Where("deity = ?", deity)
	}

	return pagination.FetchEligiblePage[models.Wallpaper](query, userCreatedAt, firstDayUnlockCount, dailyUnlockBatchSize, time.Now(), page, pageSize)
}
