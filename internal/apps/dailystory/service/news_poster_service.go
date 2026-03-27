package service

import (
	"fmt"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/repository"
	"go-backend/pkg/utils"
)

// NewsPosterService defines the interface for news poster business logic
type NewsPosterService interface {
	CreateNewsPoster(req *models.CreateNewsPosterRequest) (*models.NewsPosterResponse, error)
}

// newsPosterService implements NewsPosterService
type newsPosterService struct {
	repo repository.NewsPosterRepository
}

// NewNewsPosterService creates a new instance of NewsPosterService
func NewNewsPosterService(repo repository.NewsPosterRepository) NewsPosterService {
	return &newsPosterService{repo: repo}
}

// CreateNewsPoster creates a new news poster record in the database
func (s *newsPosterService) CreateNewsPoster(req *models.CreateNewsPosterRequest) (*models.NewsPosterResponse, error) {
	// Initialize metadata if not provided
	metadata := utils.Metadata{}
	if req.Metadata != nil {
		metadata = *req.Metadata
	}

	// Create news poster record
	newsPoster := &models.NewsPoster{
		NewsID:            req.NewsID,
		UserID:            req.UserID,
		ProfilePictureKey: req.ProfilePictureKey,
		UserName:          req.UserName,
		UserDetail:        req.UserDetail,
		UserStateID:       req.UserStateID,
		LanguageCode:      req.LanguageCode,
		Metadata:          metadata,
	}

	if err := s.repo.Create(newsPoster); err != nil {
		return nil, fmt.Errorf("failed to create news poster: %w", err)
	}

	// Convert to response
	response := &models.NewsPosterResponse{
		ID:                newsPoster.ID,
		NewsID:            newsPoster.NewsID,
		UserID:            newsPoster.UserID,
		ProfilePictureKey: newsPoster.ProfilePictureKey,
		UserName:          newsPoster.UserName,
		UserDetail:        newsPoster.UserDetail,
		UserStateID:       newsPoster.UserStateID,
		LanguageCode:      newsPoster.LanguageCode,
		Metadata:          newsPoster.Metadata,
		CreatedAt:         newsPoster.CreatedAt,
		UpdatedAt:         newsPoster.UpdatedAt,
	}

	return response, nil
}
