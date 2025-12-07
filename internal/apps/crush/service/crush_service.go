package service

import (
	"errors"
	"strings"

	"go-backend/internal/apps/crush/models"
	"go-backend/internal/apps/crush/repository"
	userModels "go-backend/internal/apps/user/models"
	userRepository "go-backend/internal/apps/user/repository"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CrushService defines the interface for crush business logic
type CrushService interface {
	CreateCrush(req models.CreateCrushRequest) (*models.CrushResponse, error)
	UpdateCrush(id uuid.UUID, req models.UpdateCrushRequest) (*models.CrushResponse, error)
	GetCrushByID(id uuid.UUID) (*models.CrushResponse, error)
	ListCrushesByUserID(userID uuid.UUID) ([]models.CrushResponse, error)
	ListCrushesOnUser(userID uuid.UUID) ([]models.CrushOnUserResponse, error)
	ListAllCrushes() ([]models.AllCrushesResponse, error)
}

// crushService implements CrushService
type crushService struct {
	repo     repository.CrushRepository
	userRepo userRepository.UserRepository
}

// NewCrushService creates a new instance of CrushService
func NewCrushService(repo repository.CrushRepository, userRepo userRepository.UserRepository) CrushService {
	return &crushService{
		repo:     repo,
		userRepo: userRepo,
	}
}

// validateContactMethod ensures at least one contact method is provided
// (country_code + phone) OR instagram_id OR snapchat_id
func validateContactMethod(countryCode, phone, instagramID, snapchatID *string) error {
	phonePresent := phone != nil && strings.TrimSpace(*phone) != ""
	ccPresent := countryCode != nil && strings.TrimSpace(*countryCode) != ""
	instagramPresent := instagramID != nil && strings.TrimSpace(*instagramID) != ""
	snapchatPresent := snapchatID != nil && strings.TrimSpace(*snapchatID) != ""

	// Check if at least one contact method is present
	hasPhone := phonePresent && ccPresent
	hasInstagram := instagramPresent
	hasSnapchat := snapchatPresent

	if !hasPhone && !hasInstagram && !hasSnapchat {
		return errors.New("at least one contact method is required: (country_code + phone), instagram_id, or snapchat_id")
	}

	// Validate that if one part of phone is present, both must be present
	if (phonePresent && !ccPresent) || (!phonePresent && ccPresent) {
		return errors.New("both country_code and phone must be provided together")
	}

	return nil
}

// isSelfCrush checks if the crush identifiers match the user's own identifiers
func isSelfCrush(user *userModels.User, crushCountryCode, crushPhone, crushInstagramID, crushSnapchatID *string) bool {
	// Check phone match
	if crushCountryCode != nil && crushPhone != nil && *crushCountryCode != "" && *crushPhone != "" {
		if user.CountryCode != nil && user.Phone != nil &&
			*user.CountryCode == *crushCountryCode && *user.Phone == *crushPhone {
			return true
		}
	}

	// Check Instagram ID match from user metadata
	if crushInstagramID != nil && *crushInstagramID != "" && user.Metadata != nil {
		if userInsta, ok := user.Metadata["instagram_id"].(string); ok && userInsta != "" {
			if userInsta == *crushInstagramID {
				return true
			}
		}
	}

	// Check Snapchat ID match from user metadata
	if crushSnapchatID != nil && *crushSnapchatID != "" && user.Metadata != nil {
		if userSnap, ok := user.Metadata["snapchat_id"].(string); ok && userSnap != "" {
			if userSnap == *crushSnapchatID {
				return true
			}
		}
	}

	return false
}

// CreateCrush creates a new crush entry
func (s *crushService) CreateCrush(req models.CreateCrushRequest) (*models.CrushResponse, error) {
	// Validate and trim name
	trimmedName := strings.TrimSpace(req.Name)
	if trimmedName == "" {
		return nil, errors.New("name is required and cannot be empty")
	}

	// Prevent users from adding themselves as a crush
	user, err := s.userRepo.FindByID(req.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}

	// Check if crush identifiers match the user's own identifiers
	if isSelfCrush(user, req.CountryCode, req.Phone, req.InstagramID, req.SnapchatID) {
		return nil, errors.New("you cannot add yourself as a crush")
	}

	if err := validateContactMethod(req.CountryCode, req.Phone, req.InstagramID, req.SnapchatID); err != nil {
		return nil, err
	}

	// Build model
	crush := &models.Crush{
		UserID:      req.UserID,
		Name:        trimmedName,
		CountryCode: req.CountryCode,
		Phone:       req.Phone,
		InstagramID: req.InstagramID,
		SnapchatID:  req.SnapchatID,
		Metadata:    req.Metadata,
	}

	if err := s.repo.Create(crush); err != nil {
		return nil, err
	}
	resp := crush.ToResponse()
	return &resp, nil
}

// UpdateCrush updates an existing crush entry
func (s *crushService) UpdateCrush(id uuid.UUID, req models.UpdateCrushRequest) (*models.CrushResponse, error) {
	crush, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("crush not found")
		}
		return nil, err
	}

	// Apply updates if provided
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			return nil, errors.New("name cannot be empty")
		}
		crush.Name = trimmed
	}
	if req.CountryCode != nil {
		crush.CountryCode = req.CountryCode
	}
	if req.Phone != nil {
		crush.Phone = req.Phone
	}
	if req.InstagramID != nil {
		crush.InstagramID = req.InstagramID
	}
	if req.SnapchatID != nil {
		crush.SnapchatID = req.SnapchatID
	}
	// Merge metadata if provided (partial update)
	if len(req.Metadata) > 0 {
		if crush.Metadata == nil {
			crush.Metadata = make(models.Metadata)
		}
		for key, value := range req.Metadata {
			crush.Metadata[key] = value
		}
	}

	// Validate that at least one contact method remains after update
	if err := validateContactMethod(crush.CountryCode, crush.Phone, crush.InstagramID, crush.SnapchatID); err != nil {
		return nil, err
	}

	// Prevent users from updating crush to match their own identifiers
	user, err := s.userRepo.FindByID(crush.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}

	if isSelfCrush(user, crush.CountryCode, crush.Phone, crush.InstagramID, crush.SnapchatID) {
		return nil, errors.New("you cannot add yourself as a crush")
	}

	if err := s.repo.Update(crush); err != nil {
		return nil, err
	}
	resp := crush.ToResponse()
	return &resp, nil
}

