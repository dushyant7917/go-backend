package service

import (
	"errors"
	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/repository"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ImageTemplateService defines the interface for image template business logic
type ImageTemplateService interface {
	CreateImageTemplate(req models.CreateImageTemplateRequest) (*models.ImageTemplateResponse, error)
	UpdateImageTemplate(id uuid.UUID, req models.UpdateImageTemplateRequest) (*models.ImageTemplateResponse, error)
	GetImageTemplateByID(id uuid.UUID) (*models.ImageTemplateResponse, error)
	GetImageTemplatesWithFilters(category, subCategory string, authorID *uuid.UUID, status *string, page, pageSize int) (*models.PaginatedImageTemplatesResponse, error)
	GetDesignerStats() ([]models.DesignerStatsResponse, error)
	GetPosterCountByTemplate(orderByCount bool, page, pageSize int) (*models.PaginatedTemplatePosterCountResponse, error)
}

// imageTemplateService implements ImageTemplateService
type imageTemplateService struct {
	repo repository.ImageTemplateRepository
}

// NewImageTemplateService creates a new instance of ImageTemplateService
func NewImageTemplateService(repo repository.ImageTemplateRepository) ImageTemplateService {
	return &imageTemplateService{
		repo: repo,
	}
}

// CreateImageTemplate creates a new image template
func (s *imageTemplateService) CreateImageTemplate(req models.CreateImageTemplateRequest) (*models.ImageTemplateResponse, error) {
	// Build model
	template := &models.ImageTemplate{
		FileKey:     req.FileKey,
		Category:    req.Category,
		SubCategory: req.SubCategory,
		Config:      req.Config,
		Metadata:    req.Metadata,
		AuthorID:    req.AuthorID,
	}

	if err := s.repo.Create(template); err != nil {
		return nil, err
	}
	resp := template.ToResponse()
	return &resp, nil
}

// GetImageTemplateByID retrieves an image template by ID
func (s *imageTemplateService) GetImageTemplateByID(id uuid.UUID) (*models.ImageTemplateResponse, error) {
	template, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("image template not found")
		}
		return nil, err
	}
	resp := template.ToResponse()
	return &resp, nil
}

// UpdateImageTemplate updates an existing image template
func (s *imageTemplateService) UpdateImageTemplate(id uuid.UUID, req models.UpdateImageTemplateRequest) (*models.ImageTemplateResponse, error) {
	template, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("image template not found")
		}
		return nil, err
	}

	// Apply updates if provided
	if req.FileKey != nil {
		template.FileKey = *req.FileKey
	}
	if req.Category != nil {
		template.Category = *req.Category
	}
	if req.SubCategory != nil {
		template.SubCategory = *req.SubCategory
	}
	if req.Config != nil {
		template.Config = req.Config
	}
	if req.Metadata != nil && len(req.Metadata) > 0 {
		// Merge metadata (partial update)
		if template.Metadata == nil {
			template.Metadata = make(utils.Metadata)
		}
		for key, value := range req.Metadata {
			template.Metadata[key] = value
		}
	}
	if req.AuthorID != nil {
		template.AuthorID = req.AuthorID
	}

	if err := s.repo.Update(template); err != nil {
		return nil, err
	}
	resp := template.ToResponse()
	return &resp, nil
}

// GetImageTemplatesWithFilters retrieves image templates with optional filters and pagination
func (s *imageTemplateService) GetImageTemplatesWithFilters(category, subCategory string, authorID *uuid.UUID, status *string, page, pageSize int) (*models.PaginatedImageTemplatesResponse, error) {
	// Validate page and pageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10 // default page size
	}
	if pageSize > 100 {
		pageSize = 100 // max page size
	}

	templates, total, err := s.repo.FindWithFilters(category, subCategory, authorID, status, page, pageSize)
	if err != nil {
		return nil, err
	}

	// Build response
	responses := make([]models.ImageTemplateResponse, len(templates))
	for i, template := range templates {
		responses[i] = template.ToResponse()
	}

	// Calculate pagination metadata
	totalPages := int(total) / pageSize
	if int(total)%pageSize > 0 {
		totalPages++
	}

	var nextPage *int
	var prevPage *int
	if page < totalPages {
		next := page + 1
		nextPage = &next
	}
	if page > 1 {
		prev := page - 1
		prevPage = &prev
	}

	return &models.PaginatedImageTemplatesResponse{
		Data:       responses,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		NextPage:   nextPage,
		PrevPage:   prevPage,
	}, nil
}

// GetDesignerStats retrieves template creation statistics for designers
func (s *imageTemplateService) GetDesignerStats() ([]models.DesignerStatsResponse, error) {
	return s.repo.GetDesignerStats()
}

// GetPosterCountByTemplate retrieves count of posters generated for each template with pagination
func (s *imageTemplateService) GetPosterCountByTemplate(orderByCount bool, page, pageSize int) (*models.PaginatedTemplatePosterCountResponse, error) {
	// Validate page and pageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10 // default page size
	}
	if pageSize > 100 {
		pageSize = 100 // max page size
	}

	results, total, err := s.repo.GetPosterCountByTemplate(orderByCount, page, pageSize)
	if err != nil {
		return nil, err
	}

	// Calculate pagination metadata
	totalPages := int(total) / pageSize
	if int(total)%pageSize > 0 {
		totalPages++
	}

	var nextPage *int
	var prevPage *int
	if page < totalPages {
		next := page + 1
		nextPage = &next
	}
	if page > 1 {
		prev := page - 1
		prevPage = &prev
	}

	return &models.PaginatedTemplatePosterCountResponse{
		Data:       results,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		NextPage:   nextPage,
		PrevPage:   prevPage,
	}, nil
}
