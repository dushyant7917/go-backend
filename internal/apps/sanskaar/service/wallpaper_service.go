package service

import (
	"errors"
	"time"

	r2ConfigService "go-backend/internal/apps/r2/config/service"
	"go-backend/internal/apps/sanskaar/models"
	"go-backend/internal/apps/sanskaar/repository"
	"go-backend/internal/common/constants"
	"go-backend/internal/common/pagination"
	"go-backend/internal/common/r2cleanup"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// WallpaperService defines the interface for wallpaper business logic
type WallpaperService interface {
	Create(req models.CreateWallpaperRequest) (*models.WallpaperResponse, error)
	GetByID(id uuid.UUID) (*models.WallpaperResponse, error)
	Update(id uuid.UUID, req models.UpdateWallpaperRequest) (*models.WallpaperResponse, error)
	List(deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize, page, pageSize int) (*pagination.Result[models.WallpaperResponse], error)
}

type wallpaperService struct {
	repo            repository.WallpaperRepository
	publicURLBase   string
	bucketName      string
	r2ClientFactory *r2ConfigService.R2ClientFactory
}

// NewWallpaperService creates a new instance of WallpaperService.
// publicURLBase is the R2 public bucket base URL used to build media URLs
// embedded in responses (e.g. from R2_SANSKAAR_WALLPAPERS_PUBLIC_URL). bucketName and
// r2ClientFactory are used to delete the old media file from R2 when Update replaces it.
func NewWallpaperService(repo repository.WallpaperRepository, publicURLBase, bucketName string, r2ClientFactory *r2ConfigService.R2ClientFactory) WallpaperService {
	return &wallpaperService{repo: repo, publicURLBase: publicURLBase, bucketName: bucketName, r2ClientFactory: r2ClientFactory}
}

// Create creates a new wallpaper
func (s *wallpaperService) Create(req models.CreateWallpaperRequest) (*models.WallpaperResponse, error) {
	thumbnailFileKey, err := resolveThumbnailFileKey(req.Type, req.ThumbnailFileKey, nil)
	if err != nil {
		return nil, err
	}

	verifyKeys := []string{req.MediaFileKey}
	if thumbnailFileKey != nil {
		verifyKeys = append(verifyKeys, *thumbnailFileKey)
	}
	if err := r2cleanup.VerifyFilesExist(s.r2ClientFactory, constants.AppNameSanskaar, s.bucketName, verifyKeys...); err != nil {
		return nil, err
	}

	wallpaper := &models.Wallpaper{
		Deity:            req.Deity,
		MediaFileKey:     req.MediaFileKey,
		Type:             req.Type,
		ThumbnailFileKey: thumbnailFileKey,
		Metadata:         req.Metadata,
	}

	if err := s.repo.Create(wallpaper); err != nil {
		return nil, err
	}
	resp := wallpaper.ToResponse(s.publicURLBase)
	return &resp, nil
}

// GetByID retrieves a wallpaper by ID
func (s *wallpaperService) GetByID(id uuid.UUID) (*models.WallpaperResponse, error) {
	wallpaper, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("wallpaper not found")
		}
		return nil, err
	}
	resp := wallpaper.ToResponse(s.publicURLBase)
	return &resp, nil
}

// Update updates an existing wallpaper
func (s *wallpaperService) Update(id uuid.UUID, req models.UpdateWallpaperRequest) (*models.WallpaperResponse, error) {
	wallpaper, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("wallpaper not found")
		}
		return nil, err
	}

	finalType := wallpaper.Type
	if req.Type != nil {
		finalType = *req.Type
	}
	thumbnailFileKey, err := resolveThumbnailFileKey(finalType, req.ThumbnailFileKey, wallpaper.ThumbnailFileKey)
	if err != nil {
		return nil, err
	}

	var newFileKeys []string
	if req.MediaFileKey != nil {
		newFileKeys = append(newFileKeys, *req.MediaFileKey)
	}
	if req.ThumbnailFileKey != nil {
		newFileKeys = append(newFileKeys, *req.ThumbnailFileKey)
	}
	if err := r2cleanup.VerifyFilesExist(s.r2ClientFactory, constants.AppNameSanskaar, s.bucketName, newFileKeys...); err != nil {
		return nil, err
	}

	oldMediaFileKey := wallpaper.MediaFileKey
	oldThumbnailFileKey := wallpaper.ThumbnailFileKey

	if req.Deity != nil {
		wallpaper.Deity = *req.Deity
	}
	if req.MediaFileKey != nil {
		wallpaper.MediaFileKey = *req.MediaFileKey
	}
	if req.Type != nil {
		wallpaper.Type = *req.Type
	}
	wallpaper.ThumbnailFileKey = thumbnailFileKey
	if len(req.Metadata) > 0 {
		if wallpaper.Metadata == nil {
			wallpaper.Metadata = make(utils.Metadata)
		}
		for key, value := range req.Metadata {
			wallpaper.Metadata[key] = value
		}
	}

	if err := s.repo.Update(wallpaper); err != nil {
		return nil, err
	}

	var replacedKeys []string
	if oldMediaFileKey != wallpaper.MediaFileKey {
		replacedKeys = append(replacedKeys, oldMediaFileKey)
	}
	if oldThumbnailFileKey != nil && (wallpaper.ThumbnailFileKey == nil || *oldThumbnailFileKey != *wallpaper.ThumbnailFileKey) {
		replacedKeys = append(replacedKeys, *oldThumbnailFileKey)
	}
	r2cleanup.DeleteOldFiles(s.r2ClientFactory, constants.AppNameSanskaar, s.bucketName, replacedKeys...)

	resp := wallpaper.ToResponse(s.publicURLBase)
	return &resp, nil
}

// List retrieves wallpapers with an optional deity filter, pagination, and progressive-unlock
// eligibility when userCreatedAt is provided.
func (s *wallpaperService) List(deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize, page, pageSize int) (*pagination.Result[models.WallpaperResponse], error) {
	params := pagination.NormalizeParams(page, pageSize)

	wallpapers, total, err := s.repo.FindWithFilters(deity, userCreatedAt, firstDayUnlockCount, dailyUnlockBatchSize, params.Page, params.PageSize)
	if err != nil {
		return nil, err
	}

	responses := make([]models.WallpaperResponse, len(wallpapers))
	for i, wallpaper := range wallpapers {
		responses[i] = wallpaper.ToResponse(s.publicURLBase)
	}

	result := pagination.BuildResult(responses, total, params.Page, params.PageSize)
	return &result, nil
}
