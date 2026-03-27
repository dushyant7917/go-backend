package repository

import (
	"time"

	"go-backend/internal/apps/dailystory/models"

	"gorm.io/gorm"
)

// NewsRepository defines the interface for news data operations
type NewsRepository interface {
	FindAllPaginated(category, languageCode, status string, createdAtFrom *time.Time, page, pageSize int) ([]models.NewsResponse, int64, error)
	FindByID(id string) (*models.News, error)
	Update(news *models.News) error
}

// newsRepository implements NewsRepository
type newsRepository struct {
	db *gorm.DB
}

// NewNewsRepository creates a new instance of NewsRepository
func NewNewsRepository(db *gorm.DB) NewsRepository {
	return &newsRepository{db: db}
}

// FindAllPaginated retrieves news with pagination and optional category, language code, status, and created_at_from filters
func (r *newsRepository) FindAllPaginated(category, languageCode, status string, createdAtFrom *time.Time, page, pageSize int) ([]models.NewsResponse, int64, error) {
	var results []models.NewsResponse
	var total int64

	// Build the base query with join to news_translations
	query := r.db.Table("news").
		Select(`
			news.id,
			news.link,
			news.media_file_key,
			news.category,
			news.status,
			news_translations.title,
			news_translations.language_code,
			news.published_at,
			news.metadata,
			news.created_at,
			news.updated_at
		`).
		Joins("LEFT JOIN news_translations ON news_translations.news_id = news.id")

	// Apply category filter if provided
	if category != "" {
		query = query.Where("news.category = ?", category)
	}

	// Apply language code filter if provided
	if languageCode != "" {
		query = query.Where("news_translations.language_code = ?", languageCode)
	}

	// Apply status filter if provided
	if status != "" {
		query = query.Where("news.status = ?", status)
	}

	// Apply created_at_from filter if provided
	if createdAtFrom != nil {
		query = query.Where("news.created_at >= ?", createdAtFrom)
	}

	// Get total count
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Calculate offset
	offset := (page - 1) * pageSize

	// Get paginated results
	err := query.Order("news.published_at DESC NULLS LAST, news.created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Scan(&results).Error

	if err != nil {
		return nil, 0, err
	}

	return results, total, nil
}

// FindByID retrieves a news article by ID
func (r *newsRepository) FindByID(id string) (*models.News, error) {
	var news models.News
	if err := r.db.Where("id = ?", id).First(&news).Error; err != nil {
		return nil, err
	}
	return &news, nil
}

// Update updates a news article
func (r *newsRepository) Update(news *models.News) error {
	return r.db.Save(news).Error
}
