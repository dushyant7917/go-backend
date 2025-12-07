package repository

import (
	"go-backend/internal/apps/crush/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CrushRepository defines the interface for crush data operations
type CrushRepository interface {
	Create(crush *models.Crush) error
	FindByID(id uuid.UUID) (*models.Crush, error)
	FindByUserID(userID uuid.UUID) ([]models.Crush, error)
	Update(crush *models.Crush) error
	FindCrushesOnUser(countryCode, phone, instagramID, snapchatID *string) ([]models.Crush, error)
}

// crushRepository implements CrushRepository
type crushRepository struct {
	db *gorm.DB
}

// NewCrushRepository creates a new instance of CrushRepository
func NewCrushRepository(db *gorm.DB) CrushRepository {
	return &crushRepository{db: db}
}

// Create creates a new crush in the database
func (r *crushRepository) Create(crush *models.Crush) error {
	return r.db.Create(crush).Error
}

// FindByID retrieves a crush by its ID
func (r *crushRepository) FindByID(id uuid.UUID) (*models.Crush, error) {
	var crush models.Crush
	if err := r.db.First(&crush, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &crush, nil
}

// FindByUserID retrieves all crushes for a specific user
func (r *crushRepository) FindByUserID(userID uuid.UUID) ([]models.Crush, error) {
	var crushes []models.Crush
	if err := r.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&crushes).Error; err != nil {
		return nil, err
	}
	return crushes, nil
}

// Update updates an existing crush
func (r *crushRepository) Update(crush *models.Crush) error {
	return r.db.Save(crush).Error
}

// FindCrushesOnUser finds all crushes on a user by matching identifiers
// Only non-nil identifiers are considered in the matching
func (r *crushRepository) FindCrushesOnUser(countryCode, phone, instagramID, snapchatID *string) ([]models.Crush, error) {
	var crushes []models.Crush
	query := r.db.Model(&models.Crush{})

	// Build OR conditions only for non-nil identifiers
	var conditions []interface{}

	// Check phone match (both country code and phone must match)
	if countryCode != nil && phone != nil && *countryCode != "" && *phone != "" {
		conditions = append(conditions, r.db.Where("country_code = ? AND phone = ?", *countryCode, *phone))
	}

	// Check Instagram ID match
	if instagramID != nil && *instagramID != "" {
		conditions = append(conditions, r.db.Where("instagram_id = ?", *instagramID))
	}

	// Check Snapchat ID match
	if snapchatID != nil && *snapchatID != "" {
		conditions = append(conditions, r.db.Where("snapchat_id = ?", *snapchatID))
	}

	// If no valid identifiers provided, return empty list
	if len(conditions) == 0 {
		return crushes, nil
	}

	// Combine conditions with OR
	for i, condition := range conditions {
		if i == 0 {
			query = query.Where(condition)
		} else {
			query = query.Or(condition)
		}
	}

	// Order by creation time (most recent first)
	if err := query.Order("created_at DESC").Find(&crushes).Error; err != nil {
		return nil, err
	}

	return crushes, nil
}
