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

// StatusService defines the interface for status business logic
type StatusService interface {
	Create(req models.CreateStatusRequest) (*models.StatusResponse, error)
	GetByID(id uuid.UUID) (*models.StatusResponse, error)
	Update(id uuid.UUID, req models.UpdateStatusRequest) (*models.StatusResponse, error)
	List(category, deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize, page, pageSize int) (*pagination.Result[models.StatusResponse], error)
}

type statusService struct {
	repo            repository.StatusRepository
	publicURLBase   string
	bucketName      string
	r2ClientFactory *r2ConfigService.R2ClientFactory
}

// NewStatusService creates a new instance of StatusService.
// publicURLBase is the R2 public bucket base URL used to build media URLs
// embedded in responses (e.g. from R2_SANSKAAR_STATUSES_PUBLIC_URL). bucketName and
// r2ClientFactory are used to delete the old media file from R2 when Update replaces it.
func NewStatusService(repo repository.StatusRepository, publicURLBase, bucketName string, r2ClientFactory *r2ConfigService.R2ClientFactory) StatusService {
	return &statusService{repo: repo, publicURLBase: publicURLBase, bucketName: bucketName, r2ClientFactory: r2ClientFactory}
}

// Create creates a new status
func (s *statusService) Create(req models.CreateStatusRequest) (*models.StatusResponse, error) {
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

	status := &models.Status{
		Deity:            req.Deity,
		MediaFileKey:     req.MediaFileKey,
		Type:             req.Type,
		Category:         req.Category,
		AspectRatio:      req.AspectRatio,
		ThumbnailFileKey: thumbnailFileKey,
		Metadata:         req.Metadata,
	}

	if err := s.repo.Create(status); err != nil {
		return nil, err
	}
	resp := status.ToResponse(s.publicURLBase)
	return &resp, nil
}

// GetByID retrieves a status by ID
func (s *statusService) GetByID(id uuid.UUID) (*models.StatusResponse, error) {
	status, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("status not found")
		}
		return nil, err
	}
	resp := status.ToResponse(s.publicURLBase)
	return &resp, nil
}

// Update updates an existing status
func (s *statusService) Update(id uuid.UUID, req models.UpdateStatusRequest) (*models.StatusResponse, error) {
	status, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("status not found")
		}
		return nil, err
	}

	finalType := status.Type
	if req.Type != nil {
		finalType = *req.Type
	}
	thumbnailFileKey, err := resolveThumbnailFileKey(finalType, req.ThumbnailFileKey, status.ThumbnailFileKey)
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

	oldMediaFileKey := status.MediaFileKey
	oldThumbnailFileKey := status.ThumbnailFileKey

	if req.Deity != nil {
		status.Deity = *req.Deity
	}
	if req.MediaFileKey != nil {
		status.MediaFileKey = *req.MediaFileKey
	}
	if req.Type != nil {
		status.Type = *req.Type
	}
	if req.Category != nil {
		status.Category = *req.Category
	}
	if req.AspectRatio != nil {
		status.AspectRatio = *req.AspectRatio
	}
	status.ThumbnailFileKey = thumbnailFileKey
	if len(req.Metadata) > 0 {
		if status.Metadata == nil {
			status.Metadata = make(utils.Metadata)
		}
		for key, value := range req.Metadata {
			status.Metadata[key] = value
		}
	}

	if err := s.repo.Update(status); err != nil {
		return nil, err
	}

	var replacedKeys []string
	if oldMediaFileKey != status.MediaFileKey {
		replacedKeys = append(replacedKeys, oldMediaFileKey)
	}
	if oldThumbnailFileKey != nil && (status.ThumbnailFileKey == nil || *oldThumbnailFileKey != *status.ThumbnailFileKey) {
		replacedKeys = append(replacedKeys, *oldThumbnailFileKey)
	}
	r2cleanup.DeleteOldFiles(s.r2ClientFactory, constants.AppNameSanskaar, s.bucketName, replacedKeys...)

	resp := status.ToResponse(s.publicURLBase)
	return &resp, nil
}

// List retrieves statuses with optional category/deity filters, pagination, and
// progressive-unlock eligibility when userCreatedAt is provided.
func (s *statusService) List(category, deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize, page, pageSize int) (*pagination.Result[models.StatusResponse], error) {
	params := pagination.NormalizeParams(page, pageSize)

	statuses, total, err := s.repo.FindWithFilters(category, deity, userCreatedAt, firstDayUnlockCount, dailyUnlockBatchSize, params.Page, params.PageSize)
	if err != nil {
		return nil, err
	}

	responses := make([]models.StatusResponse, len(statuses))
	for i, status := range statuses {
		responses[i] = status.ToResponse(s.publicURLBase)
	}

	result := pagination.BuildResult(responses, total, params.Page, params.PageSize)
	return &result, nil
}
