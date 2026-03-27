package repository

import (
	"go-backend/internal/apps/dailystory/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// NewsPosterRepository defines the interface for news poster data operations
type NewsPosterRepository interface {
	Create(newsPoster *models.NewsPoster) error
	FindByID(id uuid.UUID) (*models.NewsPoster, error)
	FindByNewsIDAndUserID(newsID, userID uuid.UUID) (*models.NewsPoster, error)
}

// newsPosterRepository implements NewsPosterRepository
type newsPosterRepository struct {
	db *gorm.DB
}

// NewNewsPosterRepository creates a new instance of NewsPosterRepository
func NewNewsPosterRepository(db *gorm.DB) NewsPosterRepository {
	return &newsPosterRepository{db: db}
}

// Create creates a new news poster in the database
func (r *newsPosterRepository) Create(newsPoster *models.NewsPoster) error {
	return r.db.Create(newsPoster).Error
}

// FindByID retrieves a news poster by ID
func (r *newsPosterRepository) FindByID(id uuid.UUID) (*models.NewsPoster, error) {
	var newsPoster models.NewsPoster
	if err := r.db.Where("id = ?", id).First(&newsPoster).Error; err != nil {
		return nil, err
	}
	return &newsPoster, nil
}

// FindByNewsIDAndUserID retrieves a news poster by news_id and user_id
func (r *newsPosterRepository) FindByNewsIDAndUserID(newsID, userID uuid.UUID) (*models.NewsPoster, error) {
	var newsPoster models.NewsPoster
	if err := r.db.Where("news_id = ? AND user_id = ?", newsID, userID).First(&newsPoster).Error; err != nil {
		return nil, err
	}
	return &newsPoster, nil
}
