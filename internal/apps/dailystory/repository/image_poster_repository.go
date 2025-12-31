package repository

import (
	"go-backend/internal/apps/dailystory/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ImagePosterRepository defines the interface for image poster data operations
type ImagePosterRepository interface {
	Create(poster *models.ImagePoster) error
	CreateWithTx(tx *gorm.DB, poster *models.ImagePoster) error
	FindByCombo(userID, templateID uuid.UUID, nameUsed, profilePictureKeyUsed string) (*models.ImagePoster, error)
	GetDB() *gorm.DB
}

// imagePosterRepository implements ImagePosterRepository
type imagePosterRepository struct {
	db *gorm.DB
}

// NewImagePosterRepository creates a new instance of ImagePosterRepository
func NewImagePosterRepository(db *gorm.DB) ImagePosterRepository {
	return &imagePosterRepository{db: db}
}

// Create creates a new image poster in the database
func (r *imagePosterRepository) Create(poster *models.ImagePoster) error {
	return r.db.Create(poster).Error
}

// CreateWithTx creates a new image poster within a transaction
func (r *imagePosterRepository) CreateWithTx(tx *gorm.DB, poster *models.ImagePoster) error {
	return tx.Create(poster).Error
}

// GetDB returns the underlying database instance for transaction management
func (r *imagePosterRepository) GetDB() *gorm.DB {
	return r.db
}

// FindByCombo retrieves an image poster by the unique combo of user_id, template_id, name_used, and profile_picture_key_used
func (r *imagePosterRepository) FindByCombo(userID, templateID uuid.UUID, nameUsed, profilePictureKeyUsed string) (*models.ImagePoster, error) {
	var poster models.ImagePoster
	if err := r.db.Where("user_id = ? AND template_id = ? AND name_used = ? AND profile_picture_key_used = ?",
		userID, templateID, nameUsed, profilePictureKeyUsed).First(&poster).Error; err != nil {
		return nil, err
	}
	return &poster, nil
}
