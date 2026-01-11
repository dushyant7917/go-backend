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
	GetUserPosterStatsByAppName(appName, sortBy string, page, pageSize int) ([]models.UserPosterStatsResponse, int64, error)
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

// GetUserPosterStatsByAppName retrieves user poster statistics filtered by app_name with pagination and sorting
// Returns users sorted based on the sortBy parameter
// Supported sortBy values:
// - "most_active": Most recent activity first, with highest engagement as tiebreaker (currently active power users)
// - "least_active": Least recent activity first, with lowest engagement as tiebreaker (low-usage inactive users to contact for feedback)
// - "power_users": Highest poster count first, with recent activity as tiebreaker (top content creators)
// - "new_engaged": Newest users first, with highest engagement as tiebreaker (highly engaged new users)
func (r *imagePosterRepository) GetUserPosterStatsByAppName(appName, sortBy string, page, pageSize int) ([]models.UserPosterStatsResponse, int64, error) {
	var stats []models.UserPosterStatsResponse
	var total int64

	// Calculate offset
	offset := (page - 1) * pageSize

	// Query to get total count of distinct users who have generated posters
	countQuery := r.db.Table("image_posters").Select("COUNT(DISTINCT image_posters.user_id)").
		Joins("INNER JOIN users ON users.id = image_posters.user_id").
		Where("users.app_name = ?", appName)

	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Determine ORDER BY clause based on sortBy parameter
	var orderClause string
	switch sortBy {
	case "most_active":
		orderClause = "last_generation_at DESC, poster_count DESC"
	case "least_active":
		orderClause = "last_generation_at ASC, poster_count ASC"
	case "power_users":
		orderClause = "poster_count DESC, last_generation_at DESC"
	case "new_engaged":
		orderClause = "user_created_at DESC, poster_count DESC"
	default:
		// Default to most active users
		orderClause = "last_generation_at DESC, poster_count DESC"
	}

	// Main query to get user poster stats
	// For each user, we get:
	// - user_id
	// - user_name
	// - country_code
	// - phone
	// - user_created_at (sign up date)
	// - count of posters
	// - max(created_at) as the last generation date
	query := r.db.Table("image_posters").
		Select(`
			image_posters.user_id,
			users.name as user_name,
			users.country_code,
			users.phone,
			users.created_at as user_created_at,
			COUNT(image_posters.id) as poster_count,
			MAX(image_posters.created_at) as last_generation_at
		`).
		Joins("INNER JOIN users ON users.id = image_posters.user_id").
		Where("users.app_name = ?", appName).
		Group("image_posters.user_id, users.name, users.country_code, users.phone, users.created_at").
		Order(orderClause).
		Limit(pageSize).
		Offset(offset)

	if err := query.Scan(&stats).Error; err != nil {
		return nil, 0, err
	}

	return stats, total, nil
}
