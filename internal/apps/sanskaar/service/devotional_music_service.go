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

// DevotionalMusicService defines the interface for devotional music business logic
type DevotionalMusicService interface {
	Create(req models.CreateDevotionalMusicRequest) (*models.DevotionalMusicResponse, error)
	GetByID(id uuid.UUID) (*models.DevotionalMusicResponse, error)
	Update(id uuid.UUID, req models.UpdateDevotionalMusicRequest) (*models.DevotionalMusicResponse, error)
	List(category, deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize, page, pageSize int) (*pagination.Result[models.DevotionalMusicResponse], error)
}

type devotionalMusicService struct {
	repo            repository.DevotionalMusicRepository
	publicURLBase   string
	bucketName      string
	r2ClientFactory *r2ConfigService.R2ClientFactory
}

// NewDevotionalMusicService creates a new instance of DevotionalMusicService.
// publicURLBase is the R2 public bucket base URL used to build audio/thumbnail URLs
// embedded in responses (e.g. from R2_SANSKAAR_DEVOTIONAL_MUSIC_PUBLIC_URL). bucketName and
// r2ClientFactory are used to delete old audio/thumbnail files from R2 when Update replaces them.
func NewDevotionalMusicService(repo repository.DevotionalMusicRepository, publicURLBase, bucketName string, r2ClientFactory *r2ConfigService.R2ClientFactory) DevotionalMusicService {
	return &devotionalMusicService{repo: repo, publicURLBase: publicURLBase, bucketName: bucketName, r2ClientFactory: r2ClientFactory}
}

// Create creates a new devotional music track
func (s *devotionalMusicService) Create(req models.CreateDevotionalMusicRequest) (*models.DevotionalMusicResponse, error) {
	if err := r2cleanup.VerifyFilesExist(s.r2ClientFactory, constants.AppNameSanskaar, s.bucketName, req.AudioFileKey, req.ThumbnailFileKey); err != nil {
		return nil, err
	}

	music := &models.DevotionalMusic{
		Title:            req.Title,
		Deity:            req.Deity,
		Category:         req.Category,
		AudioFileKey:     req.AudioFileKey,
		ThumbnailFileKey: req.ThumbnailFileKey,
		Duration:         req.Duration,
		Metadata:         req.Metadata,
	}

	if err := s.repo.Create(music); err != nil {
		return nil, err
	}
	resp := music.ToResponse(s.publicURLBase)
	return &resp, nil
}

// GetByID retrieves a devotional music track by ID
func (s *devotionalMusicService) GetByID(id uuid.UUID) (*models.DevotionalMusicResponse, error) {
	music, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("devotional music not found")
		}
		return nil, err
	}
	resp := music.ToResponse(s.publicURLBase)
	return &resp, nil
}

// Update updates an existing devotional music track
func (s *devotionalMusicService) Update(id uuid.UUID, req models.UpdateDevotionalMusicRequest) (*models.DevotionalMusicResponse, error) {
	music, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("devotional music not found")
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

	oldAudioFileKey := music.AudioFileKey
	oldThumbnailFileKey := music.ThumbnailFileKey

	if req.Title != nil {
		music.Title = *req.Title
	}
	if req.Deity != nil {
		music.Deity = *req.Deity
	}
	if req.Category != nil {
		music.Category = *req.Category
	}
	if req.AudioFileKey != nil {
		music.AudioFileKey = *req.AudioFileKey
	}
	if req.ThumbnailFileKey != nil {
		music.ThumbnailFileKey = *req.ThumbnailFileKey
	}
	if req.Duration != nil {
		music.Duration = *req.Duration
	}
	if len(req.Metadata) > 0 {
		if music.Metadata == nil {
			music.Metadata = make(utils.Metadata)
		}
		for key, value := range req.Metadata {
			music.Metadata[key] = value
		}
	}

	if err := s.repo.Update(music); err != nil {
		return nil, err
	}

	var replacedKeys []string
	if oldAudioFileKey != music.AudioFileKey {
		replacedKeys = append(replacedKeys, oldAudioFileKey)
	}
	if oldThumbnailFileKey != music.ThumbnailFileKey {
		replacedKeys = append(replacedKeys, oldThumbnailFileKey)
	}
	r2cleanup.DeleteOldFiles(s.r2ClientFactory, constants.AppNameSanskaar, s.bucketName, replacedKeys...)

	resp := music.ToResponse(s.publicURLBase)
	return &resp, nil
}

// List retrieves devotional music tracks with optional category/deity filters, pagination, and
// progressive-unlock eligibility when userCreatedAt is provided.
func (s *devotionalMusicService) List(category, deity string, userCreatedAt *time.Time, firstDayUnlockCount, dailyUnlockBatchSize, page, pageSize int) (*pagination.Result[models.DevotionalMusicResponse], error) {
	params := pagination.NormalizeParams(page, pageSize)

	tracks, total, err := s.repo.FindWithFilters(category, deity, userCreatedAt, firstDayUnlockCount, dailyUnlockBatchSize, params.Page, params.PageSize)
	if err != nil {
		return nil, err
	}

	responses := make([]models.DevotionalMusicResponse, len(tracks))
	for i, track := range tracks {
		responses[i] = track.ToResponse(s.publicURLBase)
	}

	result := pagination.BuildResult(responses, total, params.Page, params.PageSize)
	return &result, nil
}
