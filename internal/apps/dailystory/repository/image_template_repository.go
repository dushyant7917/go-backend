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
	GetPosterCountByTemplate(orderByCount bool, page, pageSize int) ([]models.TemplatePosterCountResponse, int64, error)
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

// GetPosterCountByTemplate retrieves count of posters generated for each template with pagination
// orderByCount: true = order by poster count DESC, false = order by template created_at DESC
// Only includes templates where metadata['status'] = 'approved'
func (r *imageTemplateRepository) GetPosterCountByTemplate(orderByCount bool, page, pageSize int) ([]models.TemplatePosterCountResponse, int64, error) {
	var results []models.TemplatePosterCountResponse
	var total int64

	// Build the base query with LEFT JOIN and GROUP BY, filtering for approved templates only
	query := r.db.Table("image_templates it").
		Select(`
			it.id as template_id,
			it.file_key,
			it.category,
			it.sub_category,
			COUNT(ip.id) as poster_count,
			it.created_at
		`).
		Joins("LEFT JOIN image_posters ip ON it.id = ip.template_id").
		Where("it.metadata @> ?", `{"status":"approved"}`). // Filter for approved templates only
		Group("it.id, it.file_key, it.category, it.sub_category, it.created_at")

	// Get total count before pagination
	var countResult []struct {
		Count int64
	}
	countQuery := r.db.Table("image_templates it").
		Select("COUNT(DISTINCT it.id) as count").
		Where("it.metadata @> ?", `{"status":"approved"}`) // Filter for approved templates only
	if err := countQuery.Scan(&countResult).Error; err != nil {
		return nil, 0, err
	}
	if len(countResult) > 0 {
		total = countResult[0].Count
	}

	// Apply ordering based on parameter
	if orderByCount {
		query = query.Order("poster_count DESC, it.created_at DESC")
	} else {
		query = query.Order("it.created_at DESC")
	}

	// Apply pagination
	offset := (page - 1) * pageSize
	query = query.Offset(offset).Limit(pageSize)

	err := query.Scan(&results).Error
	if err != nil {
		return nil, 0, err
	}

	return results, total, nil
}
