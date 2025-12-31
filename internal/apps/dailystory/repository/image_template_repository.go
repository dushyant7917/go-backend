package repository

import (
	"go-backend/internal/apps/dailystory/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ImageTemplateRepository defines the interface for image template data operations
type ImageTemplateRepository interface {
	Create(template *models.ImageTemplate) error
	FindByID(id uuid.UUID) (*models.ImageTemplate, error)
	Update(template *models.ImageTemplate) error
	FindWithFilters(category, subCategory string, authorID *uuid.UUID, page, pageSize int) ([]models.ImageTemplate, int64, error)
}

// imageTemplateRepository implements ImageTemplateRepository
type imageTemplateRepository struct {
	db *gorm.DB
}

// NewImageTemplateRepository creates a new instance of ImageTemplateRepository
func NewImageTemplateRepository(db *gorm.DB) ImageTemplateRepository {
	return &imageTemplateRepository{db: db}
}

// Create creates a new image template in the database
func (r *imageTemplateRepository) Create(template *models.ImageTemplate) error {
	return r.db.Create(template).Error
}

// FindByID retrieves an image template by its ID
func (r *imageTemplateRepository) FindByID(id uuid.UUID) (*models.ImageTemplate, error) {
	var template models.ImageTemplate
	if err := r.db.First(&template, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &template, nil
}

// Update updates an existing image template
func (r *imageTemplateRepository) Update(template *models.ImageTemplate) error {
	return r.db.Save(template).Error
}

// FindWithFilters retrieves image templates with optional filters and pagination
func (r *imageTemplateRepository) FindWithFilters(category, subCategory string, authorID *uuid.UUID, page, pageSize int) ([]models.ImageTemplate, int64, error) {
	var templates []models.ImageTemplate
	var total int64

	query := r.db.Model(&models.ImageTemplate{})

	// Apply filters if provided
	if category != "" {
		query = query.Where("category = ?", category)
	}
	if subCategory != "" {
		query = query.Where("sub_category = ?", subCategory)
	}
	if authorID != nil {
		query = query.Where("author_id = ?", *authorID)
	}

	// Get total count
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Calculate offset
	offset := (page - 1) * pageSize

	// Get paginated results ordered by creation date (newest first)
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&templates).Error; err != nil {
		return nil, 0, err
	}

	return templates, total, nil
}
