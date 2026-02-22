package service

import (
	"fmt"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/repository"
	userRepository "go-backend/internal/apps/user/repository"
	"go-backend/pkg/storage"
)

// ImagePosterService defines the interface for image poster business logic
type ImagePosterService interface {
	CreatePoster(req *models.CreatePosterRequest) (*models.ImagePoster, error)
	GetUserPosterStatsByAppName(appName, sortBy string, page, pageSize int) (*models.PaginatedUserPosterStatsResponse, error)
}

// imagePosterService implements ImagePosterService
type imagePosterService struct {
	posterRepo   repository.ImagePosterRepository
	templateRepo repository.ImageTemplateRepository
	userRepo     userRepository.UserRepository
	r2Client     *storage.R2Client
}

// NewImagePosterService creates a new instance of ImagePosterService
func NewImagePosterService(
	posterRepo repository.ImagePosterRepository,
	templateRepo repository.ImageTemplateRepository,
	userRepo userRepository.UserRepository,
	r2Client *storage.R2Client,
) ImagePosterService {
	return &imagePosterService{
		posterRepo:   posterRepo,
		templateRepo: templateRepo,
		userRepo:     userRepo,
		r2Client:     r2Client,
	}
}

// CreatePoster creates a new poster record in the database
func (s *imagePosterService) CreatePoster(req *models.CreatePosterRequest) (*models.ImagePoster, error) {
	// Create poster record
	poster := &models.ImagePoster{
		UserID:                req.UserID,
		TemplateID:            req.TemplateID,
		NameUsed:              req.NameUsed,
		DetailUsed:            req.DetailUsed,
		ProfilePictureKeyUsed: req.ProfilePictureKeyUsed,
		FileKey:               req.FileKey,
	}

	if err := s.posterRepo.Create(poster); err != nil {
		return nil, fmt.Errorf("failed to create poster: %w", err)
	}

	return poster, nil
}

// GetUserPosterStatsByAppName retrieves paginated user poster statistics filtered by app_name with sorting
// Supported sortBy values:
// - "most_active": Most recent activity first, with highest engagement as tiebreaker (find currently active power users)
// - "least_active": Least recent activity first, with lowest engagement as tiebreaker (find low-usage inactive users to contact for feedback)
// - "power_users": Highest poster count first, with recent activity as tiebreaker (find top content creators)
// - "new_engaged": Newest users first, with highest engagement as tiebreaker (find highly engaged new users)
func (s *imagePosterService) GetUserPosterStatsByAppName(appName, sortBy string, page, pageSize int) (*models.PaginatedUserPosterStatsResponse, error) {
	// Validate sortBy parameter
	validSortOptions := map[string]bool{
		"most_active":  true,
		"least_active": true,
		"power_users":  true,
		"new_engaged":  true,
	}

	if !validSortOptions[sortBy] {
		return nil, fmt.Errorf("invalid sort_by value: must be one of [most_active, least_active, power_users, new_engaged]")
	}

	// Validate pagination parameters
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// Get stats from repository
	stats, total, err := s.posterRepo.GetUserPosterStatsByAppName(appName, sortBy, page, pageSize)
	if err != nil {
		return nil, fmt.Errorf("failed to get user poster stats: %w", err)
	}

	// Calculate pagination metadata
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))
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

	return &models.PaginatedUserPosterStatsResponse{
		Data:       stats,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		NextPage:   nextPage,
		PrevPage:   prevPage,
	}, nil
}
