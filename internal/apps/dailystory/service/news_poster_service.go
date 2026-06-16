package service

import (
	"errors"
	"fmt"
	"os"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/repository"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var ErrNewsPosterPrefillUserNotFound = errors.New("user not found or does not belong to DailyStoryApp")
var ErrNewsPosterPrefillNewsNotFound = errors.New("news not found")
var ErrNewsPosterPrefillTranslationNotFound = errors.New("news translation not found for user's language")

// NewsPosterService defines the interface for news poster business logic
type NewsPosterService interface {
	CreateNewsPoster(req *models.CreateNewsPosterRequest) (*models.NewsPosterResponse, error)
	GetNewsPosterPrefillData(newsID, userID uuid.UUID) (*models.NewsPosterPrefillResponse, error)
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

// GetNewsPosterPrefillData fetches user and news translation data needed to prefill a news poster.
func (s *newsPosterService) GetNewsPosterPrefillData(newsID, userID uuid.UUID) (*models.NewsPosterPrefillResponse, error) {
	row, err := s.repo.FindPrefillData(newsID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNewsPosterPrefillUserNotFound
		}
		return nil, err
	}

	if !row.NewsExists {
		return nil, ErrNewsPosterPrefillNewsNotFound
	}

	if row.NewsTitle == nil {
		return nil, ErrNewsPosterPrefillTranslationNotFound
	}

	var mediaLink *string
	if row.MediaFileKey != nil && *row.MediaFileKey != "" {
		if publicURLBase := os.Getenv("R2_DS_NEWS_PUBLIC_URL"); publicURLBase != "" {
			ml := publicURLBase + "/" + *row.MediaFileKey
			mediaLink = &ml
		}
	}

	return &models.NewsPosterPrefillResponse{
		User: &models.NewsPosterPrefillUserData{
			Name:              row.UserName,
			Phone:             row.UserPhone,
			Detail:            row.UserDetail,
			DisplayPhone:      row.DisplayPhone,
			ProfilePictureKey: row.ProfilePictureKey,
			LanguageCode:      row.UserLanguageCode,
		},
		News: &models.NewsPosterPrefillNewsData{
			Title:     row.NewsTitle,
			Summary:   row.NewsSummary,
			MediaLink: mediaLink,
		},
	}, nil
}
