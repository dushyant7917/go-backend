package service

import (
	"errors"
	"os"
	"time"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/repository"
	"go-backend/pkg/utils"

	"gorm.io/gorm"
)

// NewsService defines the interface for news business logic
type NewsService interface {
	ListNewsPaginated(category, languageCode, status string, createdAtFrom *time.Time, page, pageSize int) (*models.PaginatedNewsResponse, error)
	UpdateNews(id string, req *models.UpdateNewsRequest) (*models.News, error)
}

// newsService implements NewsService
type newsService struct {
	repo repository.NewsRepository
}

// NewNewsService creates a new instance of NewsService
func NewNewsService(repo repository.NewsRepository) NewsService {
	return &newsService{repo: repo}
}

// ListNewsPaginated retrieves news with pagination and optional filters
func (s *newsService) ListNewsPaginated(category, languageCode, status string, createdAtFrom *time.Time, page, pageSize int) (*models.PaginatedNewsResponse, error) {
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

	news, total, err := s.repo.FindAllPaginated(category, languageCode, status, createdAtFrom, page, pageSize)
	if err != nil {
		return nil, err
	}

	// Get R2 public URL base for constructing media_link
	publicURLBase := os.Getenv("R2_DS_NEWS_PUBLIC_URL")

	// Construct media_link from media_file_key for each news item
	for i := range news {
		if news[i].MediaFileKey != nil && *news[i].MediaFileKey != "" && publicURLBase != "" {
			mediaLink := publicURLBase + "/" + *news[i].MediaFileKey
			news[i].MediaLink = &mediaLink
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

	return &models.PaginatedNewsResponse{
		Data:       news,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		NextPage:   nextPage,
		PrevPage:   prevPage,
	}, nil
}

// UpdateNews updates a news article
func (s *newsService) UpdateNews(id string, req *models.UpdateNewsRequest) (*models.News, error) {
	// Find existing news
	news, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("news not found")
		}
		return nil, err
	}

	// Update fields if provided
	if req.Link != nil {
		news.Link = *req.Link
	}
	if req.MediaFileKey != nil {
		news.MediaFileKey = req.MediaFileKey
	}
	if req.Category != nil {
		news.Category = *req.Category
	}
	if req.Status != nil {
		news.Status = *req.Status
	}
	if req.PublishedAt != nil {
		news.PublishedAt = req.PublishedAt
	}
	if req.Metadata != nil {
		// Merge metadata: only update/add provided keys, preserve existing
		if news.Metadata == nil {
			news.Metadata = make(utils.Metadata)
		}
		for key, value := range req.Metadata {
			news.Metadata[key] = value
		}
	}

	// Save changes
	if err := s.repo.Update(news); err != nil {
		return nil, err
	}

	return news, nil
}
