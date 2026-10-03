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

// ToneService defines the interface for tone business logic
type ToneService interface {
	Create(req models.CreateToneRequest) (*models.ToneResponse, error)
	GetByID(id uuid.UUID) (*models.ToneResponse, error)
	Update(id uuid.UUID, req models.UpdateToneRequest) (*models.ToneResponse, error)
	List(deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize, page, pageSize int) (*pagination.Result[models.ToneResponse], error)
}

type toneService struct {
	repo            repository.ToneRepository
	publicURLBase   string
	bucketName      string
	r2ClientFactory *r2ConfigService.R2ClientFactory
}

// NewToneService creates a new instance of ToneService.
// publicURLBase is the R2 public bucket base URL used to build audio/thumbnail URLs
// embedded in responses (e.g. from R2_SANSKAAR_TONES_PUBLIC_URL). bucketName and
// r2ClientFactory are used to delete old audio/thumbnail files from R2 when Update replaces them.
func NewToneService(repo repository.ToneRepository, publicURLBase, bucketName string, r2ClientFactory *r2ConfigService.R2ClientFactory) ToneService {
	return &toneService{repo: repo, publicURLBase: publicURLBase, bucketName: bucketName, r2ClientFactory: r2ClientFactory}
}

// Create creates a new tone
func (s *toneService) Create(req models.CreateToneRequest) (*models.ToneResponse, error) {
	if err := r2cleanup.VerifyFilesExist(s.r2ClientFactory, constants.AppNameSanskaar, s.bucketName, req.AudioFileKey, req.ThumbnailFileKey); err != nil {
		return nil, err
	}

	tone := &models.Tone{
		Title:            req.Title,
		Deity:            req.Deity,
		AudioFileKey:     req.AudioFileKey,
		ThumbnailFileKey: req.ThumbnailFileKey,
		Duration:         req.Duration,
		Metadata:         req.Metadata,
	}

	if err := s.repo.Create(tone); err != nil {
		return nil, err
	}
	resp := tone.ToResponse(s.publicURLBase)
	return &resp, nil
}

// GetByID retrieves a tone by ID
func (s *toneService) GetByID(id uuid.UUID) (*models.ToneResponse, error) {
	tone, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("tone not found")
		}
		return nil, err
	}
	resp := tone.ToResponse(s.publicURLBase)
	return &resp, nil
}

// Update updates an existing tone
func (s *toneService) Update(id uuid.UUID, req models.UpdateToneRequest) (*models.ToneResponse, error) {
	tone, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("tone not found")
		}
		return nil, err
	}

	var newFileKeys []string
	if req.AudioFileKey != nil {
		newFileKeys = append(newFileKeys, *req.AudioFileKey)
	}
	if req.ThumbnailFileKey != nil {
		newFileKeys = append(newFileKeys, *req.ThumbnailFileKey)
	}
	if err := r2cleanup.VerifyFilesExist(s.r2ClientFactory, constants.AppNameSanskaar, s.bucketName, newFileKeys...); err != nil {
		return nil, err
	}

	oldAudioFileKey := tone.AudioFileKey
	oldThumbnailFileKey := tone.ThumbnailFileKey

	if req.Title != nil {
		tone.Title = *req.Title
	}
	if req.Deity != nil {
		tone.Deity = *req.Deity
	}
	if req.AudioFileKey != nil {
		tone.AudioFileKey = *req.AudioFileKey
	}
	if req.ThumbnailFileKey != nil {
		tone.ThumbnailFileKey = *req.ThumbnailFileKey
	}
	if req.Duration != nil {
		tone.Duration = *req.Duration
	}
	if len(req.Metadata) > 0 {
		if tone.Metadata == nil {
			tone.Metadata = make(utils.Metadata)
		}
		for key, value := range req.Metadata {
			tone.Metadata[key] = value
		}
	}

	if err := s.repo.Update(tone); err != nil {
		return nil, err
	}

	var replacedKeys []string
	if oldAudioFileKey != tone.AudioFileKey {
		replacedKeys = append(replacedKeys, oldAudioFileKey)
	}
	if oldThumbnailFileKey != tone.ThumbnailFileKey {
		replacedKeys = append(replacedKeys, oldThumbnailFileKey)
	}
	r2cleanup.DeleteOldFiles(s.r2ClientFactory, constants.AppNameSanskaar, s.bucketName, replacedKeys...)

	resp := tone.ToResponse(s.publicURLBase)
	return &resp, nil
}

// List retrieves tones with an optional deity filter, pagination, and progressive-unlock
// eligibility when userCreatedAt is provided.
func (s *toneService) List(deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize, page, pageSize int) (*pagination.Result[models.ToneResponse], error) {
	params := pagination.NormalizeParams(page, pageSize)

	tones, total, err := s.repo.FindWithFilters(deity, userCreatedAt, firstDayUnlockCount, dailyUnlockBatchSize, params.Page, params.PageSize)
	if err != nil {
		return nil, err
	}

	responses := make([]models.ToneResponse, len(tones))
	for i, tone := range tones {
		responses[i] = tone.ToResponse(s.publicURLBase)
	}

	result := pagination.BuildResult(responses, total, params.Page, params.PageSize)
	return &result, nil
}
