package service

import (
	"errors"

	"go-backend/internal/apps/meta_event/models"
	"go-backend/internal/apps/meta_event/repository"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// MetaEventService defines the interface for meta event business logic
type MetaEventService interface {
	CreateMetaEvent(tx *gorm.DB, userID uuid.UUID, appName string, name string, metadata utils.Metadata) (*models.MetaEvent, error)
	GetPendingEvent(userID uuid.UUID, appName string) (*models.MetaEvent, error)
	UpdateMetaEvent(id uuid.UUID, req models.UpdateMetaEventRequest) (*models.MetaEvent, error)
}

// metaEventService implements MetaEventService interface
type metaEventService struct {
	repo repository.MetaEventRepository
}

// NewMetaEventService creates a new instance of MetaEventService
func NewMetaEventService(repo repository.MetaEventRepository) MetaEventService {
	return &metaEventService{repo: repo}
}

// CreateMetaEvent creates a new meta event within a transaction
func (s *metaEventService) CreateMetaEvent(tx *gorm.DB, userID uuid.UUID, appName string, name string, metadata utils.Metadata) (*models.MetaEvent, error) {
	event := &models.MetaEvent{
		UserID:    userID,
		AppName:   appName,
		Name:      name,
		Triggered: false,
		Metadata:  metadata,
	}

	if err := s.repo.Create(tx, event); err != nil {
		return nil, err
	}

	return event, nil
}

// GetPendingEvent returns the oldest non-triggered meta event for a user and app
func (s *metaEventService) GetPendingEvent(userID uuid.UUID, appName string) (*models.MetaEvent, error) {
	event, err := s.repo.FindOldestNonTriggered(userID, appName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return event, nil
}

// UpdateMetaEvent updates a meta event by ID
func (s *metaEventService) UpdateMetaEvent(id uuid.UUID, req models.UpdateMetaEventRequest) (*models.MetaEvent, error) {
	event, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("meta event not found")
		}
		return nil, err
	}

	if req.Triggered != nil {
		event.Triggered = *req.Triggered
	}
	if req.Metadata != nil {
		event.Metadata = req.Metadata
	}

	if err := s.repo.Update(nil, event); err != nil {
		return nil, err
	}

	return event, nil
}
