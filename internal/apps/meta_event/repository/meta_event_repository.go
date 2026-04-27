package repository

import (
	"go-backend/internal/apps/meta_event/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MetaEventRepository defines the interface for meta event data operations
type MetaEventRepository interface {
	Create(tx *gorm.DB, event *models.MetaEvent) error
	FindOldestNonTriggered(userID uuid.UUID, appName string) (*models.MetaEvent, error)
	FindByID(id uuid.UUID) (*models.MetaEvent, error)
	Update(tx *gorm.DB, event *models.MetaEvent) error
}

// metaEventRepository implements MetaEventRepository interface
type metaEventRepository struct {
	db *gorm.DB
}

// NewMetaEventRepository creates a new instance of MetaEventRepository
func NewMetaEventRepository(db *gorm.DB) MetaEventRepository {
	return &metaEventRepository{db: db}
}

// Create creates a new meta event in the database
func (r *metaEventRepository) Create(tx *gorm.DB, event *models.MetaEvent) error {
	return r.getDB(tx).Create(event).Error
}

// FindOldestNonTriggered retrieves the oldest non-triggered meta event for a user and app
func (r *metaEventRepository) FindOldestNonTriggered(userID uuid.UUID, appName string) (*models.MetaEvent, error) {
	var event models.MetaEvent
	err := r.db.Where("user_id = ? AND app_name = ? AND triggered = false", userID, appName).
		Order("created_at ASC").
		First(&event).Error
	if err != nil {
		return nil, err
	}
	return &event, nil
}

// FindByID retrieves a meta event by its ID
func (r *metaEventRepository) FindByID(id uuid.UUID) (*models.MetaEvent, error) {
	var event models.MetaEvent
	err := r.db.Where("id = ?", id).First(&event).Error
	if err != nil {
		return nil, err
	}
	return &event, nil
}

// Update updates an existing meta event
func (r *metaEventRepository) Update(tx *gorm.DB, event *models.MetaEvent) error {
	return r.getDB(tx).Save(event).Error
}

// getDB returns the transaction if provided, otherwise returns the default db
func (r *metaEventRepository) getDB(tx *gorm.DB) *gorm.DB {
	if tx != nil {
		return tx
	}
	return r.db
}
