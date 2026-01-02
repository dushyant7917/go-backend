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
	FindWithFilters(category, subCategory string, authorID *uuid.UUID, status *string, page, pageSize int) ([]models.ImageTemplate, int64, error)
	GetDesignerStats() ([]models.DesignerStatsResponse, error)
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
func (r *imageTemplateRepository) FindWithFilters(category, subCategory string, authorID *uuid.UUID, status *string, page, pageSize int) ([]models.ImageTemplate, int64, error) {
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
	if status != nil {
		// Filter by status field in metadata (published, approved, or rejected)
		query = query.Where("metadata @> ?", `{"status":"`+*status+`"}`)
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

// GetDesignerStats retrieves template creation statistics for designers (users with app_name='TemplateDesigner')
func (r *imageTemplateRepository) GetDesignerStats() ([]models.DesignerStatsResponse, error) {
	var results []models.DesignerStatsResponse

	// Use GORM query builder with joins and aggregations
	err := r.db.Table("users u").
		Select(`
			u.id as user_id,
			u.name as user_name,
			COUNT(CASE WHEN it.created_at >= CURRENT_DATE THEN 1 END) as templates_created_today,
			COUNT(CASE WHEN it.created_at >= date_trunc('week', CURRENT_DATE) THEN 1 END) as templates_created_this_week,
			COUNT(CASE WHEN it.created_at >= date_trunc('month', CURRENT_DATE) THEN 1 END) as templates_created_this_month,
			COUNT(it.id) as templates_created_total,
			COUNT(CASE WHEN it.metadata->>'status' = 'published' THEN 1 END) as templates_pending_approval
		`).
		Joins("LEFT JOIN image_templates it ON u.id = it.author_id").
		Where("u.app_name = ? AND u.deleted_at IS NULL", "TemplateDesigner").
		Group("u.id, u.name").
		Order("templates_created_total DESC, u.created_at DESC").
		Scan(&results).Error

	if err != nil {
		return nil, err
	}

	return results, nil
}
