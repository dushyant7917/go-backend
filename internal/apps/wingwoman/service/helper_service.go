package service

import (
	"go-backend/internal/apps/wingwoman/models"
	"go-backend/internal/apps/wingwoman/repository"
)

// HelperService defines the interface for helper business logic
type HelperService interface {
	ListHelpersPaginated(page, pageSize int) (*models.PaginatedHelpersResponse, error)
}

// helperService implements HelperService
type helperService struct {
	repo repository.HelperRepository
}

// NewHelperService creates a new instance of HelperService
func NewHelperService(repo repository.HelperRepository) HelperService {
	return &helperService{repo: repo}
}

// ListHelpersPaginated retrieves helpers with pagination
func (s *helperService) ListHelpersPaginated(page, pageSize int) (*models.PaginatedHelpersResponse, error) {
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

	users, total, err := s.repo.FindHelpersPaginated(page, pageSize)
	if err != nil {
		return nil, err
	}

	// Build response
	responses := make([]models.HelperResponse, len(users))
	for i, user := range users {
		responses[i] = models.HelperResponse{
			ID:          user.ID,
			Name:        user.Name,
			CountryCode: user.CountryCode,
			Phone:       user.Phone,
			Email:       user.Email,
			Metadata:    user.Metadata,
			CreatedAt:   user.CreatedAt,
			UpdatedAt:   user.UpdatedAt,
		}
	}

	// Calculate total pages
	totalPages := int(total) / pageSize
	if int(total)%pageSize > 0 {
		totalPages++
	}

	// Calculate next and previous pages
	var nextPage, prevPage *int
	if page > 1 {
		prev := page - 1
		prevPage = &prev
	}
	if page < totalPages {
		next := page + 1
		nextPage = &next
	}

	return &models.PaginatedHelpersResponse{
		Data:       responses,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		NextPage:   nextPage,
		PrevPage:   prevPage,
	}, nil
}