// ListCrushesByUserID retrieves all crushes for a specific user
func (s *crushService) ListCrushesByUserID(userID uuid.UUID) ([]models.CrushResponse, error) {
	crushes, err := s.repo.FindByUserID(userID)
	if err != nil {
		return nil, err
	}

	responses := make([]models.CrushResponse, len(crushes))
	for i, crush := range crushes {
		responses[i] = crush.ToResponse()
	}
	return responses, nil
}

// GetCrushByID retrieves a crush by its ID
func (s *crushService) GetCrushByID(id uuid.UUID) (*models.CrushResponse, error) {
	crush, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("crush not found")
		}
		return nil, err
	}
	resp := crush.ToResponse()
	return &resp, nil
}

// ListCrushesOnUser lists all people who have a crush on the user
// Matches based on user's phone, Instagram ID, or Snapchat ID
func (s *crushService) ListCrushesOnUser(userID uuid.UUID) ([]models.CrushOnUserResponse, error) {
	// Get user details
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("user not found")
		}
		return nil, err
	}

	// Extract Instagram and Snapchat IDs from metadata if present
	var instagramID, snapchatID *string
	if user.Metadata != nil {
		if insta, ok := user.Metadata["instagram_id"].(string); ok && insta != "" {
			instagramID = &insta
		}
		if snap, ok := user.Metadata["snapchat_id"].(string); ok && snap != "" {
			snapchatID = &snap
		}
	}

	// Find all crushes matching any of the user's identifiers
	crushes, err := s.repo.FindCrushesOnUser(user.CountryCode, user.Phone, instagramID, snapchatID)
	if err != nil {
		return nil, err
	}

	responses := make([]models.CrushOnUserResponse, len(crushes))
	for i, crush := range crushes {
		responses[i] = crush.ToMinimalResponse()
	}
	return responses, nil
}

// ListAllCrushes retrieves all crushes with user and crush phone numbers
func (s *crushService) ListAllCrushes() ([]models.AllCrushesResponse, error) {
	crushes, err := s.repo.FindAll()
	if err != nil {
		return nil, err
	}

	// Build response with user phone numbers
	responses := make([]models.AllCrushesResponse, 0, len(crushes))
	for _, crush := range crushes {
		// Get user details to fetch phone number
		user, err := s.userRepo.FindByID(crush.UserID)
		if err != nil {
			// Skip crushes where user is not found
			continue
		}

		responses = append(responses, models.AllCrushesResponse{
			UserCountryCode:  user.CountryCode,
			UserPhone:        user.Phone,
			CrushCountryCode: crush.CountryCode,
			CrushPhone:       crush.Phone,
			InstagramID:      crush.InstagramID,
			SnapchatID:       crush.SnapchatID,
			CreatedAt:        crush.CreatedAt,
		})
	}
	return responses, nil
}
